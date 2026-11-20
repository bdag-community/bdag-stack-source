package consensus

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"golang.org/x/crypto/sha3"
)

var (
	errUnclesUnsupported = errors.New("uncles unsupported")
)

func (me *bdagEngine) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase, nil
}

func (me *bdagEngine) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	// Short circuit if the header is known, or its parent not
	number := header.Number.Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return me.verifyHeader(chain, header, parent)
}

func (me *bdagEngine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	if len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}

	// Spawn as many workers as allowed threads
	workers := runtime.GOMAXPROCS(0)
	if len(headers) < workers {
		workers = len(headers)
	}

	// Create a task channel and spawn the verifiers
	var (
		inputs = make(chan int)
		done   = make(chan int, workers)
		errors = make([]error, len(headers))
		abort  = make(chan struct{})
	)
	for i := 0; i < workers; i++ {
		go func() {
			for index := range inputs {
				errors[index] = me.verifyHeaderWorker(chain, headers, index)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
		defer close(inputs)
		var (
			in, out = 0, 0
			checked = make([]bool, len(headers))
			inputs  = inputs
		)
		for {
			select {
			case inputs <- in:
				if in++; in == len(headers) {
					// Reached end of headers. Stop sending to workers.
					inputs = nil
				}
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == len(headers)-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()
	return abort, errorsOut
}

func (me *bdagEngine) verifyHeaderWorker(chain consensus.ChainHeaderReader, headers []*types.Header, index int) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash, headers[0].Number.Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return me.verifyHeader(chain, headers[index], parent)
}

func (me *bdagEngine) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	if len(block.Uncles()) > 0 {
		return errUnclesUnsupported
	}
	return nil
}

func (me *bdagEngine) verifyHeader(chain consensus.ChainHeaderReader, header, parent *types.Header) error {
	// Verify that the gas limit is <= 2^63-1
	cap := uint64(0x7fffffffffffffff)
	if header.GasLimit > cap {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, cap)
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}
	if params.IsMainnetChainID(chain.Config().ChainID) && header.GasLimit != params.MainnetFixedGasLimit {
		return fmt.Errorf("invalid mainnet gasLimit: have %d, want %d", header.GasLimit, params.MainnetFixedGasLimit)
	}
	// Verify the block's gas usage and (if applicable) verify the base fee.
	if !chain.Config().IsLondon(header.Number) {
		// Verify BaseFee not present before EIP-1559 fork.
		if header.BaseFee != nil {
			return fmt.Errorf("invalid baseFee before fork: have %d, expected 'nil'", header.BaseFee)
		}
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number, parent.Number); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyDAOHeaderExtraData(chain.Config(), header); err != nil {
		return err
	}
	if err := ValidateHeaderRewardExtra(header.Number, header.Extra); err != nil {
		return err
	}
	return nil
}

func (me *bdagEngine) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	return big.NewInt(1)
}

func (me *bdagEngine) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	header.Difficulty = big.NewInt(1)
	return nil
}

