//go:build bdag_recovery_test

package blockchain

import (
	"errors"
	"strings"
	"testing"

	bdagevm "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethash "github.com/ethereum/go-ethereum/consensus/ethash"
	ethcore "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

type integrationRewindChain struct {
	*ethcore.BlockChain
	setHeadCalls   int
	postSet        bool
	postHead       *gethtypes.Header
	postBodySet    bool
	postBody       *gethtypes.Block
	postStateSet   bool
	postHasState   bool
	lookedAfterSet bool
	stateReadAfter bool
}

func (c *integrationRewindChain) SetHead(number uint64) error {
	c.setHeadCalls++
	err := c.BlockChain.SetHead(number)
	c.postSet = true
	return err
}

func (c *integrationRewindChain) CurrentBlock() *gethtypes.Header {
	if c.postSet && c.postHead != nil {
		return c.postHead
	}
	return c.BlockChain.CurrentBlock()
}

func (c *integrationRewindChain) GetBlock(hash ethcommon.Hash, number uint64) *gethtypes.Block {
	if c.postSet {
		c.lookedAfterSet = true
		if c.postBodySet {
			return c.postBody
		}
	}
	return c.BlockChain.GetBlock(hash, number)
}

func (c *integrationRewindChain) HasState(root ethcommon.Hash) bool {
	if c.postSet {
		c.stateReadAfter = true
		if c.postStateSet {
			return c.postHasState
		}
	}
	return c.BlockChain.HasState(root)
}

func newGuardRewindIntegrationChain(t *testing.T) (*ethcore.BlockChain, []*gethtypes.Block) {
	t.Helper()
	engine := ethash.NewFaker()
	genesis := &ethcore.Genesis{Config: params.AllEthashProtocolChanges, GasLimit: 30_000_000}
	chain, err := ethcore.NewBlockChain(rawdb.NewMemoryDatabase(), nil, genesis, nil, engine, vm.Config{}, nil)
	if err != nil {
		t.Fatalf("NewBlockChain: %v", err)
	}
	_, blocks, _ := ethcore.GenerateChainWithGenesis(genesis, engine, 2, func(int, *ethcore.BlockGen) {})
	if _, err := chain.InsertChain(blocks); err != nil {
		chain.Stop()
		t.Fatalf("InsertChain: %v", err)
	}
	t.Cleanup(chain.Stop)
	return chain, blocks
}

func guardRewindAnchor(header *gethtypes.Header) *state.BlockState {
	anchor := state.NewBlockState(1)
	anchor.SetOrder(1)
	anchor.SetEVM(header)
	return anchor
}

func guardRewindCandidate() *commitmentTestBlock {
	return newCommitmentTestBlock(
		ethcommon.HexToHash("0x11"),
		8,
		ethcommon.HexToHash("0x08"),
		ethcommon.HexToHash("0x09"),
	)
}

func assertGuardRewindFailure(t *testing.T, err, guardErr error) {
	t.Helper()
	if err == nil || !isEVMStateGuardRuleError(err) || !isEVMStateGuardRecoveryError(err) {
		t.Fatalf("rewind caller error = %v, want guard recovery classification", err)
	}
	if !bdagevm.IsTestUnsafeEVMRewindError(err) {
		t.Fatalf("rewind caller error = %v, want typed unsafe rewind cause", err)
	}
	if guardErr != nil {
		var recovery evmStateGuardRecoveryError
		if !errors.As(err, &recovery) || recovery.guardErr != guardErr || (errors.Unwrap(guardErr) != nil && !errors.Is(recovery.guardErr, errors.Unwrap(guardErr))) {
			t.Fatalf("rewind caller error %q lost original guard error %q", err, guardErr)
		}
	}
}

type recoveryHoldEntry struct {
	pointer *LocalEVMStateUnavailableError
	value   LocalEVMStateUnavailableError
}

type recoveryHoldSnapshot struct {
	pointer *localEVMRecoveryHolds
	pending map[ethcommon.Hash]recoveryHoldEntry
	fatal   recoveryHoldEntry
	held    bool
	reason  string
}

func seedRecoveryHold(t *testing.T, bc *BlockChain, candidate ethcommon.Hash, anchor *state.BlockState) recoveryHoldSnapshot {
	t.Helper()
	bc.chainLock.Lock()
	defer bc.chainLock.Unlock()
	pending := &LocalEVMStateUnavailableError{
		Candidate: candidate, AnchorNumber: anchor.GetEVMNumber(), AnchorHash: anchor.GetEVMHash(),
		AnchorRoot: anchor.GetEVMRoot(), Cause: errors.New("pending hold sentinel"), anchor: anchor,
	}
	fatal := *pending
	fatal.Candidate = ethcommon.HexToHash("0xfafa")
	fatal.Fatal = true
	fatal.Cause = errors.New("distinct fatal hold sentinel")
	bc.holdLocalEVMRecovery(pending)
	bc.holdLocalEVMRecovery(&fatal)
	before := recoveryHoldSnapshot{pointer: bc.localEVMRecovery.Load(), pending: make(map[ethcommon.Hash]recoveryHoldEntry)}
	for key, value := range before.pointer.pending {
		before.pending[key] = recoveryHoldEntry{pointer: value, value: *value}
	}
	before.fatal = recoveryHoldEntry{pointer: before.pointer.fatal, value: *before.pointer.fatal}
	before.held, before.reason = bc.LocalEVMRecoveryStatus()
	return before
}

func assertRecoveryHoldUnchanged(t *testing.T, bc *BlockChain, before recoveryHoldSnapshot) {
	t.Helper()
	after := bc.localEVMRecovery.Load()
	if after != before.pointer || after == nil {
		t.Fatalf("local EVM recovery snapshot pointer changed: before=%p after=%p", before.pointer, after)
	}
	if len(after.pending) != len(before.pending) {
		t.Fatalf("pending hold count changed: before=%d after=%d", len(before.pending), len(after.pending))
	}
	for key, entry := range before.pending {
		value, ok := after.pending[key]
		if !ok || value != entry.pointer || value == nil || *value != entry.value {
			t.Fatalf("pending hold identity/content changed for %s", key)
		}
	}
	if after.fatal != before.fatal.pointer || after.fatal == nil || *after.fatal != before.fatal.value {
		t.Fatal("fatal hold pointer/content changed")
	}
	held, reason := bc.LocalEVMRecoveryStatus()
	if held != before.held || reason != before.reason {
		t.Fatalf("local EVM recovery status changed: before=(%t,%q) after=(%t,%q)", before.held, before.reason, held, reason)
	}
}

func TestEVMStateGuardRewindCallerUsesRealPostSetHeadEvidence(t *testing.T) {
	tests := map[string]struct {
		mutate       func(*integrationRewindChain, *gethtypes.Block)
		bodyObserved bool
		stateRead    bool
	}{
		"post below target": {mutate: func(c *integrationRewindChain, _ *gethtypes.Block) {
			c.postHead = c.BlockChain.GetHeaderByNumber(0)
		}},
		"wrong hash": {mutate: func(c *integrationRewindChain, block *gethtypes.Block) {
			header := gethtypes.CopyHeader(block.Header())
			header.Extra = []byte("wrong hash")
			c.postHead = header
		}},
		"wrong root": {mutate: func(c *integrationRewindChain, block *gethtypes.Block) {
			header := gethtypes.CopyHeader(block.Header())
			header.Root = ethcommon.HexToHash("0xdead")
			c.postHead = header
		}},
		"missing body": {mutate: func(c *integrationRewindChain, _ *gethtypes.Block) {
			c.postBodySet = true
			c.postBody = nil
		}, bodyObserved: true},
		"missing state": {mutate: func(c *integrationRewindChain, _ *gethtypes.Block) {
			c.postStateSet = true
			c.postHasState = false
		}, bodyObserved: true, stateRead: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			chain, blocks := newGuardRewindIntegrationChain(t)
			wrapped := &integrationRewindChain{BlockChain: chain}
			target := &bdagevm.BdagChain{}
			restore := bdagevm.BindTestEVMRewindChain(target, wrapped)
			t.Cleanup(restore)
			bc := &BlockChain{bdagChain: target, evmStateGuard: newEVMStateGuard(1)}
			anchor := guardRewindAnchor(blocks[0].Header())
			candidate := guardRewindCandidate()
			originalCause := errors.New("original guard cause sentinel")
			guardErr := ruleErrorWithCause(ErrEVMStateGuard, "original guard sentinel", originalCause)
			holdBefore := seedRecoveryHold(t, bc, *candidate.GetHash(), anchor)

			if head := chain.CurrentBlock(); head == nil || head.Number.Uint64() != 2 {
				t.Fatalf("fixture head before rewind = %v, want block 2", head)
			}
			test.mutate(wrapped, blocks[0])
			err := bc.rewindEVMStateGuardRejection(anchor, candidate, guardErr)
			assertGuardRewindFailure(t, err, guardErr)
			if chain.CurrentBlock() == nil || chain.CurrentBlock().Number.Uint64() != 1 {
				t.Fatalf("real chain head after rewind = %v, want block 1", chain.CurrentBlock())
			}
			if wrapped.setHeadCalls != 1 || wrapped.lookedAfterSet != test.bodyObserved || wrapped.stateReadAfter != test.stateRead {
				t.Fatalf("post-set observations = calls:%d body:%t state:%t, want 1/%t/%t", wrapped.setHeadCalls, wrapped.lookedAfterSet, wrapped.stateReadAfter, test.bodyObserved, test.stateRead)
			}
			assertRecoveryHoldUnchanged(t, bc, holdBefore)
		})
	}
}

