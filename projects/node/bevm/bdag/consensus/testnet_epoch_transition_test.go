package consensus

import (
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/stretchr/testify/require"
)

func TestTestnetPreActivationMatchesRC2FinancialState(t *testing.T) {
	previous := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	actual := qparams.TestNetParams
	legacy := actual
	// RC2 leaves this setting unset and indexes auto-stakes by block height.
	legacy.StakingEpochDurationSeconds = 0
	var roots [2]common.Hash
	for i, net := range []*qparams.Params{&legacy, &actual} {
		active := qparams.TestNetParam
		active.Params = net
		qparams.ActiveNetParams = &active
		db := rawdb.NewMemoryDatabase()
		tdb := triedb.NewDatabase(db, nil)
		t.Cleanup(func() { tdb.Close() })
		s, err := state.New(common.Hash{}, state.NewDatabase(tdb, nil))
		require.NoError(t, err)
		engine := New(db)
		config := *params.BdagTestnetChainConfig
		config.BdagGovernanceForkBlock = big.NewInt(100_000_000)
		chain := finalizeTestChainReader{config: &config}
		genesis := uint64(net.GenesisBlock.Block().Header.Timestamp.Unix())
		for _, n := range []int64{1, 103, 20_000, net.SubsidyReductionInterval, net.SubsidyReductionInterval + 1} {
			h := &types.Header{Number: big.NewInt(n), Time: genesis + uint64(n)*60,
				Coinbase: common.HexToAddress("0x1234"), Extra: EncodeRewardExtra(1000)}
			engine.Finalize(chain, h, s, &types.Body{})
			require.Zero(t, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus).Big().Sign())
			roots[i] = h.Root
		}
	}
	require.Equal(t, roots[0], roots[1], "pre-activation candidate must preserve RC2 financial state")
}

func TestRewardLedgersDoNotReopenClosedEpochs(t *testing.T) {
	previous := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	qparams.ActiveNetParams = &qparams.DevNetParam
	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	t.Cleanup(func() { tdb.Close() })
	s, err := state.New(common.Hash{}, state.NewDatabase(tdb, nil))
	require.NoError(t, err)
	engine := New(db)
	pool := common.HexToAddress("0x1234")
	s.SetState(params.StakingContractAddr, calculateMappingSlot(pool, params.SlotPoolTotalSharesBase), common.BigToHash(big.NewInt(100)))
	genesis := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	for i, epoch := range []uint64{3, 4, 5, 4} {
		h := &types.Header{Number: big.NewInt(int64(i + 1)), Time: genesis + epoch*60, Coinbase: pool}
		// Finalize credits the staking ledger before the mining ledger.
		engine.updateStakingRewardVotingLedger(h, s, big.NewInt(270))
		engine.updateRewardVotingLedger(h, s, big.NewInt(730))
	}
	require.True(t, rewardVotingEpochFinalized(s, 4))
	require.Equal(t, int64(730), rewardVotingTotal(s, 4).Int64(), "closed mining epoch must be immutable")
	require.Equal(t, int64(270_000_000), stakingEpochVotingTotal(s, 4).Int64(), "closed staking epoch must be immutable")
	require.Equal(t, int64(1460), rewardVotingTotal(s, 5).Int64())
	require.Equal(t, int64(540_000_000), stakingEpochVotingTotal(s, 5).Int64())
}

func TestTestnetFastRewardsRequireBootstrapAndCompleteEpoch(t *testing.T) {
	previous := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	qparams.ActiveNetParams = &qparams.TestNetParam
	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	t.Cleanup(func() { tdb.Close() })
	s, err := state.New(common.Hash{}, state.NewDatabase(tdb, nil))
	require.NoError(t, err)
	engine := New(db)
	config := *params.BdagTestnetChainConfig
	config.BdagGovernanceForkBlock = big.NewInt(100)
	config.BdagGovernanceFork = params.TestnetGovernanceArtifactTemplate()
	chain := finalizeTestChainReader{config: &config}
	genesis := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	pool := common.HexToAddress("0x1234")
	finalize := func(number int64, seconds uint64) {
		h := &types.Header{Number: big.NewInt(number), Time: genesis + seconds, Coinbase: pool, Extra: EncodeRewardExtra(1000)}
		engine.Finalize(chain, h, s, &types.Body{})
	}
	finalize(99, 299)
	finalize(100, 300) // Earliest EVM height alone must not enable votes.
	require.Zero(t, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus).Big().Sign())
	s.SetState(params.ChainTallyAddr, governanceAppliedSlot, common.BigToHash(big.NewInt(101)))
	finalize(101, 301) // Partial epoch 5, not eligible.
	require.EqualValues(t, 5, s.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64())
	finalize(102, 360)
	require.False(t, rewardVotingEpochFinalized(s, 5))
	finalize(103, 420)
	require.True(t, rewardVotingEpochFinalized(s, 6))
	require.Positive(t, rewardVotingTotal(s, 6).Sign())
	require.Positive(t, stakingEpochVotingTotal(s, 6).Sign())
	require.Zero(t, s.GetState(params.ChainTallyAddr, params.SlotCurrentEpoch).Big().Sign(), "legacy financial clock must remain RC2-compatible")
	legacyAutoStake := calculateAddressUintMappingSlot(pool, 0, params.SlotPoolEpochAutoStakeBase)
	require.Positive(t, s.GetState(params.StakingContractAddr, legacyAutoStake).Big().Sign())
	fastAutoStake := calculateAddressUintMappingSlot(pool, 7, params.SlotPoolEpochAutoStakeBase)
	require.Zero(t, s.GetState(params.StakingContractAddr, fastAutoStake).Big().Sign(), "operator shares must not be indexed by fast reward epoch")
}
