package bdag

import (
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	qevent "github.com/BlockdagNetworkLabs/bdag/core/event"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	ethcore "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/miner"
	ethparams "github.com/ethereum/go-ethereum/params"
)

type testConsensusWithChain struct {
	model.Consensus
	chain model.BlockChain
}

func (c testConsensusWithChain) BlockChain() model.BlockChain {
	return c.chain
}

type testCurrentAwareChain struct {
	model.BlockChain
	current bool
	mainTip model.Block
}

func (c testCurrentAwareChain) IsCurrent() bool {
	return c.current
}

func (c testCurrentAwareChain) GetMainChainTip() model.Block {
	return c.mainTip
}

type testEVMState struct {
	model.BlockState
	evmNumber uint64
}

func (s testEVMState) GetEVMNumber() uint64 {
	return s.evmNumber
}

type testEVMMainTip struct {
	model.Block
	state model.BlockState
}

func (b testEVMMainTip) GetState() model.BlockState {
	return b.state
}

type testSyncProgressBackend struct {
	progress ethereum.SyncProgress
}

func (b testSyncProgressBackend) BlockChain() *ethcore.BlockChain {
	return nil
}

func (b testSyncProgressBackend) TxPool() *txpool.TxPool {
	return nil
}

func (b testSyncProgressBackend) SyncProgress() ethereum.SyncProgress {
	return b.progress
}

type canonicalMiningTestBackend struct {
	chain  *ethcore.BlockChain
	txpool *txpool.TxPool
}

func (b canonicalMiningTestBackend) BlockChain() *ethcore.BlockChain { return b.chain }
func (b canonicalMiningTestBackend) TxPool() *txpool.TxPool          { return b.txpool }

type canonicalMiningTestChain struct {
	model.BlockChain
	header *etypes.Header
}

func (c canonicalMiningTestChain) CommittedEVMHead() (common.Hash, uint64, common.Hash, bool, bool) {
	return c.header.Hash(), c.header.Number.Uint64(), c.header.Root, true, true
}

func (canonicalMiningTestChain) GetMainChainTip() model.Block { return nil }
func (canonicalMiningTestChain) IsCurrent() bool              { return false }

type canonicalMiningTestConsensus struct {
	model.Consensus
	chain  model.BlockChain
	events qevent.Feed
}

func (c *canonicalMiningTestConsensus) BlockChain() model.BlockChain { return c.chain }
func (c *canonicalMiningTestConsensus) Events() *qevent.Feed         { return &c.events }

func newCanonicalMiningResetTestPool(t *testing.T) (*BdagPool, *canonicalMiningTestConsensus) {
	t.Helper()

	db := rawdb.NewMemoryDatabase()
	engine := ethash.NewFaker()
	chain, err := ethcore.NewBlockChain(db, nil, &ethcore.Genesis{
		Config:   ethparams.AllDevChainProtocolChanges,
		GasLimit: miner.DefaultConfig.GasCeil,
	}, nil, engine, vm.Config{}, nil)
	if err != nil {
		t.Fatalf("NewBlockChain() error = %v", err)
	}
	t.Cleanup(func() { chain.Stop() })

	legacy := legacypool.New(legacypool.Config{Journal: ""}, chain)
	transactions, err := txpool.New(0, chain, []txpool.SubPool{legacy})
	if err != nil {
		t.Fatalf("txpool.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := transactions.Close(); err != nil {
			t.Errorf("TxPool.Close() error = %v", err)
		}
	})

	consensus := &canonicalMiningTestConsensus{
		chain: canonicalMiningTestChain{header: chain.CurrentBlock()},
	}
	pool := &BdagPool{}
	if err := pool.init(
		consensus,
		&miner.Config{GasCeil: miner.DefaultConfig.GasCeil, GasPrice: big.NewInt(1)},
		chain.Config(),
		engine,
		canonicalMiningTestBackend{chain: chain, txpool: transactions},
		nil,
	); err != nil {
		t.Fatalf("BdagPool.init() error = %v", err)
	}
	pool.Start()
	waitForPoolEventSubscription(t, consensus)
	t.Cleanup(pool.Stop)
	return pool, consensus
}