func TestEVMStateGuardRewindCallerRejectsInitialHeadBelowTarget(t *testing.T) {
	chain, blocks := newGuardRewindIntegrationChain(t)
	wrapped := &integrationRewindChain{BlockChain: chain}
	// Fixture setup really rewinds the local database to genesis. The recovery
	// caller must reject moving this actual head forward to its saved block1.
	if err := chain.SetHead(0); err != nil {
		t.Fatalf("prepare initial genesis head: %v", err)
	}
	if head := chain.CurrentBlock(); head == nil || head.Number.Uint64() != 0 {
		t.Fatalf("initial real head=%v, want genesis", head)
	}
	target := &bdagevm.BdagChain{}
	restore := bdagevm.BindTestEVMRewindChain(target, wrapped)
	t.Cleanup(restore)
	bc := &BlockChain{bdagChain: target, evmStateGuard: newEVMStateGuard(1)}
	anchor := guardRewindAnchor(blocks[0].Header())
	candidate := guardRewindCandidate()
	originalCause := errors.New("initial below-target cause sentinel")
	guardErr := ruleErrorWithCause(ErrEVMStateGuard, "initial below-target sentinel", originalCause)
	holdBefore := seedRecoveryHold(t, bc, *candidate.GetHash(), anchor)
	err := bc.rewindEVMStateGuardRejection(anchor, candidate, guardErr)
	assertGuardRewindFailure(t, err, guardErr)
	if wrapped.setHeadCalls != 0 || chain.CurrentBlock().Number.Uint64() != 0 || !strings.Contains(err.Error(), "below rewind target") {
		t.Fatalf("initial below-target state: calls=%d head=%v error=%v", wrapped.setHeadCalls, chain.CurrentBlock(), err)
	}
	assertRecoveryHoldUnchanged(t, bc, holdBefore)
}

