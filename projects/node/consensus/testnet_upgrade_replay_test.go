package consensus

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	nativeraw "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/stretchr/testify/require"
)

// Reuse archived proof-of-work block bodies, not archived post-fork state.
// The historical schedule is a test fixture only. Both runs rebuild a new
// candidate history from genesis, deriving the signal anchor normally, with
// the current revision-7 bootstrap. This does not publish a live schedule or
// certify network transport, operator readiness or the shared chain's genesis.
func TestNativeReplayThroughCandidateGovernanceUpgrade(t *testing.T) {
	archive := os.Getenv("BDAG_V14_ARCHIVE")
	if archive == "" {
		t.Skip("isolated archived block bodies not configured")
	}
	previousNet := params.ActiveNetParams
	previousConfig := evmparams.BdagTestnetChainConfig
	previousFork, previousBlock := evmparams.BdagTestnetGovernanceFork, evmparams.BdagTestnetGovernanceForkBlock
	previousUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams = previousNet
		evmparams.BdagTestnetChainConfig = previousConfig
		evmparams.BdagTestnetGovernanceFork, evmparams.BdagTestnetGovernanceForkBlock = previousFork, previousBlock
		upgrades.ReplaceUpgrades(previousUpgrades)
	})
	params.ActiveNetParams = &params.TestNetParam
	nativeArchive := rawdb.NewMemoryDatabase()
	evmArchive, err := rawdb.NewDatabaseWithFreezer(memorydb.New(), t.TempDir(), "upgrade-replay-archive", false)
	require.NoError(t, err)
	t.Cleanup(func() { nativeArchive.Close(); evmArchive.Close() })
	_, err = snapshot.MultiRead(archive, map[string]ethdb.Database{snapshot.SourceChain: nativeArchive, snapshot.SourceEVM: evmArchive}, snapshot.ReadOptions{Network: "testnet", RequireEmpty: true})
	require.NoError(t, err)
	archivedActivation := rawdb.ReadHeader(evmArchive, rawdb.ReadCanonicalHash(evmArchive, 5084), 5084)
	require.NotNil(t, archivedActivation)
	manifest := evmparams.TestnetGovernanceArtifactTemplate()
	manifest.SignalWindowStartLayer, manifest.ActivationLayer = 1000, 4999
	manifest.NotBeforeTimestamp = archivedActivation.Time
	manifest.NoticePublishedAt = archivedActivation.Time - 86400
	manifest.InitialReleaseDistributedAt = manifest.NoticePublishedAt
	require.NoError(t, manifest.Validate())
	candidateConfig := *previousConfig
	candidateConfig.BdagGovernanceForkBlock, candidateConfig.BdagGovernanceFork = big.NewInt(5000), manifest
	evmparams.BdagTestnetChainConfig = &candidateConfig
	evmparams.BdagTestnetGovernanceFork, evmparams.BdagTestnetGovernanceForkBlock = manifest, big.NewInt(5000)
	allocationRaw, err := os.ReadFile(os.Getenv("BDAG_V14_ALLOCATIONS"))
	require.NoError(t, err)
	var allocation struct {
		Allocations []struct {
			Address        string
			AllocationBdag string
		}
	}
	require.NoError(t, json.Unmarshal(allocationRaw, &allocation))
	bootstrapRaw, err := os.ReadFile("../bevm/bdag/consensus/bytecode/TestnetGovernanceBootstrap.alloc.json")
	require.NoError(t, err)
	var bootstrap core.GenesisAlloc
	require.NoError(t, json.Unmarshal(bootstrapRaw, &bootstrap))
	require.Len(t, bootstrap, 19)
	appliedSlot := crypto.Keccak256Hash([]byte("bdag.governance.v3.activation.manifest"))
	var expectedHash, expectedRoot ethcommon.Hash
	var expectedApplied uint64
	for run := 0; run < 2; run++ {
		t.Run(fmt.Sprintf("fresh-replay-%d", run+1), func(t *testing.T) {
			upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
			cfg := common.DefaultConfig(t.TempDir())
			cfg.NoFileLogging, cfg.DebugLevel = true, "error"
			cfg.EVMEnv = "--cache 64 --db.engine pebble"
			for _, row := range allocation.Allocations {
				cfg.GenesisAlloc = append(cfg.GenesisAlloc, fmt.Sprintf("%s:%s", row.Address, row.AllocationBdag))
			}
			rawConfig := *cfg // SetupConfig appends the network to paths once per process.
			t.Setenv("BDAG_NETWORK", "testnet")
			require.NoError(t, common.SetupConfig(cfg))
			interrupt := make(chan struct{})
			db, err := chaindb.Open(cfg, interrupt)
			require.NoError(t, err)
			cons := New(cfg, db, interrupt, make(chan struct{}, 1))
			require.NoError(t, cons.Init())
			bc := cons.BlockChain().(*blockchain.BlockChain)
			require.NoError(t, bc.Start())
			stopped := false
			shutdown := func() {
				if !stopped {
					close(interrupt)
					require.NoError(t, bc.Stop())
					db.Close()
					stopped = true
				}
			}
			t.Cleanup(shutdown)
			var applied uint64
			for id := uint64(1); id <= 5106; id++ {
				body := nativeraw.ReadBodyByID(nativeArchive, id)
				require.NotNil(t, body, "native body %d", id)
				_, orphan, err := bc.ProcessBlock(body, blockchain.BFNone, nil)
				require.NoError(t, err, "native acceptance at ID %d", id)
				require.False(t, orphan, "native orphan at ID %d", id)
				if id < 4998 {
					continue
				}
				chain := bc.EVMChain().ETHChain().Ether().BlockChain()
				state, err := chain.State()
				require.NoError(t, err)
				marker := state.GetState(evmparams.ChainTallyAddr, appliedSlot).Big().Uint64()
				if marker == 0 {
					require.Zero(t, state.GetState(evmparams.ChainTallyAddr, evmparams.SlotRewardVotingBootstrapEpochPlus).Big().Sign())
					continue
				}
				if applied == 0 {
					applied = marker
					require.GreaterOrEqual(t, chain.CurrentBlock().Time, manifest.NotBeforeTimestamp)
					nativeUpgrade := upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name]
					require.NotNil(t, nativeUpgrade)
					require.True(t, nativeUpgrade.LockedIn)
					require.True(t, nativeUpgrade.AnchorSet)
					require.EqualValues(t, 4999, nativeUpgrade.ActivateAnchor)
					for addr, account := range bootstrap {
						require.Equal(t, account.Code, state.GetCode(addr), "bootstrap runtime %s", addr)
					}
					require.Zero(t, state.GetState(evmparams.ChainTallyAddr, evmparams.SlotRewardVotingFirstEpochPlus).Big().Sign(), "no votes in partial bootstrap epoch")
					t.Logf("Bootstrap at EVM %d native ID %d time %d, native anchor %d, 19 runtime codes match", marker, id, chain.CurrentBlock().Time, nativeUpgrade.ActivateAnchor)
				}
				require.Equal(t, applied, marker, "activation must not execute twice")
			}
			require.NotZero(t, applied, "new candidate must actually bootstrap")
			chain := bc.EVMChain().ETHChain().Ether().BlockChain()
			state, err := chain.State()
			require.NoError(t, err)
			require.Positive(t, state.GetState(evmparams.ChainTallyAddr, evmparams.SlotRewardVotingFirstEpochPlus).Big().Sign(), "complete reward epoch must become eligible")
			head := bc.Blockdag().GetMainChainTip().GetState()
			require.NoError(t, bc.CheckEVMCheckpoint(head.GetEVMNumber(), head.GetEVMHash(), head.GetEVMRoot()))
			badRoot := head.GetEVMRoot()
			badRoot[0] ^= 0xff // Zero means omitted, not a conflicting commitment.
			require.Error(t, bc.CheckEVMCheckpoint(head.GetEVMNumber(), head.GetEVMHash(), badRoot), "mismatched checkpoint guard remains enabled")
			if run == 0 {
				expectedHash, expectedRoot, expectedApplied = head.GetEVMHash(), head.GetEVMRoot(), applied
			} else {
				require.Equal(t, expectedHash, head.GetEVMHash())
				require.Equal(t, expectedRoot, head.GetEVMRoot())
				require.Equal(t, expectedApplied, applied)
			}
			t.Logf("Rebuilt candidate native history through 5106, EVM %d hash %s root %s", head.GetEVMNumber(), head.GetEVMHash(), head.GetEVMRoot())
			// Re-open actual persisted native + EVM databases, not an in-memory
			// state copy. Discard process-global upgrade state as a new process does.
			shutdown()
			upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
			persistedDataDir := cfg.DataDir
			restartConfig := rawConfig
			cfg = &restartConfig
			require.NoError(t, common.SetupConfig(cfg))
			require.Equal(t, persistedDataDir, cfg.DataDir, "restart must reopen the same database")
			interrupt = make(chan struct{})
			db, err = chaindb.Open(cfg, interrupt)
			require.NoError(t, err)
			cons = New(cfg, db, interrupt, make(chan struct{}, 1))
			require.NoError(t, cons.Init())
			bc = cons.BlockChain().(*blockchain.BlockChain)
			require.NoError(t, bc.Start())
			stopped = false
			chain = bc.EVMChain().ETHChain().Ether().BlockChain()
			state, err = chain.State()
			require.NoError(t, err)
			require.Equal(t, expectedHash, chain.CurrentBlock().Hash(), "restart EVM hash")
			require.Equal(t, expectedRoot, chain.CurrentBlock().Root, "restart state root")
			require.Equal(t, applied, state.GetState(evmparams.ChainTallyAddr, appliedSlot).Big().Uint64(), "restart activation marker")
			restartedUpgrade := upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name]
			require.NotNil(t, restartedUpgrade)
			require.True(t, restartedUpgrade.Active && restartedUpgrade.LockedIn && restartedUpgrade.AnchorSet)
			require.EqualValues(t, 4999, restartedUpgrade.ActivateAnchor)
			t.Log("Graceful restart preserved EVM head, state root, native anchor and one-time bootstrap marker")
		})
	}
}
