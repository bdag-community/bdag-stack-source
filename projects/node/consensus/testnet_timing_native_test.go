package consensus

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"testing"
	"time"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

type timingFixtureClock struct{ now time.Time }

func (c *timingFixtureClock) AdjustedTime() time.Time         { return c.now }
func (c *timingFixtureClock) AddTimeSample(string, time.Time) {}
func (c *timingFixtureClock) Offset() time.Duration           { return 0 }

// Full native template/import/restart integration with real Scrypt solutions.
// ONLY this isolated test uses an easy target and a controlled clock. It retains
// the complete 2,000-layer signal window, 1,500 threshold, 2,000-layer delay and
// 24-hour notice. It is NOT canonical-history, P2P or physical ASIC qualification.
// No production parameter, manifest, network or database is modified.
func TestIsolatedNativeTimingTransition(t *testing.T) {
	if os.Getenv("BDAG_ISOLATED_TIMING_TEST") != "1" {
		t.Skip("explicit isolated native timing fixture not enabled")
	}
	signalCount := uint64(2000)
	if requested := os.Getenv("BDAG_TIMING_SIGNAL_COUNT"); requested != "" {
		var err error
		signalCount, err = strconv.ParseUint(requested, 10, 64)
		require.NoError(t, err)
		require.True(t, signalCount == 1499 || signalCount == 1500 || signalCount == 2000, "qualified fixture count")
	}
	t.Logf("Isolated native fixture: %d of 2000 timing signal layers", signalCount)
	previousNet, previousConfig := params.ActiveNetParams, evmparams.BdagTestnetChainConfig
	previousGov, previousGovBlock := evmparams.BdagTestnetGovernanceFork, evmparams.BdagTestnetGovernanceForkBlock
	previousTiming, previousTimingBlock := evmparams.BdagTestnetTimingFork, evmparams.BdagTestnetTimingForkBlock
	previousUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams, evmparams.BdagTestnetChainConfig = previousNet, previousConfig
		evmparams.BdagTestnetGovernanceFork, evmparams.BdagTestnetGovernanceForkBlock = previousGov, previousGovBlock
		evmparams.BdagTestnetTimingFork, evmparams.BdagTestnetTimingForkBlock = previousTiming, previousTimingBlock
		upgrades.ReplaceUpgrades(previousUpgrades)
	})
	isolateNet := params.TestNetParam
	isolateParams := *params.TestNetParam.Params
	isolatePow := *isolateParams.PowConfig
	isolatePow.ScryptPowLimit = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(1))
	isolatePow.ScryptPowLimitBits = 0x207fffff
	isolateParams.PowConfig = &isolatePow
	isolateNet.Params = &isolateParams
	params.ActiveNetParams = &isolateNet
	genesis := uint64(params.TestNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	start := genesis + 100000
	governance := evmparams.TestnetGovernanceArtifactTemplate()
	governance.SignalWindowStartLayer, governance.ActivationLayer = 1000, 4999
	governance.NotBeforeTimestamp = start + 10000
	governance.NoticePublishedAt = governance.NotBeforeTimestamp - 86400
	governance.InitialReleaseDistributedAt = governance.NoticePublishedAt
	require.NoError(t, governance.Validate())
	timing := &ethparams.BDAGTestnetTimingForkConfig{
		Version: 1, ArtifactSHA256: ethcommon.HexToHash("0xb8d100d8c69746070d02b497ee3e4c2837f9020981d036eff7d5a94ef61a5275"),
		SignalStartLayer: 6000, ActivationLayer: 9999, ReleaseDistributedAt: start - 86400,
		NoticePublishedAt: start - 86400, NotBeforeTimestamp: start,
		EpochSeconds: 1800, TargetBlockSeconds: 2,
	}
	require.NoError(t, timing.Validate(big.NewInt(1043)))
	candidateConfig := *previousConfig
	candidateConfig.BdagGovernanceFork, candidateConfig.BdagGovernanceForkBlock = governance, big.NewInt(5000)
	candidateConfig.BdagTestnetTiming, candidateConfig.BdagTestnetTimingBlock = timing, big.NewInt(6000)
	evmparams.BdagTestnetChainConfig = &candidateConfig
	evmparams.BdagTestnetGovernanceFork, evmparams.BdagTestnetGovernanceForkBlock = governance, big.NewInt(5000)
	evmparams.BdagTestnetTimingFork, evmparams.BdagTestnetTimingForkBlock = timing, big.NewInt(6000)
	clock := &timingFixtureClock{now: time.Unix(int64(start), 0)}
	cfg := common.DefaultConfig(t.TempDir())
	cfg.NoFileLogging, cfg.DebugLevel = true, "error"
	cfg.EVMEnv = "--cache 64 --db.engine pebble"
	t.Setenv("BDAG_NETWORK", "testnet")
	rawConfig := *cfg
	require.NoError(t, common.SetupConfig(cfg))
	// SetupConfig selects the standard network. Restore only the test's copy.
	params.ActiveNetParams = &isolateNet
	interrupt := make(chan struct{})
	db, err := chaindb.Open(cfg, interrupt)
	require.NoError(t, err)
	cons := New(cfg, db, interrupt, make(chan struct{}, 1))
	cons.mediantimeSource = clock
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
	markerSlot := crypto.Keccak256Hash([]byte("bdag.testnet.timing.v1.activation"))
	payout := ethcommon.HexToAddress("0x0000000000000000000000000000000000009876")
	var applied uint64
	for height := uint64(1); height <= 10100; height++ {
		clock.now = time.Unix(int64(start+height*2), 0)
		require.NoError(t, bc.EVMChain().BdagPool().ResetTemplate(), "pending template at %d", height)
		template, err := mining.NewBlockTemplate(&mining.Policy{BlockMaxSize: 1310720}, &isolateParams,
			cons.SigCache(), nil, clock, cons, payout, nil, pow.SCRYPT, mining.CoinbaseFlags(""))
		require.NoError(t, err, "native template %d", height)
		block := template.Block
		if height >= timing.SignalStartLayer+signalCount && height < timing.SignalStartLayer+2000 {
			block.Header.Version &^= upgrades.TestnetTimingV1BitMask
		}
		block.Header.Pow.SetParams(&isolatePow)
		block.Header.Pow.SetMainHeight(pow.MainHeight(height))
		solved := false
		for nonce := uint32(0); nonce < 100000; nonce++ {
			block.Header.Pow.SetNonce(nonce)
			var encoded bytes.Buffer
			require.NoError(t, block.Header.Serialize(&encoded))
			if block.Header.Pow.FindSolver(encoded.Bytes(), ethcommon.Hash{}, block.Header.Difficulty) {
				solved = true
				break
			}
		}
		require.True(t, solved, "isolated Scrypt solution %d bits %x", height, block.Header.Difficulty)
		_, orphan, err := bc.ProcessBlock(types.NewBlock(block), blockchain.BFNone, nil)
		require.NoError(t, err, "native acceptance %d", height)
		require.False(t, orphan)
		if height%1000 == 0 || height >= 9998 {
			chain := bc.EVMChain().ETHChain().Ether().BlockChain()
			state, err := chain.State()
			require.NoError(t, err)
			marker := state.GetState(evmparams.ChainTallyAddr, markerSlot).Big().Uint64()
			if height < timing.ActivationLayer {
				require.Zero(t, marker, "no early activation")
			} else if marker != 0 {
				if applied == 0 {
					applied = marker
				}
				require.Equal(t, applied, marker, "one-time transition")
			}
			if height%1000 == 0 {
				t.Logf("Native %d layer %d EVM %s time %d timing marker %d, definition %+v, chain policy %+v", height, bc.Blockdag().GetMainChainTip().GetLayer(), chain.CurrentBlock().Number, chain.CurrentBlock().Time, marker, upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name], chain.Config().BdagTestnetTiming)
			}
		}
	}
	definition := upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name]
	require.NotNil(t, definition)
	if signalCount >= 1500 {
		require.NotZero(t, applied, "timing transition must execute through native validation")
		require.True(t, definition.LockedIn && definition.AnchorSet && definition.Active)
		require.EqualValues(t, 9999, definition.ActivateAnchor)
	} else {
		require.Zero(t, applied, "1499 votes cannot activate timing")
		require.False(t, definition.LockedIn || definition.AnchorSet || definition.Active)
		require.Zero(t, definition.ActivateAnchor)
	}
	chain := bc.EVMChain().ETHChain().Ether().BlockChain()
	head := chain.CurrentBlock()
	expectedHash, expectedRoot := head.Hash(), head.Root
	require.NoError(t, bc.CheckEVMCheckpoint(head.Number.Uint64(), expectedHash, expectedRoot))
	wrongRoot := expectedRoot
	wrongRoot[0] ^= 1
	require.Error(t, bc.CheckEVMCheckpoint(head.Number.Uint64(), expectedHash, wrongRoot))
	shutdown()
	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	cfg = &rawConfig
	require.NoError(t, common.SetupConfig(cfg))
	params.ActiveNetParams = &isolateNet
	interrupt = make(chan struct{})
	db, err = chaindb.Open(cfg, interrupt)
	require.NoError(t, err)
	cons = New(cfg, db, interrupt, make(chan struct{}, 1))
	cons.mediantimeSource = clock
	require.NoError(t, cons.Init())
	bc = cons.BlockChain().(*blockchain.BlockChain)
	require.NoError(t, bc.Start())
	stopped = false
	chain = bc.EVMChain().ETHChain().Ether().BlockChain()
	require.Equal(t, expectedHash, chain.CurrentBlock().Hash())
	require.Equal(t, expectedRoot, chain.CurrentBlock().Root)
	state, err := chain.State()
	require.NoError(t, err)
	require.Equal(t, applied, state.GetState(evmparams.ChainTallyAddr, markerSlot).Big().Uint64())
	t.Log(fmt.Sprintf("PASS isolated native timing %d/2000 votes, activation %d, restart EVM %s hash %s root %s", signalCount, applied, chain.CurrentBlock().Number, expectedHash, expectedRoot))
}