func TestEVMStateGuardReorgCallerUsesRealRewindAndPreservesHold(t *testing.T) {
	chain, blocks := newGuardRewindIntegrationChain(t)
	wrapped := &integrationRewindChain{BlockChain: chain}
	target := &bdagevm.BdagChain{}
	restore := bdagevm.BindTestEVMRewindChain(target, wrapped)
	t.Cleanup(restore)
	bc := &BlockChain{bdagChain: target, evmStateGuard: newEVMStateGuard(1)}
	anchor := guardRewindAnchor(blocks[0].Header())
	candidate := guardRewindCandidate()
	holdBefore := seedRecoveryHold(t, bc, *candidate.GetHash(), anchor)
	if err := bc.rewindEVMStateGuardReorgStart(anchor, candidate); err != nil {
		t.Fatalf("exact reorg rewind: %v", err)
	}
	if wrapped.setHeadCalls != 1 || chain.CurrentBlock() == nil || chain.CurrentBlock().Number.Uint64() != 1 {
		t.Fatalf("exact reorg rewind state = calls:%d head:%v, want 1/block1", wrapped.setHeadCalls, chain.CurrentBlock())
	}
	assertRecoveryHoldUnchanged(t, bc, holdBefore)
	// The bridge serializes provider bindings for the whole installation
	// lifetime; restore before constructing the independent failure fixture.
	restore()

	chain2, blocks2 := newGuardRewindIntegrationChain(t)
	wrapped2 := &integrationRewindChain{BlockChain: chain2}
	wrapped2.postStateSet = true
	wrapped2.postHasState = false
	target2 := &bdagevm.BdagChain{}
	restore2 := bdagevm.BindTestEVMRewindChain(target2, wrapped2)
	t.Cleanup(restore2)
	bc2 := &BlockChain{bdagChain: target2, evmStateGuard: newEVMStateGuard(1)}
	anchor2 := guardRewindAnchor(blocks2[0].Header())
	candidate2 := guardRewindCandidate()
	holdBefore2 := seedRecoveryHold(t, bc2, *candidate2.GetHash(), anchor2)
	err := bc2.rewindEVMStateGuardReorgStart(anchor2, candidate2)
	assertGuardRewindFailure(t, err, nil)
	if wrapped2.setHeadCalls != 1 || !wrapped2.lookedAfterSet || !wrapped2.stateReadAfter || chain2.CurrentBlock().Number.Uint64() != 1 {
		t.Fatalf("reorg failure observations = calls:%d body:%t state:%t", wrapped2.setHeadCalls, wrapped2.lookedAfterSet, wrapped2.stateReadAfter)
	}
	assertRecoveryHoldUnchanged(t, bc2, holdBefore2)
}