func (me *bdagEngine) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, body *types.Body) {
	// Importers/builders already prepare before transactions. Revalidation here
	// is idempotent and protects direct engine callers before any balance writes.
	if err := prepareVersionedRewardClock(chain.Config(), header, state); err != nil {
		me.log.Crit("versioned reward clock validation failed", "err", err)
	}
	// 1. Reward Parsing happens in OnExtraStateChange
	totalSubsidy := me.OnExtraStateChange(chain, header, state)

	if totalSubsidy == nil {
		totalSubsidy = big.NewInt(0)
	}

	// 2. Determine Epoch & Split Ratio
	currentEpoch := stakingEpochForHeader(header)
	// ==========================================
	// ACTIVATION EPOCH CHECK
	// ==========================================
	if currentEpoch < qparams.ActiveNetParams.StakingActivationEpoch {
		// --- OLD LOGIC: 100% TO MINER ---
		if totalSubsidy.Sign() > 0 {
			state.AddBalance(header.Coinbase, uint256.MustFromBig(totalSubsidy), tracing.BalanceIncreaseRewardMineBlock)
		}

		// Legacy Support
		me.updateStakingTallies(chain, header, state, totalSubsidy)
		header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
		return
	}

	minerSplit := minerSplitFromState(header, state)

	// 3. Calculate Shares
	// Miner Share = Total * Split / 100
	if totalSubsidy == nil {
		totalSubsidy = big.NewInt(0)
	}
	liquidReward := new(big.Int).Mul(totalSubsidy, minerSplit)
	liquidReward.Div(liquidReward, big.NewInt(100))

	// Stakers Share = Total - Miner Share
	stakersShare := new(big.Int).Sub(totalSubsidy, liquidReward)

	// Immediate Payout (The 70% miner reward is paid immediately on every block)
	if liquidReward.Sign() > 0 {
		state.AddBalance(header.Coinbase, uint256.MustFromBig(liquidReward), tracing.BalanceIncreaseRewardMineBlock)
	}

	// 4. Update Staking Pool State (Reward Distribution Logic)
	// We assume Coinbase IS the Pool Address
	poolAddr := header.Coinbase
	poolTotalAssetsSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalAssetsBase)
	poolTotalAssets := state.GetState(activeStakingAddress(header, state), poolTotalAssetsSlot).Big()
	if poolTotalAssets == nil {
		poolTotalAssets = big.NewInt(0)
	}

	var distributableStakerReward *big.Int = big.NewInt(0)

	// ELIGIBILITY CHECK: Pool Stake > 0
	// --- A. AUTO-STAKE (3% of Total / 10% of Stakers Share) ---
	// This happens regardless of existing stake (miners can start new pools)
	autoStakeReward := new(big.Int).Mul(stakersShare, big.NewInt(int64(params.AutoStakePct)))
	autoStakeReward.Div(autoStakeReward, big.NewInt(100))

	// Pay to Staking Contract (Balance)
	state.AddBalance(activeStakingAddress(header, state), uint256.MustFromBig(autoStakeReward), tracing.BalanceIncreaseRewardMineBlock)

	// Update Storage (Lock & Compound Stake)
	if autoStakeReward.Sign() > 0 {
		me.updateAutoStake(state, header, autoStakeReward)
	}

	// --- B. STAKER DISTRIBUTION (27% of Total) ---
	distributableStakerReward.Sub(stakersShare, autoStakeReward)

	// V2 Vault Math: We simply dump the 27% yield directly into the pool's TotalStaked (The Vault).
	// Because the Total Shares stay exactly the same, the Share Price instantly Inflates.
	// This creates Gasless Auto-Compounding for every single staker in the pool simultaneously!
	poolTotalAssetsSlot = calculateMappingSlot(poolAddr, params.SlotPoolTotalAssetsBase)
	poolTotalAssets = state.GetState(activeStakingAddress(header, state), poolTotalAssetsSlot).Big()
	if poolTotalAssets == nil {
		poolTotalAssets = big.NewInt(0)
	}

	if poolTotalAssets.Sign() > 0 && distributableStakerReward.Sign() > 0 {
		if rewardVotingEnabled(chain, header, state) {
			me.updateStakingRewardVotingLedger(header, state, distributableStakerReward)
		}
		newTotalAssets := new(big.Int).Add(poolTotalAssets, distributableStakerReward)
		state.SetState(activeStakingAddress(header, state), poolTotalAssetsSlot, common.BigToHash(newTotalAssets))

		// Credit Stakers Pot
		state.AddBalance(activeStakingAddress(header, state), uint256.MustFromBig(distributableStakerReward), tracing.BalanceIncreaseRewardMineBlock)
	}

	// Legacy Support (Accumulated Tally in value) - using Total Subsidy
	me.updateStakingTallies(chain, header, state, totalSubsidy)

	// Reward voting is activated only by the separately reviewed governance
	// migration. Mining power is the value actually earned by the coinbase:
	// liquid reward plus its locked operator auto-stake credit.
	if rewardVotingEnabled(chain, header, state) {
		miningEarned := new(big.Int).Add(new(big.Int).Set(liquidReward), autoStakeReward)
		me.updateRewardVotingLedger(header, state, miningEarned)
	}

	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
}

func rewardVotingEnabled(chain consensus.ChainHeaderReader, header *types.Header, statedb *state.StateDB) bool {
	if chain != nil && chain.Config() != nil && chain.Config().BdagMainnetDAO != nil && header != nil && header.Number != nil && statedb != nil {
		marker := statedb.GetState(params.ChainTallyAddr, mainnetDAOAppliedSlot)
		return marker != (common.Hash{}) && marker.Big().Cmp(header.Number) <= 0 &&
			header.Number.Cmp(new(big.Int).SetUint64(chain.Config().BdagMainnetDAO.EarliestEVMBlock)) >= 0
	}
	if chain == nil || chain.Config() == nil || chain.Config().BdagGovernanceForkBlock == nil ||
		chain.Config().BdagGovernanceFork == nil || header == nil || header.Number == nil || statedb == nil {
		return false
	}
	if header.Number.Cmp(chain.Config().BdagGovernanceForkBlock) < 0 {
		return false
	}
	return statedb.GetState(params.ChainTallyAddr, governanceAppliedSlot) != (common.Hash{})
}

