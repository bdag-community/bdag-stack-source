package consensus

import (
	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/stretchr/testify/require"
	"math/big"
	"os"
	"testing"
)

// Explicit opt-in only. Supply a locally authenticated, isolated snapshot copy;
// never a live database. Both disk layers are opened read-only and no Commit is
// called. Synthetic future gate/header inputs test migration of REAL old state,
// not proof that a live signalling window or notice period has elapsed.
func TestSeparateStakingAuthenticatedSnapshotPreflight(t *testing.T) {
	directory := os.Getenv("BDAG_SEPARATE_SNAPSHOT_CHAINDATA")
	if directory == "" {
		t.Skip("authenticated isolated snapshot path not supplied")
	}
	db, err := rawdb.Open(rawdb.OpenOptions{Directory: directory, Cache: 128, Handles: 64, ReadOnly: true, SkipBackgroundFreeze: true})
	require.NoError(t, err)
	defer db.Close()
	headHash := rawdb.ReadHeadBlockHash(db)
	require.NotEqual(t, common.Hash{}, headHash)
	number := rawdb.ReadHeaderNumber(db, headHash)
	require.NotNil(t, number)
	head := rawdb.ReadHeader(db, headHash, *number)
	require.NotNil(t, head)
	trie := triedb.NewDatabase(db, &triedb.Config{PathDB: pathdb.ReadOnly})
	defer trie.Close()
	s, err := state.New(head.Root, state.NewDatabase(trie, nil))
	require.NoError(t, err)
	originalNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.TestNetParam
	t.Cleanup(func() { qparams.ActiveNetParams = originalNet })
	clock, err := rewardEpochScheduleFromState(s)
	require.NoError(t, err)
	previous := s.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big()
	require.True(t, previous.IsUint64())
	bounds, err := clock.Bounds(previous.Uint64())
	require.NoError(t, err)
	config := *params.BdagTestnetChainConfig
	config.BdagSeparateStakingBlock = new(big.Int).SetUint64(*number + 1)
	config.BdagSeparateStaking = &ethparams.BDAGSeparateStakingForkConfig{Version: 1, ArtifactSHA256: separateStakingArtifactSHA256, SignalStartLayer: 700000, ActivationLayer: 703999, ReleaseDistributedAt: head.Time - 86400, NoticePublishedAt: head.Time - 86400, NotBeforeTimestamp: head.Time}
	oldDefs := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{upgrades.SeparateStakingV1Name: {Name: upgrades.SeparateStakingV1Name, BitMask: upgrades.SeparateStakingV1BitMask, StartLayer: 700000, TimeoutLayer: 702000, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000, LockedIn: true, AnchorSet: true, ActivateAnchor: 703999}})
	defer upgrades.ReplaceUpgrades(oldDefs)
	h := &types.Header{Number: config.BdagSeparateStakingBlock, Time: bounds.End, Extra: EncodeRewardExtraWithLayer(1, 703999)}
	oldBalance := s.GetBalance(params.StakingContractAddr).ToBig()
	oldStorage := s.GetStorageRoot(params.StakingContractAddr)
	oldCode := s.GetCodeHash(params.StakingContractAddr)
	builder, importer := s.Copy(), s.Copy()
	require.NoError(t, ApplySeparateStakingFork(&config, h, builder))
	require.NoError(t, ApplySeparateStakingFork(&config, h, importer))
	require.Equal(t, builder.IntermediateRoot(false), importer.IntermediateRoot(false))
	require.Equal(t, oldStorage, builder.GetStorageRoot(params.StakingContractAddr))
	require.Equal(t, oldCode, builder.GetCodeHash(params.StakingContractAddr))
	require.Equal(t, oldBalance, builder.GetBalance(params.StakingContractAddr).ToBig())
	require.Zero(t, builder.GetBalance(params.SeparateStakingV2Addr).Sign())
	require.Equal(t, common.Hash{}, s.GetState(params.ChainTallyAddr, separateStakingAppliedSlot))
	t.Logf("snapshot block=%d hash=%s legacy-storage=%s old-balance=%s synthetic-new-root=%s", *number, headHash, oldStorage, oldBalance, builder.IntermediateRoot(false))
}