func TestEVMStateGuardReorgCallerLeavesPreEnforcementFailureUnclassified(t *testing.T) {
	chain, blocks := newGuardRewindIntegrationChain(t)
	wrapped := &integrationRewindChain{BlockChain: chain}
	wrong := gethtypes.CopyHeader(blocks[0].Header())
	wrong.Extra = []byte("pre-enforcement wrong hash")
	wrapped.postHead = wrong
	target := &bdagevm.BdagChain{}
	restore := bdagevm.BindTestEVMRewindChain(target, wrapped)
	t.Cleanup(restore)
	bc := &BlockChain{bdagChain: target, evmStateGuard: newEVMStateGuard(0)}
	anchor, candidate := guardRewindAnchor(blocks[0].Header()), guardRewindCandidate()
	holdBefore := seedRecoveryHold(t, bc, *candidate.GetHash(), anchor)
	if err := bc.rewindEVMStateGuardReorgStart(anchor, candidate); err == nil || isEVMStateGuardRuleError(err) || !bdagevm.IsTestUnsafeEVMRewindError(err) {
		t.Fatalf("pre-enforcement rewind error = %v, want unclassified typed unsafe cause", err)
	}
	if wrapped.setHeadCalls != 1 || chain.CurrentBlock().Number.Uint64() != 1 {
		t.Fatalf("pre-enforcement real rewind: calls=%d head=%v", wrapped.setHeadCalls, chain.CurrentBlock())
	}
	assertRecoveryHoldUnchanged(t, bc, holdBefore)
}

func TestEVMStateGuardRewindCallerSuccessfulRewindRetainsOriginalErrorAndHold(t *testing.T) {
	chain, blocks := newGuardRewindIntegrationChain(t)
	wrapped := &integrationRewindChain{BlockChain: chain}
	target := &bdagevm.BdagChain{}
	restore := bdagevm.BindTestEVMRewindChain(target, wrapped)
	t.Cleanup(restore)
	bc := &BlockChain{bdagChain: target, evmStateGuard: newEVMStateGuard(1)}
	anchor, candidate := guardRewindAnchor(blocks[0].Header()), guardRewindCandidate()
	holdBefore := seedRecoveryHold(t, bc, *candidate.GetHash(), anchor)
	originalCause := errors.New("successful rewind original guard cause")
	guardErr := ruleErrorWithCause(ErrEVMStateGuard, "original rejection still applies", originalCause)
	err := bc.rewindEVMStateGuardRejection(anchor, candidate, guardErr)
	if err != guardErr || !errors.Is(err, originalCause) || isEVMStateGuardRecoveryError(err) {
		t.Fatalf("successful rewind returned %v, want original guard error and cause", err)
	}
	if wrapped.setHeadCalls != 1 || chain.CurrentBlock().Number.Uint64() != 1 || !wrapped.lookedAfterSet || !wrapped.stateReadAfter {
		t.Fatalf("successful real rewind: calls=%d head=%v body=%t state=%t", wrapped.setHeadCalls, chain.CurrentBlock(), wrapped.lookedAfterSet, wrapped.stateReadAfter)
	}
	assertRecoveryHoldUnchanged(t, bc, holdBefore)
}