func minerSplitFromState(header *types.Header, state *state.StateDB) *big.Int {
	if state != nil {
		minerSplit := state.GetState(activeStakingAddress(header, state), params.SlotMinerSplit).Big()
		if minerSplit != nil && minerSplit.Sign() > 0 {
			return minerSplit
		}
	}
	return big.NewInt(int64(params.DefaultMinerSplitPct))
}

func (me *bdagEngine) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, body *types.Body, receipts []*types.Receipt) (*types.Block, error) {
	if err := ValidateHeaderRewardExtra(header.Number, header.Extra); err != nil {
		return nil, err
	}
	me.Finalize(chain, header, state, body)

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, body, receipts, trie.NewStackTrie(nil)), nil
}

func (me *bdagEngine) SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()

	enc := []any{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	rlp.Encode(hasher, enc)
	hasher.Sum(hash[:0])
	return hash
}

func (me *bdagEngine) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	header := block.Header()
	header.Nonce, header.MixDigest = types.BlockNonce{}, common.Hash{}
	select {
	case results <- block.WithSeal(header):
	default:
		me.log.Warn("Sealing result is not read by miner", "mode", "fake", "sealhash", me.SealHash(block.Header()))
	}
	return nil
}

func (me *bdagEngine) OnExtraStateChange(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB) *big.Int {
	// Parse Total Reward (PoW Subsidy) from Header Extra field (8-byte BigEndian).
	// This happens early in the block lifecycle.
	if !RewardExtraRequired(header.Number) {
		return big.NewInt(0)
	}
	totalSubsidy, err := RewardExtraToWei(header.Extra)
	if err != nil {
		me.log.Error("invalid block reward extra", "err", err)
		return big.NewInt(0)
	}
	return totalSubsidy
}