func waitForPoolEventSubscription(t *testing.T, consensus *canonicalMiningTestConsensus) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		ack := make(chan struct{}, 1)
		consensus.Events().Send(&qevent.Event{Ack: ack})
		select {
		case <-ack:
			return
		default:
		}
		select {
		case <-timeout.C:
			t.Fatal("BdagPool downloader-event handler did not subscribe")
		case <-tick.C:
		}
	}
}

func sendDownloaderStart(t *testing.T, consensus *canonicalMiningTestConsensus) {
	t.Helper()
	ack := make(chan struct{}, 1)
	consensus.Events().Send(&qevent.Event{Data: qevent.DownloaderStart, Ack: ack})
	select {
	case <-ack:
	case <-time.After(5 * time.Second):
		t.Fatal("BdagPool downloader-event handler did not process DownloaderStart")
	}
}

func assertNoUsableMiningWork(t *testing.T, pool *BdagPool) {
	t.Helper()
	if block, receipts, statedb := pool.Pending(); block != nil || receipts != nil || statedb != nil {
		t.Fatalf("Pending() exposed mining work while downloader latch active: block=%v receipts=%v state=%v", block != nil, receipts != nil, statedb != nil)
	}
	if block := pool.PendingBlock(); block != nil {
		t.Fatal("PendingBlock() exposed mining work while downloader latch active")
	}
	if block, receipts := pool.PendingBlockAndReceipts(); block != nil || receipts != nil {
		t.Fatalf("PendingBlockAndReceipts() exposed mining work while downloader latch active: block=%v receipts=%v", block != nil, receipts != nil)
	}
}

func TestDownloaderLatchIsZeroValueSafeAndGenerational(t *testing.T) {
	var latch downloaderLatch
	if latch.Load() {
		t.Fatal("zero-value latch is active")
	}
	if got := latch.Snapshot(); got != 0 {
		t.Fatalf("zero-value generation = %d, want 0", got)
	}

	latch.Store(false)
	first := latch.Snapshot()
	latch.Store(false)
	second := latch.Snapshot()
	if second != first+1 {
		t.Fatalf("same-value event generation = %d, want %d", second, first+1)
	}
	latch.Store(true)
	if !latch.Load() {
		t.Fatal("DownloaderStart did not activate latch")
	}
	if got := latch.Snapshot(); got != second+1 {
		t.Fatalf("start generation = %d, want %d", got, second+1)
	}
}

