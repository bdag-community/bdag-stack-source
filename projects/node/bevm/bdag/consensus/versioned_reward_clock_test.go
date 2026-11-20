package consensus

import (
	"math"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/stretchr/testify/require"
)

// The release migration must install this marker only after all native and
// old-policy authorization gates pass. Setting it here simulates that state,
// not an approved deployment or an alternate activation path.
var clockFixtureMarker = crypto.Keccak256Hash([]byte("bdag.testnet.timing.v1.activation"))

func versionedClockFixture(t *testing.T) (*state.StateDB, *bdagEngine, finalizeTestChainReader, uint64) {
	t.Helper()
	previous := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	qparams.ActiveNetParams = &qparams.TestNetParam
	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	t.Cleanup(func() { tdb.Close(); db.Close() })
	s, err := state.New(common.Hash{}, state.NewDatabase(tdb, nil))
	require.NoError(t, err)
	// Native tally is nonce 1 on the real chain. Otherwise IntermediateRoot's
	// EIP-161 pruning would erase a code/balance/nonce-empty fixture account.
	s.SetNonce(params.ChainTallyAddr, 1)
	config := *params.BdagTestnetChainConfig
	config.BdagGovernanceForkBlock = big.NewInt(1)
	s.SetState(params.ChainTallyAddr, governanceAppliedSlot, common.BigToHash(big.NewInt(1)))
	s.SetState(params.ChainTallyAddr, clockFixtureMarker, common.BigToHash(big.NewInt(2)))
	s.SetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch, common.BigToHash(big.NewInt(8)))
	s.SetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus, common.BigToHash(big.NewInt(6)))
	s.SetState(params.ChainTallyAddr, params.SlotRewardVotingFirstEpochPlus, common.BigToHash(big.NewInt(7)))
	genesis := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	s.SetState(params.ChainTallyAddr, params.SlotRewardEpochScheduleLength, common.BigToHash(big.NewInt(2)))
	for i, value := range []uint64{0, genesis, 60, 10, genesis + 600, 1800} {
		s.SetState(params.ChainTallyAddr, scheduleWordSlot(int64(i)), common.BigToHash(new(big.Int).SetUint64(value)))
	}
	return s, New(db), finalizeTestChainReader{config: &config}, genesis
}

func TestVersionedClockRejectsInvalidStateWithoutMutation(t *testing.T) {
	for _, test := range []string{"no schedule", "bad origin", "discontinuous", "huge schedule", "overflow epoch", "overflow timestamp floor", "missing bootstrap", "bootstrap in future", "future first epoch", "wrong chain", "wrong native network", "future marker", "missing V3", "missing V3 config", "timestamp overflow"} {
		t.Run(test, func(t *testing.T) {
			s, _, chain, genesis := versionedClockFixture(t)
			h := &types.Header{Number: big.NewInt(3), Time: genesis + 600}
			slot, value := common.Hash{}, common.Hash{}
			switch test {
			case "no schedule":
				slot = params.SlotRewardEpochScheduleLength
			case "bad origin":
				slot, value = scheduleWordSlot(1), common.BigToHash(big.NewInt(1))
			case "discontinuous":
				slot, value = scheduleWordSlot(4), common.BigToHash(new(big.Int).SetUint64(genesis+601))
			case "huge schedule":
				slot, value = params.SlotRewardEpochScheduleLength, common.BigToHash(big.NewInt(65))
			case "overflow epoch":
				slot, value = params.SlotRewardVotingEpoch, common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 64))
			case "overflow timestamp floor":
				slot, value = params.SlotGovernanceTimeFloor, common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 64))
			case "missing bootstrap":
				slot = params.SlotRewardVotingBootstrapEpochPlus
			case "bootstrap in future":
				slot, value = params.SlotRewardVotingBootstrapEpochPlus, common.BigToHash(big.NewInt(100))
			case "future first epoch":
				slot, value = params.SlotRewardVotingFirstEpochPlus, common.BigToHash(big.NewInt(100))
			case "wrong chain":
				chain.config.ChainID = big.NewInt(1404)
			case "wrong native network":
				qparams.ActiveNetParams = &qparams.MainNetParam
			case "future marker":
				slot, value = clockFixtureMarker, common.BigToHash(big.NewInt(100))
			case "missing V3":
				slot = governanceAppliedSlot
			case "missing V3 config":
				chain.config.BdagGovernanceFork = nil
			case "timestamp overflow":
				h.Time = math.MaxUint64
			}
			if slot != (common.Hash{}) {
				s.SetState(params.ChainTallyAddr, slot, value)
			}
			before := s.IntermediateRoot(true)
			require.Error(t, prepareVersionedRewardClock(chain.config, h, s))
			require.Equal(t, before, s.IntermediateRoot(true), "invalid clock must fail before any write")
		})
	}
}