// updateAutoStake writes the 52-week lock to the contract storage (Slot 8)
// and updates the summary tracking (Slot 12 & 13) using the ERC4626 Share model.
func (me *bdagEngine) updateAutoStake(state *state.StateDB, header *types.Header, amount *big.Int) {
	stakerAddr := header.Coinbase
	poolAddr := header.Coinbase
	// Fresh Mainnet V2 operators may earn only native auto-stake, without ever
	// calling stake(). Record their real first credit so the normal claim gate
	// remains usable. Historical Legacy/Testnet timestamps are never rewritten.
	if state.GetState(params.ChainTallyAddr, mainnetDAOAppliedSlot) != (common.Hash{}) {
		firstSlot := calculateMappingSlot(stakerAddr, params.SlotUserFirstDepositTimestampBase)
		if state.GetState(params.SeparateStakingV2Addr, firstSlot) == (common.Hash{}) {
			floor := state.GetState(params.ChainTallyAddr, params.SlotGovernanceTimeFloor).Big().Uint64()
			first := max(header.Time, floor)
			state.SetState(params.SeparateStakingV2Addr, firstSlot, common.BigToHash(new(big.Int).SetUint64(first)))
		}
	}

	currentEpoch := stakingEpochForHeader(header)

	// 2. Vault Math: Calculate Shares to Mint based on current Share Price
	poolTotalAssetsSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalAssetsBase)
	poolTotalAssets := state.GetState(activeStakingAddress(header, state), poolTotalAssetsSlot).Big()
	if poolTotalAssets == nil {
		poolTotalAssets = big.NewInt(0)
	}

	poolTotalSharesSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalSharesBase)
	poolTotalShares := state.GetState(activeStakingAddress(header, state), poolTotalSharesSlot).Big()
	if poolTotalShares == nil {
		poolTotalShares = big.NewInt(0)
	}

	sharesToMint := big.NewInt(0)

	// If the vault is completely empty, 1 Wei = 1 Share
	if poolTotalShares.Sign() == 0 || poolTotalAssets.Sign() == 0 {
		sharesToMint.Set(amount)
	} else {
		// Optimized Math: sharesToMint = (amount * poolTotalShares) / poolTotalAssets
		// This avoids calculating intermediate sharePrice which causes double-precision loss.
		sharesToMint.Mul(amount, poolTotalShares)
		sharesToMint.Div(sharesToMint, poolTotalAssets)
	}

	// 3. Update poolTotalShares
	newTotalShares := new(big.Int).Add(poolTotalShares, sharesToMint)
	state.SetState(activeStakingAddress(header, state), poolTotalSharesSlot, common.BigToHash(newTotalShares))

	// 4. Update poolEpochAutoStake (Slot 7: pool => epoch => SHARES)
	poolKey := calculateMappingSlot(poolAddr, params.SlotPoolEpochAutoStakeBase)
	epochKey := calculateMappingSlot(common.BigToAddress(big.NewInt(int64(currentEpoch))), poolKey)

	currentAutoStakeShares := state.GetState(activeStakingAddress(header, state), epochKey).Big()
	if currentAutoStakeShares == nil {
		currentAutoStakeShares = big.NewInt(0)
	}
	newAutoStakeShares := new(big.Int).Add(currentAutoStakeShares, sharesToMint)
	state.SetState(activeStakingAddress(header, state), epochKey, common.BigToHash(newAutoStakeShares))

	// 5. Update stakerTotalLockedShares (Slot 12: staker => pool => SHARES)
	stakerSumKey := calculateMappingSlot(stakerAddr, params.SlotStakerTotalLockedSharesBase)
	sumSlot := calculateMappingSlot(poolAddr, stakerSumKey)
	oldTotalLockedShares := state.GetState(activeStakingAddress(header, state), sumSlot).Big()
	if oldTotalLockedShares == nil {
		oldTotalLockedShares = big.NewInt(0)
	}
	newTotalLockedShares := new(big.Int).Add(oldTotalLockedShares, sharesToMint)
	state.SetState(activeStakingAddress(header, state), sumSlot, common.BigToHash(newTotalLockedShares))

	// 6. Update poolTotalStakers (Slot 13: pool => uint256)
	// If this auto-stake brings their locked state from 0 -> >0, and they have no liquid/unbonding stake,
	// then they are a brand new unique staker to the pool.
	if oldTotalLockedShares.Sign() == 0 {
		stakerLiquidKey := calculateMappingSlot(stakerAddr, params.SlotStakerPoolSharesBase)
		liquidSlot := calculateMappingSlot(poolAddr, stakerLiquidKey)
		liquidShares := state.GetState(activeStakingAddress(header, state), liquidSlot).Big()
		if liquidShares == nil {
			liquidShares = big.NewInt(0)
		}

		stakerUnbondingKey := calculateMappingSlot(stakerAddr, params.SlotStakerUnbondingBDAGBase)
		unbondingSlot := calculateMappingSlot(poolAddr, stakerUnbondingKey)
		unbondingShares := state.GetState(activeStakingAddress(header, state), unbondingSlot).Big()
		if unbondingShares == nil {
			unbondingShares = big.NewInt(0)
		}

		if liquidShares.Sign() == 0 && unbondingShares.Sign() == 0 {
			stakersCountSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalStakersBase)
			currentStakers := state.GetState(activeStakingAddress(header, state), stakersCountSlot).Big()
			if currentStakers == nil {
				currentStakers = big.NewInt(0)
			}
			newStakers := new(big.Int).Add(currentStakers, big.NewInt(1))
			state.SetState(activeStakingAddress(header, state), stakersCountSlot, common.BigToHash(newStakers))
			me.log.Debug("V2 Auto-Stake Incrementing Stakers", "newStakers", newStakers.String())
		}
	}

	// 7. Update stakerPrincipalBDAG (Slot 14: staker => pool => BDAG)
	// This tracks the raw BDAG inputs for the frontend to show distinct Yield.
	principalKey := calculateMappingSlot(stakerAddr, params.SlotStakerPrincipalBDAGBase)
	principalSlot := calculateMappingSlot(poolAddr, principalKey)
	oldPrincipal := state.GetState(activeStakingAddress(header, state), principalSlot).Big()
	if oldPrincipal == nil {
		oldPrincipal = big.NewInt(0)
	}
	newPrincipal := new(big.Int).Add(oldPrincipal, amount)
	state.SetState(activeStakingAddress(header, state), principalSlot, common.BigToHash(newPrincipal))

	// 8. Update poolTotalStake (Slot 15: pool => BDAG)
	// Tracks entire pool's base deposit size without organic yield compound
	poolStakeSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalStakeBase)
	oldPoolStake := state.GetState(activeStakingAddress(header, state), poolStakeSlot).Big()
	if oldPoolStake == nil {
		oldPoolStake = big.NewInt(0)
	}
	newPoolStake := new(big.Int).Add(oldPoolStake, amount)
	state.SetState(activeStakingAddress(header, state), poolStakeSlot, common.BigToHash(newPoolStake))

	// 9. Update poolTotalAssets (Slot 5)
	// This ensures the Vault (poolTotalAssets) increases by the deposited amount.
	// Since shares were minted above at the PRE-deposit price, the share price remains stable during deposit.
	newTotalAssets := new(big.Int).Add(poolTotalAssets, amount)
	state.SetState(activeStakingAddress(header, state), poolTotalAssetsSlot, common.BigToHash(newTotalAssets))

	me.log.Debug(fmt.Sprintf("Auto-staked %s BDAG (minted %s Shares) for %s in Epoch %d.",
		amount.String(), sharesToMint.String(), stakerAddr.Hex(), currentEpoch))
}