func TestDownloaderLatchGenerationSafeClearInterleavings(t *testing.T) {
	tests := []struct {
		name string
		fn   func(*testing.T, *downloaderLatch)
	}{
		{
			name: "start before snapshot",
			fn: func(t *testing.T, latch *downloaderLatch) {
				latch.Store(true)
				generation := latch.Snapshot()
				if !latch.Load() {
					t.Fatal("active latch was not retained through snapshot")
				}
				if !latch.ClearIfUnchanged(generation) || latch.Load() {
					t.Fatal("unchanged stale latch was not recycled")
				}
			},
		},
		{
			name: "start after snapshot",
			fn: func(t *testing.T, latch *downloaderLatch) {
				generation := latch.Snapshot()
				latch.Store(true)
				if latch.ClearIfUnchanged(generation) {
					t.Fatal("newer start was cleared")
				}
				if !latch.Load() {
					t.Fatal("newer start did not remain active")
				}
			},
		},
		{
			name: "start immediately before compare and clear",
			fn: func(t *testing.T, latch *downloaderLatch) {
				generation := latch.Snapshot()
				latch.Store(true)
				if latch.ClearIfUnchanged(generation) {
					t.Fatal("start before compare-and-clear was lost")
				}
				if !latch.Load() {
					t.Fatal("start before compare-and-clear did not remain active")
				}
			},
		},
		{
			name: "start immediately after clear",
			fn: func(t *testing.T, latch *downloaderLatch) {
				generation := latch.Snapshot()
				if !latch.ClearIfUnchanged(generation) {
					t.Fatal("unchanged latch was not cleared")
				}
				latch.Store(true)
				if !latch.Load() {
					t.Fatal("start after clear did not activate latch")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.fn(t, new(downloaderLatch))
		})
	}
}

func TestDownloaderLatchConcurrentStartSurvivesClearAttempt(t *testing.T) {
	var latch downloaderLatch
	generation := latch.Snapshot()
	start := make(chan struct{})
	started := make(chan struct{})
	go func() {
		<-start
		latch.Store(true)
		close(started)
	}()
	close(start)
	<-started
	if latch.ClearIfUnchanged(generation) {
		t.Fatal("concurrent DownloaderStart was cleared")
	}
	if !latch.Load() {
		t.Fatal("concurrent DownloaderStart did not remain active")
	}
}

func TestFinalizeCanonicalTemplateRejectsStartAtInstallBoundary(t *testing.T) {
	pool := &BdagPool{}
	generation := pool.syncing.Snapshot()
	candidate := &environment{}
	pool.beforeCanonicalTemplateBoundary = func() {
		pool.syncing.Store(true)
	}

	err := pool.finalizeCanonicalTemplate(generation, func() bool { return true }, func() {
		pool.installTemplateEnvironment(candidate)
	})
	if !errors.Is(err, errBdagPoolSyncing) {
		t.Fatalf("finalizeCanonicalTemplate() error = %v, want %v", err, errBdagPoolSyncing)
	}
	if pool.current != nil {
		t.Fatal("candidate installed after DownloaderStart at boundary")
	}
	if !pool.syncing.Load() {
		t.Fatal("DownloaderStart did not remain active")
	}
	if pool.PendingBlock() != nil {
		t.Fatal("rejected candidate was exposed as a pending block")
	}
}

func TestFinalizeCanonicalTemplateStartAfterBoundaryRemainsActive(t *testing.T) {
	pool := &BdagPool{}
	generation := pool.syncing.Snapshot()
	candidate := &environment{}
	err := pool.finalizeCanonicalTemplate(generation, func() bool { return true }, func() {
		pool.installTemplateEnvironment(candidate)
	})
	if err != nil {
		t.Fatalf("finalizeCanonicalTemplate() error = %v, want nil", err)
	}
	if pool.current != candidate {
		t.Fatal("candidate was not installed at the atomic boundary")
	}

	pool.syncing.Store(true)
	if !pool.syncing.Load() {
		t.Fatal("DownloaderStart after boundary did not activate latch")
	}
	assertNoUsableMiningWork(t, pool)
}

func TestResetTemplateForCanonicalMiningRejectsDownloaderStartAtFinalizationBoundary(t *testing.T) {
	pool, consensus := newCanonicalMiningResetTestPool(t)
	boundary := make(chan struct{})
	resume := make(chan struct{})
	pool.beforeCanonicalTemplateBoundary = func() {
		close(boundary)
		<-resume
	}

	result := make(chan error, 1)
	go func() {
		result <- pool.ResetTemplateForCanonicalMining()
	}()
	select {
	case <-boundary:
	case <-time.After(5 * time.Second):
		t.Fatal("ResetTemplateForCanonicalMining() did not reach finalization boundary")
	}

	sendDownloaderStart(t, consensus)
	close(resume)
	err := <-result
	if !errors.Is(err, errBdagPoolSyncing) {
		t.Fatalf("ResetTemplateForCanonicalMining() error = %v, want %v", err, errBdagPoolSyncing)
	}
	var resetErr *PendingTemplateResetError
	if !errors.As(err, &resetErr) || resetErr.Code != PendingTemplateResetSyncing {
		t.Fatalf("ResetTemplateForCanonicalMining() reset code = %#v, want %s", resetErr, PendingTemplateResetSyncing)
	}
	if !pool.syncing.Load() {
		t.Fatal("DownloaderStart did not leave the latch active")
	}
	if pool.current != nil {
		t.Fatal("candidate was installed after DownloaderStart at finalization boundary")
	}
	pool.snapshotMu.RLock()
	published := pool.snapshotBlock != nil || pool.snapshotReceipts != nil || pool.snapshotState != nil
	pool.snapshotMu.RUnlock()
	if published {
		t.Fatal("candidate was published after DownloaderStart at finalization boundary")
	}
	assertNoUsableMiningWork(t, pool)
}

func TestResetTemplateForCanonicalMiningHidesInstalledWorkAfterDownloaderStart(t *testing.T) {
	pool, consensus := newCanonicalMiningResetTestPool(t)
	if err := pool.ResetTemplateForCanonicalMining(); err != nil {
		t.Fatalf("ResetTemplateForCanonicalMining() error = %v", err)
	}
	if pool.current == nil || pool.PendingBlock() == nil {
		t.Fatal("successful canonical reset did not install and publish its candidate")
	}

	sendDownloaderStart(t, consensus)
	if !pool.syncing.Load() {
		t.Fatal("DownloaderStart did not activate latch after finalization")
	}
	if pool.current == nil {
		t.Fatal("DownloaderStart withdrew the installed candidate instead of gating access")
	}
	assertNoUsableMiningWork(t, pool)
}

func TestResetTemplateStoppedReturnsError(t *testing.T) {
	pool := &BdagPool{quit: make(chan struct{}), resetTemplate: make(chan *resetTemplateMsg)}

	err := pool.ResetTemplate()
	if !errors.Is(err, errBdagPoolStopped) {
		t.Fatalf("ResetTemplate() error = %v, want %v", err, errBdagPoolStopped)
	}
	var resetErr *PendingTemplateResetError
	if !errors.As(err, &resetErr) || resetErr.Code != PendingTemplateResetStopped {
		t.Fatalf("ResetTemplate() reset code = %#v, want %s", resetErr, PendingTemplateResetStopped)
	}
}

func TestResetTemplatePropagatesHandlerError(t *testing.T) {
	want := errors.New("template rebuild failed")
	pool := &BdagPool{
		running:       1,
		quit:          make(chan struct{}),
		resetTemplate: make(chan *resetTemplateMsg),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		msg := <-pool.resetTemplate
		msg.reply <- want
	}()

	err := pool.ResetTemplate()
	if !errors.Is(err, want) {
		t.Fatalf("ResetTemplate() error = %v, want %v", err, want)
	}
	var resetErr *PendingTemplateResetError
	if !errors.As(err, &resetErr) || resetErr.Code != PendingTemplateResetFailed {
		t.Fatalf("ResetTemplate() reset code = %#v, want %s", resetErr, PendingTemplateResetFailed)
	}
	<-done
}

func TestResetTemplateForMiningKeepsSyncingGuard(t *testing.T) {
	pool := &BdagPool{
		running:       1,
		quit:          make(chan struct{}),
		resetTemplate: make(chan *resetTemplateMsg),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		msg := <-pool.resetTemplate
		if msg.allowWhileSyncing {
			t.Error("ResetTemplateForMining bypassed the syncing guard")
		}
		msg.reply <- nil
	}()

	if err := pool.ResetTemplateForMining(); err != nil {
		t.Fatalf("ResetTemplateForMining() error = %v, want nil", err)
	}
	<-done
}

func TestUpdateTemplateSyncingReturnsError(t *testing.T) {
	pool := &BdagPool{}
	pool.syncing.Store(true)

	err := pool.updateTemplate(nil, 1, false, false)
	if !errors.Is(err, errBdagPoolSyncing) {
		t.Fatalf("updateTemplate() error = %v, want %v", err, errBdagPoolSyncing)
	}
}

func TestUpdateTemplateEVMSyncProgressReturnsError(t *testing.T) {
	pool := &BdagPool{eth: testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 99, HighestBlock: 100}}}

	err := pool.updateTemplate(nil, 1, false, false)
	if !errors.Is(err, errBdagPoolSyncing) {
		t.Fatalf("updateTemplate() error = %v, want %v", err, errBdagPoolSyncing)
	}
}

