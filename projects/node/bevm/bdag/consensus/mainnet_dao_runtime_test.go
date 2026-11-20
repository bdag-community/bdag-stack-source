package consensus

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

type mainnetRuntimeContext struct {
	Metadata struct {
		ABI       map[string]json.RawMessage `json:"abi"`
		Addresses map[string]common.Address  `json:"addresses"`
	} `json:"metadata"`
	GenesisHeader  *types.Header `json:"genesisHeader"`
	BaselineHeader *types.Header `json:"baselineHeader"`
	Prestate       struct {
		Legacy, Implementation, Blocklist, Selective, Governance                     common.Address
		LegacyCode, ImplementationCode, BlocklistCode, SelectiveCode, GovernanceCode hexutil.Bytes
		LegacyOwner, BlocklistOwner, SelectiveOwner                                  common.Address
		RestrictionStorage                                                           map[common.Address]map[common.Hash]common.Hash `json:"restrictionStorage"`
	} `json:"prestate"`
}

// Uses real canonical public V1 code/clock and real compiled new contracts on an
// isolated synthetic state. It is not a replay of a complete Mainnet database.
func TestMainnetDAORealRuntimesLegacyExitsFreshV2RewardsVotingAndFactoryNonce(t *testing.T) {
	c, h, _, _ := mainnetDAOTestFixture(t)
	raw, err := os.ReadFile("testdata/mainnet-dao/bundle.json")
	require.NoError(t, err)
	contextRaw, err := os.ReadFile("testdata/mainnet-dao/context.json")
	require.NoError(t, err)
	var ctx mainnetRuntimeContext
	require.NoError(t, json.Unmarshal(contextRaw, &ctx))
	b, roles, err := loadMainnetDAOBundle(raw)
	require.NoError(t, err)
	c.genesis, c.baseline = ctx.GenesisHeader, ctx.BaselineHeader
	g := c.c.BdagMainnetDAO
	g.ArtifactSHA256 = common.Hash(sha256.Sum256(raw))
	g.EarliestEVMBlock = b.BaselineNumber + 2
	h.Number = new(big.Int).SetUint64(g.EarliestEVMBlock)
	h.GasLimit = 45_000_000
	h.BaseFee = big.NewInt(0)
	h.Difficulty = big.NewInt(1)
	c.parent.Number = new(big.Int).Sub(h.Number, big.NewInt(1))
	h.ParentHash = c.parent.Hash()
	directory := t.TempDir()
	storage, err := rawdb.Open(rawdb.OpenOptions{Type: "leveldb", Directory: directory, Cache: 16, Handles: 16, SkipBackgroundFreeze: true})
	require.NoError(t, err)
	trie := triedb.NewDatabase(storage, nil)
	database := state.NewDatabase(trie, nil)
	t.Cleanup(func() { trie.Close(); storage.Close() })
	s, err := state.New(types.EmptyRootHash, database)
	require.NoError(t, err)
	for _, row := range []struct {
		a    common.Address
		code []byte
	}{{ctx.Prestate.Legacy, ctx.Prestate.LegacyCode}, {ctx.Prestate.Implementation, ctx.Prestate.ImplementationCode},
		{ctx.Prestate.Blocklist, ctx.Prestate.BlocklistCode}, {ctx.Prestate.Selective, ctx.Prestate.SelectiveCode}, {ctx.Prestate.Governance, ctx.Prestate.GovernanceCode}} {
		s.SetCode(row.a, row.code)
		s.SetNonce(row.a, 1)
	}
	for _, row := range []struct{ a, owner common.Address }{{ctx.Prestate.Legacy, ctx.Prestate.LegacyOwner}, {ctx.Prestate.Blocklist, ctx.Prestate.BlocklistOwner}, {ctx.Prestate.Selective, ctx.Prestate.SelectiveOwner}} {
		s.SetState(row.a, common.Hash{}, common.BytesToHash(row.owner.Bytes()))
	}
	s.SetState(mparams.StakingContractAddr, erc1967ImplementationSlot, common.BytesToHash(ctx.Prestate.Implementation.Bytes()))
	for slot, value := range map[common.Hash]common.Hash{mparams.SlotGenesisTime: common.BigToHash(new(big.Int).SetUint64(g.LegacyGenesisTime)), mparams.SlotMinerSplit: common.BigToHash(big.NewInt(70)),
		mparams.SlotStakerSplit: common.BigToHash(big.NewInt(30)), mparams.SlotInitializableStorage: common.BigToHash(big.NewInt(1)), mparams.SlotReentrancyGuardState: common.BigToHash(big.NewInt(1))} {
		s.SetState(mparams.StakingContractAddr, slot, value)
	}
	s.SetNonce(mparams.ChainTallyAddr, 1)
	require.Len(t, ctx.Prestate.RestrictionStorage, 2)
	for address, rows := range ctx.Prestate.RestrictionStorage {
		for slot, value := range rows {
			s.SetState(address, slot, value)
		}
	}
	abis := make(map[string]abi.ABI)
	for role, rawABI := range ctx.Metadata.ABI {
		parsed, err := abi.JSON(strings.NewReader(string(rawABI)))
		require.NoError(t, err)
		abis[role] = parsed
	}
	alice, pool := common.HexToAddress("0x12345"), common.HexToAddress("0x98765")
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	s.SetBalance(alice, uint256.MustFromBig(new(big.Int).Mul(big.NewInt(100), unit)), tracing.BalanceChangeUnspecified)
	call := func(target, from common.Address, contract, method string, value *big.Int, args ...interface{}) ([]byte, error) {
		data, err := abis[contract].Pack(method, args...)
		require.NoError(t, err)
		block := vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: h.Number, Time: h.Time, GasLimit: 45_000_000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(0)}
		evm := vm.NewEVM(block, vm.TxContext{Origin: from}, s, c.c, vm.Config{NoBaseFee: true})
		out, _, err := evm.Call(vm.AccountRef(from), target, data, 30_000_000, uint256.MustFromBig(value))
		return out, err
	}
	view := func(target common.Address, contract, method string, args ...interface{}) []interface{} {
		out, err := call(target, alice, contract, method, big.NewInt(0), args...)
		require.NoError(t, err, "view %s", method)
		values, err := abis[contract].Unpack(method, out)
		require.NoError(t, err)
		return values
	}
	activationTime := h.Time
	h.Time = activationTime - 10*604800
	_, err = call(mparams.StakingContractAddr, alice, "legacyImplementation", "stake", new(big.Int).Mul(big.NewInt(10), unit), alice, new(big.Int).Mul(big.NewInt(10), unit), pool)
	require.NoError(t, err)
	first := view(mparams.StakingContractAddr, "legacyImplementation", "userFirstDepositTimestamp", alice)[0].(*big.Int)
	h.Time = activationTime - 604800
	_, err = call(mparams.StakingContractAddr, alice, "legacyImplementation", "unstake", big.NewInt(0), alice, unit, []common.Address{pool})
	require.NoError(t, err)
	oldRequests := view(mparams.StakingContractAddr, "legacyImplementation", "getUnbondingRequests", alice)[0]
	oldRelease := reflect.ValueOf(oldRequests).Index(0).FieldByName("ReleaseTime").Interface().(*big.Int).Uint64()
	db := rawdb.NewMemoryDatabase()
	t.Cleanup(func() { db.Close() })
	engine := New(db)
	h.Coinbase = pool
	h.Extra = EncodeRewardExtraWithLayer(100000000, g.SignalStartLayer+99999+g.ActivationDelayLayers)
	engine.Finalize(c, h, s, &types.Body{}) // genuine pre-cutover operator auto-stake
	oldOperatorLocked := view(mparams.StakingContractAddr, "legacyImplementation", "stakerTotalLockedShares", pool, pool)[0].(*big.Int)
	require.Positive(t, oldOperatorLocked.Sign())
	h.Time = activationTime
	before := s.Copy()
	before.IntermediateRoot(false)
	oldArtifact, oldProfile := mainnetDAOArtifactJSON, mparams.BdagMainnetDAO
	mainnetDAOArtifactJSON, mparams.BdagMainnetDAO = raw, g
	t.Cleanup(func() { mainnetDAOArtifactJSON, mparams.BdagMainnetDAO = oldArtifact, oldProfile })
	require.NoError(t, ValidateCompiledMainnetDAOArtifacts())
	require.NoError(t, ApplyMainnetDAOFork(c, h, s))
	replay := before.Copy()
	require.NoError(t, ApplyMainnetDAOFork(c, h, replay))
	require.Equal(t, s.Copy().IntermediateRoot(false), replay.IntermediateRoot(false))
	// Replacing just the two authorized Legacy words must recover the exact
	// complete pre-migration storage trie, including undiscovered users/mappings.
	control := s.Copy()
	control.SetState(mparams.StakingContractAddr, common.Hash{}, before.GetState(mparams.StakingContractAddr, common.Hash{}))
	control.SetState(mparams.StakingContractAddr, erc1967ImplementationSlot, before.GetState(mparams.StakingContractAddr, erc1967ImplementationSlot))
	for _, address := range []common.Address{mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr} {
		control.SetState(address, common.Hash{}, before.GetState(address, common.Hash{}))
	}
	control.IntermediateRoot(false)
	require.Equal(t, before.GetStorageRoot(mparams.StakingContractAddr), control.GetStorageRoot(mparams.StakingContractAddr))
	for _, address := range []common.Address{mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr} {
		require.Equal(t, before.GetStorageRoot(address), control.GetStorageRoot(address))
	}
	for address, rows := range ctx.Prestate.RestrictionStorage {
		for slot, value := range rows {
			require.Equal(t, value, s.GetState(address, slot))
		}
	}
	require.Equal(t, before.GetBalance(mparams.StakingContractAddr), s.GetBalance(mparams.StakingContractAddr))
	require.Equal(t, oldRequests, view(mparams.StakingContractAddr, "legacyImplementation", "getUnbondingRequests", alice)[0])
	require.Equal(t, first, view(mparams.StakingContractAddr, "legacyImplementation", "userFirstDepositTimestamp", alice)[0])
	require.EqualValues(t, 2, s.GetNonce(roles["factory"].Address), "preserve constructor-created escrow nonce")
	require.Equal(t, roles["stakingTimelock"].Address, view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "owner")[0])
	_, err = call(mparams.StakingContractAddr, ctx.Prestate.LegacyOwner, "legacyImplementation", "pause", big.NewInt(0))
	require.Error(t, err)
	_, err = call(mparams.SeparateStakingV2Addr, alice, "stakingV2Implementation", "stake", new(big.Int).Mul(big.NewInt(2), unit), alice, new(big.Int).Mul(big.NewInt(2), unit), pool)
	require.NoError(t, err)
	bondEnd := view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "bondingEndsAt", alice)[0].(*big.Int).Uint64()
	require.Equal(t, activationTime+4*604800, bondEnd)
	h.Coinbase = pool
	h.Extra = EncodeRewardExtraWithLayer(100000000, g.SignalStartLayer+99999+g.ActivationDelayLayers)
	legacyBacking := new(uint256.Int).Set(s.GetBalance(mparams.StakingContractAddr))
	engine.Finalize(c, h, s, &types.Body{})
	require.Equal(t, legacyBacking, s.GetBalance(mparams.StakingContractAddr))
	require.Equal(t, activationTime, view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "userFirstDepositTimestamp", pool)[0].(*big.Int).Uint64(), "new native-only operators have a real first-credit timestamp")
	for _, role := range []string{"miningCredential", "stakingCredential"} {
		require.Equal(t, false, view(roles[role].Address, role, "votingReady")[0])
	}
	for i := uint64(1); i <= 2; i++ {
		h.Number = new(big.Int).SetUint64(g.EarliestEVMBlock + i)
		h.Time = activationTime + i*604800
		require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
		engine.Finalize(c, h, s, &types.Body{})
	}
	for _, role := range []string{"miningCredential", "stakingCredential"} {
		require.Equal(t, true, view(roles[role].Address, role, "votingReady")[0])
		require.Positive(t, view(roles[role].Address, role, "totalSupply")[0].(*big.Int).Sign())
	}
	_, err = call(mparams.SeparateStakingV2Addr, alice, "stakingV2Implementation", "unstake", big.NewInt(0), alice, unit, []common.Address{pool})
	require.NoError(t, err)
	requests := view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "getUnbondingRequests", alice)[0]
	newRelease := reflect.ValueOf(requests).Index(0).FieldByName("ReleaseTime").Interface().(*big.Int).Uint64()
	h.Time = oldRelease
	require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
	oldBalance := new(uint256.Int).Set(s.GetBalance(alice))
	_, err = call(mparams.StakingContractAddr, alice, "legacyImplementation", "claimStake", big.NewInt(0), alice, pool)
	require.NoError(t, err)
	require.Equal(t, unit, new(big.Int).Sub(s.GetBalance(alice).ToBig(), oldBalance.ToBig()))
	// Operator auto-stake has its original 52-epoch lock, distinct from a
	// staker's three-epoch unbonding. Migration must not release it early.
	out, err := call(mparams.StakingContractAddr, pool, "legacyImplementation", "claimStake", big.NewInt(0), pool, pool)
	require.Error(t, err)
	require.Equal(t, abis["legacyImplementation"].Errors["NothingToClaim"].ID.Bytes()[:4], out)
	require.Equal(t, oldOperatorLocked, view(mparams.StakingContractAddr, "legacyImplementation", "stakerTotalLockedShares", pool, pool)[0])
	if h.Time < bondEnd {
		_, err = call(mparams.SeparateStakingV2Addr, alice, "stakingV2Implementation", "claimStake", big.NewInt(0), alice, pool)
		require.Error(t, err)
	}
	h.Time = max(newRelease, bondEnd)
	require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
	oldBalance = new(uint256.Int).Set(s.GetBalance(alice))
	_, err = call(mparams.SeparateStakingV2Addr, alice, "stakingV2Implementation", "claimStake", big.NewInt(0), alice, pool)
	require.NoError(t, err)
	require.Positive(t, new(big.Int).Sub(s.GetBalance(alice).ToBig(), oldBalance.ToBig()).Sign())

	// Use the native-installed Governor, real earned credentials and both owner
	// timelocks. No impersonated timelock/multisig or direct authority grant.
	advanceWorkEpoch := func() {
		current := s.GetState(mparams.ChainTallyAddr, mparams.SlotRewardVotingEpoch).Big().Uint64()
		h.Time = g.LegacyGenesisTime + (current+1)*604800
		h.Number = new(big.Int).Add(h.Number, big.NewInt(1))
		require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
		engine.Finalize(c, h, s, &types.Body{})
	}
	engine.Finalize(c, h, s, &types.Body{})
	advanceWorkEpoch()
	developer := common.HexToAddress("0xf00d")
	evidence := crypto.Keccak256Hash([]byte("isolated reviewed developer"))
	action, err := abis["developers"].Pack("setApproval", developer, true, evidence)
	require.NoError(t, err)
	targets, values, data := []common.Address{roles["developers"].Address}, []*big.Int{big.NewInt(0)}, [][]byte{action}
	description := "isolated Mainnet migration governance proof"
	_, err = call(roles["developers"].Address, pool, "developers", "setApproval", big.NewInt(0), developer, true, evidence)
	require.Error(t, err)
	bond := new(big.Int).Div(new(big.Int).Set(unit), big.NewInt(1000))
	out, err = call(roles["governor"].Address, pool, "governor", "proposeWithDeposit", bond, targets, values, data, description)
	require.NoError(t, err)
	proposal, err := abis["governor"].Unpack("proposeWithDeposit", out)
	require.NoError(t, err)
	id := proposal[0].(*big.Int)
	advanceWorkEpoch()
	u8, err := abi.NewType("uint8", "", nil)
	require.NoError(t, err)
	bytesType, err := abi.NewType("bytes", "", nil)
	require.NoError(t, err)
	ownersType, err := abi.NewType("address[]", "", nil)
	require.NoError(t, err)
	poolsType, err := abi.NewType("address[][]", "", nil)
	require.NoError(t, err)
	miningParams, err := (abi.Arguments{{Type: u8}, {Type: bytesType}}).Pack(uint8(1), []byte{})
	require.NoError(t, err)
	inner, err := (abi.Arguments{{Type: ownersType}, {Type: poolsType}}).Pack([]common.Address{alice}, [][]common.Address{{pool}})
	require.NoError(t, err)
	stakingParams, err := (abi.Arguments{{Type: u8}, {Type: bytesType}}).Pack(uint8(2), inner)
	require.NoError(t, err)
	_, err = call(roles["governor"].Address, pool, "governor", "castVoteWithReasonAndParams", big.NewInt(0), id, uint8(1), "earned Mining support", miningParams)
	require.NoError(t, err)
	_, err = call(roles["governor"].Address, alice, "governor", "castVoteWithReasonAndParams", big.NewInt(0), id, uint8(1), "earned Staking support", stakingParams)
	require.NoError(t, err)
	advanceWorkEpoch()
	descriptionHash := crypto.Keccak256Hash([]byte(description))
	_, err = call(roles["governor"].Address, pool, "governor", "queue", big.NewInt(0), targets, values, data, descriptionHash)
	require.NoError(t, err)
	eta := view(roles["governor"].Address, "governor", "proposalEta", id)[0].(*big.Int).Uint64()
	require.Equal(t, h.Time+604800, eta, "critical action waits one real seven-day epoch")
	h.Time = eta - 1
	require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
	_, err = call(roles["governor"].Address, pool, "governor", "execute", big.NewInt(0), targets, values, data, descriptionHash)
	require.Error(t, err)
	h.Time = eta
	require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
	_, err = call(roles["governor"].Address, pool, "governor", "execute", big.NewInt(0), targets, values, data, descriptionHash)
	require.NoError(t, err)
	require.Equal(t, true, view(roles["developers"].Address, "developers", "isApproved", developer)[0])
	_, err = call(roles["governor"].Address, alice, "governor", "withdrawProposalBond", big.NewInt(0), id)
	require.Error(t, err)
	poolBefore := new(uint256.Int).Set(s.GetBalance(pool))
	_, err = call(roles["governor"].Address, pool, "governor", "withdrawProposalBond", big.NewInt(0), id)
	require.NoError(t, err)
	require.Equal(t, bond, new(big.Int).Sub(s.GetBalance(pool).ToBig(), poolBefore.ToBig()))
	_, err = call(roles["governor"].Address, pool, "governor", "withdrawProposalBond", big.NewInt(0), id)
	require.Error(t, err)
	oldOperatorEpoch := (activationTime - 604800 - g.LegacyGenesisTime) / 604800
	h.Time = g.LegacyGenesisTime + (oldOperatorEpoch+52)*604800
	require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
	poolLegacyBefore := new(uint256.Int).Set(s.GetBalance(pool))
	_, err = call(mparams.StakingContractAddr, pool, "legacyImplementation", "claimStake", big.NewInt(0), pool, pool)
	require.NoError(t, err)
	require.Positive(t, new(big.Int).Sub(s.GetBalance(pool).ToBig(), poolLegacyBefore.ToBig()).Sign())
	require.Zero(t, view(mparams.StakingContractAddr, "legacyImplementation", "stakerTotalLockedShares", pool, pool)[0].(*big.Int).Sign())
	// Fresh V2's operator rewards keep their own first-credit date and 52-epoch
	// maturity, with no need to make a personal deposit merely to enable claims.
	h.Time = activationTime + 52*604800
	require.NoError(t, prepareVersionedRewardClock(c.c, h, s))
	engine.Finalize(c, h, s, &types.Body{})
	legacyAfterClaim := new(uint256.Int).Set(s.GetBalance(mparams.StakingContractAddr))
	freshLocked := new(big.Int).Set(view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "stakerTotalLockedShares", pool, pool)[0].(*big.Int))
	poolBefore = new(uint256.Int).Set(s.GetBalance(pool))
	_, err = call(mparams.SeparateStakingV2Addr, pool, "stakingV2Implementation", "claimStake", big.NewInt(0), pool, pool)
	require.NoError(t, err)
	require.Positive(t, new(big.Int).Sub(s.GetBalance(pool).ToBig(), poolBefore.ToBig()).Sign())
	require.Negative(t, new(big.Int).Sub(view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "stakerTotalLockedShares", pool, pool)[0].(*big.Int), freshLocked).Sign())
	require.Equal(t, legacyAfterClaim, s.GetBalance(mparams.StakingContractAddr))
	require.Equal(t, activationTime, view(mparams.SeparateStakingV2Addr, "stakingV2Implementation", "userFirstDepositTimestamp", pool)[0].(*big.Int).Uint64())

	// Commit and close the actual disk-backed synthetic EVM database, then open
	// a new DB/trie/state instance. This is not a complete native/Mainnet replay.
	rawdb.WriteChainConfig(storage, c.genesis.Hash(), c.c)
	root, err := s.Commit(h.Number.Uint64(), false)
	require.NoError(t, err)
	require.NoError(t, trie.Commit(root, false))
	require.NoError(t, trie.Close())
	require.NoError(t, storage.Close())
	storage, err = rawdb.Open(rawdb.OpenOptions{Type: "leveldb", Directory: directory, Cache: 16, Handles: 16, SkipBackgroundFreeze: true})
	require.NoError(t, err)
	trie = triedb.NewDatabase(storage, nil)
	database = state.NewDatabase(trie, nil)
	s, err = state.New(root, database)
	require.NoError(t, err)
	storedConfig := rawdb.ReadChainConfig(storage, c.genesis.Hash())
	require.NotNil(t, storedConfig)
	require.Equal(t, g, storedConfig.BdagMainnetDAO)
	c.c = storedConfig
	require.NoError(t, ApplyMainnetDAOFork(c, h, s))
	require.Equal(t, root, s.IntermediateRoot(false))
	require.Equal(t, true, view(roles["developers"].Address, "developers", "isApproved", developer)[0])
	require.EqualValues(t, 2, s.GetNonce(roles["factory"].Address))
}