func TestVersionedClockInactiveStateIsUntouched(t *testing.T) {
	s, _, chain, genesis := versionedClockFixture(t)
	s.SetState(params.ChainTallyAddr, clockFixtureMarker, common.Hash{})
	s.SetState(params.ChainTallyAddr, params.SlotRewardEpochScheduleLength, common.BigToHash(big.NewInt(1_000_000)))
	before := s.IntermediateRoot(true)
	require.NoError(t, prepareVersionedRewardClock(chain.config, &types.Header{Number: big.NewInt(3), Time: genesis + 2400}, s))
	require.Equal(t, before, s.IntermediateRoot(true), "unactivated clock must not rewrite historical state")
	require.EqualValues(t, 40, rewardVotingEpochForHeader(&types.Header{Number: big.NewInt(3), Time: genesis + 2400}, s))
}

func TestVersionedClockSnapshotRewindAndRepeat(t *testing.T) {
	s, _, chain, genesis := versionedClockFixture(t)
	h := &types.Header{Number: big.NewInt(3), Time: genesis + 2400}
	before := s.Copy()
	require.NoError(t, prepareVersionedRewardClock(chain.config, h, s))
	root := s.IntermediateRoot(true)
	require.NoError(t, prepareVersionedRewardClock(chain.config, h, s))
	require.Equal(t, root, s.IntermediateRoot(true), "repeated builder hook must be idempotent")
	require.NoError(t, prepareVersionedRewardClock(chain.config, h, before))
	require.Equal(t, root, before.IntermediateRoot(true), "fresh parent-state replay must match")
	parent := s.Copy()
	snapshot := s.Snapshot()
	h.Time = genesis + 10_000
	require.NoError(t, prepareVersionedRewardClock(chain.config, h, s))
	s.RevertToSnapshot(snapshot)
	require.Equal(t, parent.IntermediateRoot(true), s.IntermediateRoot(true), "clock must rewind with candidate state")
}

func TestVersionedClockNativeLedgerTransition(t *testing.T) {
	s, engine, chain, genesis := versionedClockFixture(t)
	pool := common.HexToAddress("0x1234")
	for i, sample := range []struct{ seconds, epoch, timeFloor uint64 }{
		{599, 9, 599}, {600, 10, 600}, {2399, 10, 2399},
		{2400, 11, 2400}, {2000, 11, 2400},
	} {
		h := &types.Header{Number: big.NewInt(int64(i + 3)), Time: genesis + sample.seconds,
			Coinbase: pool, Extra: EncodeRewardExtra(1000)}
		ApplyPreBlockStateChanges(engine, chain, h, s)
		require.EqualValues(t, sample.epoch, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64(), "contract transactions must see the new epoch before reward finalization")
		require.EqualValues(t, genesis+sample.timeFloor, s.GetState(params.ChainTallyAddr, common.BigToHash(big.NewInt(9))).Big().Uint64())
		engine.Finalize(chain, h, s, &types.Body{})
		require.EqualValues(t, sample.epoch, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64())
	}
	miningPerBlock := new(big.Int).Mul(big.NewInt(730), rewardExtraWeiMultiplier)
	require.Equal(t, miningPerBlock, rewardVotingTotal(s, 9))
	require.Equal(t, new(big.Int).Mul(miningPerBlock, big.NewInt(2)), rewardVotingTotal(s, 10))
	require.Equal(t, new(big.Int).Mul(miningPerBlock, big.NewInt(2)), rewardVotingTotal(s, 11))
	// Native subsidy units convert to wei. Pool share-price rounding must carry
	// its residual, not invent or discard staking voting units at rollover.
	units := new(big.Int)
	for _, epoch := range []uint64{9, 10, 11} {
		total := stakingEpochVotingTotal(s, epoch)
		require.Positive(t, total.Sign())
		units.Add(units, total)
	}
	units.Add(units, s.GetState(params.StakingContractAddr, calculateMappingSlot(pool, params.SlotPoolRewardRemainderBase)).Big())
	wantUnits := new(big.Int).Mul(big.NewInt(5*270*stakingRewardAccumulatorScale), rewardExtraWeiMultiplier)
	require.Equal(t, wantUnits, units)
	require.True(t, rewardVotingEpochFinalized(s, 10))
	require.EqualValues(t, 6, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus).Big().Uint64(), "do not rebootstrap eligibility")
	require.EqualValues(t, 7, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingFirstEpochPlus).Big().Uint64())
	require.Zero(t, s.GetState(params.ChainTallyAddr, params.SlotCurrentEpoch).Big().Sign(), "financial/operator epoch must not accelerate")
	require.Positive(t, s.GetState(params.StakingContractAddr, calculateAddressUintMappingSlot(pool, 0, params.SlotPoolEpochAutoStakeBase)).Big().Sign())
	require.Zero(t, s.GetState(params.StakingContractAddr, calculateAddressUintMappingSlot(pool, 11, params.SlotPoolEpochAutoStakeBase)).Big().Sign())
}