func TestPrepareChainHeadTemplateUpdateSkipsWhileSyncing(t *testing.T) {
	oldInterrupt := new(atomic.Int32)
	pool := &BdagPool{interrupt: oldInterrupt}
	pool.syncing.Store(true)

	interrupt, shouldUpdate := pool.prepareChainHeadTemplateUpdate(nil, time.Now())

	if shouldUpdate {
		t.Fatal("chain-head template update should be skipped while syncing")
	}
	if interrupt != nil {
		t.Fatalf("interrupt = %v, want nil", interrupt)
	}
	if oldInterrupt.Load() != commitInterruptNewHead {
		t.Fatalf("old interrupt = %d, want %d", oldInterrupt.Load(), commitInterruptNewHead)
	}
	if pool.interrupt != nil {
		t.Fatal("pool interrupt should be cleared when automatic template update is skipped")
	}
}

func TestPrepareChainHeadTemplateUpdateSkipsWhileEVMSyncProgressActive(t *testing.T) {
	oldInterrupt := new(atomic.Int32)
	pool := &BdagPool{
		eth:       testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 99, HighestBlock: 100}},
		interrupt: oldInterrupt,
	}

	interrupt, shouldUpdate := pool.prepareChainHeadTemplateUpdate(nil, time.Now())

	if shouldUpdate {
		t.Fatal("chain-head template update should be skipped while EVM sync progress is active")
	}
	if interrupt != nil {
		t.Fatalf("interrupt = %v, want nil", interrupt)
	}
	if oldInterrupt.Load() != commitInterruptNewHead {
		t.Fatalf("old interrupt = %d, want %d", oldInterrupt.Load(), commitInterruptNewHead)
	}
	if pool.interrupt != nil {
		t.Fatal("pool interrupt should be cleared when automatic template update is skipped")
	}
}

func TestInitialSyncingFollowsEVMSyncProgress(t *testing.T) {
	pool := &BdagPool{eth: testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 99, HighestBlock: 100}}}

	if got := pool.initialSyncing(); !got {
		t.Fatalf("initialSyncing() = %v, want true", got)
	}
}

func TestDeferredInitialTemplateDoesNotSetDownloaderLatch(t *testing.T) {
	pool := &BdagPool{eth: testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 99, HighestBlock: 100}}}

	if !pool.deferInitialTemplateUpdate() {
		t.Fatal("deferInitialTemplateUpdate() = false, want true")
	}
	if pool.syncing.Load() {
		t.Fatal("derived initial sync state must not become a persistent downloader latch")
	}
}

func TestInitialSyncingIgnoresEVMSyncProgressAtNativeTip(t *testing.T) {
	pool := &BdagPool{
		eth: testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 100, HighestBlock: 200}},
		consensus: testConsensusWithChain{chain: testCurrentAwareChain{
			current: true,
			mainTip: testEVMMainTip{
				state: testEVMState{evmNumber: 100},
			},
		}},
	}

	if got := pool.initialSyncing(); got {
		t.Fatalf("initialSyncing() = %v, want false", got)
	}
}

func TestInitialSyncingBlocksEVMSyncProgressBehindNativeTip(t *testing.T) {
	pool := &BdagPool{
		eth: testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 99, HighestBlock: 200}},
		consensus: testConsensusWithChain{chain: testCurrentAwareChain{
			current: true,
			mainTip: testEVMMainTip{
				state: testEVMState{evmNumber: 100},
			},
		}},
	}

	if got := pool.initialSyncing(); !got {
		t.Fatalf("initialSyncing() = %v, want true", got)
	}
}

func TestEVMBehindPeerTarget(t *testing.T) {
	tests := []struct {
		name    string
		current *etypes.Header
		target  uint64
		want    bool
	}{
		{name: "no target", current: &etypes.Header{Number: big.NewInt(100)}, target: 0, want: false},
		{name: "missing current", target: 100, want: true},
		{name: "behind", current: &etypes.Header{Number: big.NewInt(99)}, target: 100, want: true},
		{name: "equal", current: &etypes.Header{Number: big.NewInt(100)}, target: 100, want: false},
		{name: "ahead", current: &etypes.Header{Number: big.NewInt(101)}, target: 100, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := evmBehindPeerTarget(test.current, test.target); got != test.want {
				t.Fatalf("evmBehindPeerTarget() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestShouldProcessTxEventForTemplateSkipsWhileSyncing(t *testing.T) {
	pool := &BdagPool{current: &environment{}}
	pool.syncing.Store(true)

	if got := pool.shouldProcessTxEventForTemplate(); got {
		t.Fatalf("shouldProcessTxEventForTemplate() = %v, want false", got)
	}
}

func TestShouldProcessTxEventForTemplateSkipsWhileEVMSyncProgressActive(t *testing.T) {
	pool := &BdagPool{
		current: &environment{},
		eth:     testSyncProgressBackend{progress: ethereum.SyncProgress{CurrentBlock: 99, HighestBlock: 100}},
	}

	if got := pool.shouldProcessTxEventForTemplate(); got {
		t.Fatalf("shouldProcessTxEventForTemplate() = %v, want false", got)
	}
}

func TestShouldProcessTxEventForTemplateRequiresCurrentTemplate(t *testing.T) {
	pool := &BdagPool{}

	if got := pool.shouldProcessTxEventForTemplate(); got {
		t.Fatalf("shouldProcessTxEventForTemplate() = %v, want false", got)
	}
}

func TestShouldProcessTxEventForTemplateReady(t *testing.T) {
	pool := &BdagPool{current: &environment{}}

	if got := pool.shouldProcessTxEventForTemplate(); !got {
		t.Fatalf("shouldProcessTxEventForTemplate() = %v, want true", got)
	}
}

func TestPrepareChainHeadTemplateUpdateCreatesFreshInterruptWhenReady(t *testing.T) {
	oldInterrupt := new(atomic.Int32)
	pool := &BdagPool{interrupt: oldInterrupt}

	interrupt, shouldUpdate := pool.prepareChainHeadTemplateUpdate(nil, time.Now())

	if !shouldUpdate {
		t.Fatal("chain-head template update should run when the pool is not syncing")
	}
	if interrupt == nil {
		t.Fatal("expected fresh interrupt")
	}
	if pool.interrupt != interrupt {
		t.Fatal("fresh interrupt was not installed on the pool")
	}
	if oldInterrupt.Load() != commitInterruptNewHead {
		t.Fatalf("old interrupt = %d, want %d", oldInterrupt.Load(), commitInterruptNewHead)
	}
}

func TestPrepareChainHeadTemplateUpdateSkipsHistoricalReplayHead(t *testing.T) {
	now := time.Unix(100, 0)
	oldBlock := etypes.NewBlockWithHeader(&etypes.Header{Time: uint64(now.Add(-time.Hour).Unix())})
	oldInterrupt := new(atomic.Int32)
	pool := &BdagPool{interrupt: oldInterrupt}

	interrupt, shouldUpdate := pool.prepareChainHeadTemplateUpdate(oldBlock, now)

	if shouldUpdate {
		t.Fatal("chain-head template update should be skipped for historical replay heads")
	}
	if interrupt != nil {
		t.Fatalf("interrupt = %v, want nil", interrupt)
	}
	if oldInterrupt.Load() != commitInterruptNewHead {
		t.Fatalf("old interrupt = %d, want %d", oldInterrupt.Load(), commitInterruptNewHead)
	}
	if pool.interrupt != nil {
		t.Fatal("pool interrupt should be cleared when historical template update is skipped")
	}
}

func TestPrepareChainHeadTemplateUpdateAllowsFreshHead(t *testing.T) {
	now := time.Unix(100, 0)
	freshBlock := etypes.NewBlockWithHeader(&etypes.Header{Time: uint64(now.Add(-2 * time.Second).Unix())})
	pool := &BdagPool{}

	interrupt, shouldUpdate := pool.prepareChainHeadTemplateUpdate(freshBlock, now)

	if !shouldUpdate {
		t.Fatal("chain-head template update should run for fresh heads")
	}
	if interrupt == nil {
		t.Fatal("expected fresh interrupt")
	}
}

func TestInitialSyncingFollowsChainCurrentness(t *testing.T) {
	tests := []struct {
		name    string
		current bool
		want    bool
	}{
		{name: "current chain", current: true, want: false},
		{name: "syncing chain", current: false, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := &BdagPool{
				consensus: testConsensusWithChain{
					chain: testCurrentAwareChain{current: test.current},
				},
			}

			if got := pool.initialSyncing(); got != test.want {
				t.Fatalf("initialSyncing() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestInitialSyncingWithoutCurrentCheckerKeepsLegacyStartup(t *testing.T) {
	pool := &BdagPool{consensus: testConsensusWithChain{chain: struct{ model.BlockChain }{}}}

	if got := pool.initialSyncing(); got {
		t.Fatalf("initialSyncing() = %v, want false", got)
	}
}

func TestStartupDeferralCompletesAfterBackendsReachCurrentTip(t *testing.T) {
	pool := &BdagPool{
		consensus: testConsensusWithChain{
			chain: testCurrentAwareChain{current: false},
		},
	}
	if !pool.deferInitialTemplateUpdate() {
		t.Fatal("startup template must wait for backend catch-up")
	}
	pool.consensus = testConsensusWithChain{chain: testCurrentAwareChain{current: true}}

	if got := pool.isSyncing(); got {
		t.Fatalf("isSyncing() = %v, want false after startup catch-up", got)
	}
	if pool.syncing.Load() {
		t.Fatal("startup deferral must not create a downloader latch")
	}
}

func TestDownloaderSyncLatchRemainsUntilDownloaderEnd(t *testing.T) {
	pool := &BdagPool{
		consensus: testConsensusWithChain{
			chain: testCurrentAwareChain{current: true},
		},
	}
	pool.syncing.Store(true)

	if got := pool.isSyncing(); !got {
		t.Fatalf("isSyncing() = %v, want true while downloader latch is active", got)
	}
}

func TestEVMImportBehindMainTipBlocksTemplateReadiness(t *testing.T) {
	tests := []struct {
		name       string
		currentEVM *etypes.Header
		mainTipEVM uint64
		want       bool
	}{
		{name: "evm head behind main tip state", currentEVM: &etypes.Header{Number: big.NewInt(99)}, mainTipEVM: 100, want: true},
		{name: "evm head equal main tip state", currentEVM: &etypes.Header{Number: big.NewInt(100)}, mainTipEVM: 100, want: false},
		{name: "evm head ahead of main tip state", currentEVM: &etypes.Header{Number: big.NewInt(101)}, mainTipEVM: 100, want: false},
		{name: "genesis evm state keeps legacy startup", currentEVM: &etypes.Header{Number: big.NewInt(0)}, mainTipEVM: 0, want: false},
		{name: "missing evm head blocks when main tip expects evm state", currentEVM: nil, mainTipEVM: 100, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mainTip := testEVMMainTip{state: testEVMState{evmNumber: test.mainTipEVM}}
			if got := evmImportBehindMainTip(test.currentEVM, mainTip); got != test.want {
				t.Fatalf("evmImportBehindMainTip() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCommittedEVMHeadMatchesRequiresExactValidTuple(t *testing.T) {
	header := &etypes.Header{
		Number:     big.NewInt(100),
		ParentHash: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
	}
	hash := header.Hash()

	tests := []struct {
		name    string
		current *etypes.Header
		hash    common.Hash
		number  uint64
		root    common.Hash
		valid   bool
		known   bool
		want    bool
	}{
		{name: "exact", current: header, hash: hash, number: 100, root: header.Root, valid: true, known: true, want: true},
		{name: "unknown commitment", current: header, hash: hash, number: 100, root: header.Root, valid: true},
		{name: "invalid commitment", current: header, hash: hash, number: 100, root: header.Root, known: true},
		{name: "missing head", hash: hash, number: 100, root: header.Root, valid: true, known: true},
		{name: "height mismatch", current: header, hash: hash, number: 99, root: header.Root, valid: true, known: true},
		{name: "hash mismatch", current: header, hash: common.HexToHash("0x03"), number: 100, root: header.Root, valid: true, known: true},
		{name: "root mismatch", current: header, hash: hash, number: 100, root: common.HexToHash("0x04"), valid: true, known: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := committedEVMHeadMatches(test.current, test.hash, test.number, test.root, test.valid, test.known); got != test.want {
				t.Fatalf("committedEVMHeadMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCanonicalMiningTemplateReadyRequiresEveryLivePredicate(t *testing.T) {
	header := &etypes.Header{Number: big.NewInt(100), Root: common.HexToHash("0x02")}
	tests := []struct {
		name               string
		valid              bool
		known              bool
		evmImportBehind    bool
		evmBehindPeer      bool
		syncProgressActive bool
		want               bool
	}{
		{name: "exact and live", valid: true, known: true, want: true},
		{name: "native import behind", valid: true, known: true, evmImportBehind: true},
		{name: "peer target ahead", valid: true, known: true, evmBehindPeer: true},
		{name: "sync progress active", valid: true, known: true, syncProgressActive: true},
		{name: "commitment invalid", known: true},
		{name: "commitment unknown", valid: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := canonicalMiningTemplateReady(
				header, header.Hash(), 100, header.Root, test.valid, test.known,
				test.evmImportBehind, test.evmBehindPeer, test.syncProgressActive,
			)
			if got != test.want {
				t.Fatalf("canonicalMiningTemplateReady() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestResetTemplateForCanonicalMiningFailsClosedWithoutExactHead(t *testing.T) {
	pool := &BdagPool{}

	err := pool.ResetTemplateForCanonicalMining()
	if !errors.Is(err, errBdagPoolSyncing) {
		t.Fatalf("ResetTemplateForCanonicalMining() error = %v, want %v", err, errBdagPoolSyncing)
	}
	var resetErr *PendingTemplateResetError
	if !errors.As(err, &resetErr) || resetErr.Code != PendingTemplateResetSyncing {
		t.Fatalf("ResetTemplateForCanonicalMining() reset code = %#v, want %s", resetErr, PendingTemplateResetSyncing)
	}
}

func TestResetTemplateForCanonicalMiningNilPoolFailsClosed(t *testing.T) {
	var pool *BdagPool
	err := pool.ResetTemplateForCanonicalMining()
	if !errors.Is(err, errBdagPoolSyncing) {
		t.Fatalf("ResetTemplateForCanonicalMining() error = %v, want %v", err, errBdagPoolSyncing)
	}
}

func TestInstallTemplateEnvironmentReplacesCurrent(t *testing.T) {
	oldEnv := &environment{}
	newEnv := &environment{}
	pool := &BdagPool{current: oldEnv}

	pool.installTemplateEnvironment(newEnv)

	if pool.current != newEnv {
		t.Fatal("installTemplateEnvironment did not install the new environment")
	}
}
