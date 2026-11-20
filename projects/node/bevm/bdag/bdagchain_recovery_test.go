package bdag

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	corestate "github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/ethereum/go-ethereum/common"
	ethash "github.com/ethereum/go-ethereum/consensus/ethash"
	ethcore "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
)

func TestRecoverableEVMStateRejectsUnorderedSentinel(t *testing.T) {
	unordered := corestate.NewBlockState(1)
	unordered.SetOrder(uint64(blockdag.MaxBlockOrder))
	setTestEVMState(unordered, 42)

	if isRecoverableEVMState(unordered) {
		t.Fatal("unordered sentinel block state was treated as recoverable")
	}

	ordered := corestate.NewBlockState(2)
	ordered.SetOrder(42)
	setTestEVMState(ordered, 42)

	if !isRecoverableEVMState(ordered) {
		t.Fatal("ordered block state was not treated as recoverable")
	}
}

func TestValidateEVMHeadMatchesState(t *testing.T) {
	header := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("recorded"),
	}
	state := corestate.NewBlockState(13)
	state.SetOrder(100)
	state.SetEVM(header)

	if err := validateEVMHeadMatchesState(state, header); err != nil {
		t.Fatalf("matching EVM head was rejected: %v", err)
	}

	wrongHash := types.CopyHeader(header)
	wrongHash.Extra = []byte("different")
	if err := validateEVMHeadMatchesState(state, wrongHash); err == nil {
		t.Fatal("same-number EVM head with mismatched hash was accepted")
	}

	wrongRoot := types.CopyHeader(header)
	wrongRoot.Root = common.HexToHash("0x02")
	if err := validateEVMHeadMatchesState(state, wrongRoot); err == nil {
		t.Fatal("EVM head with mismatched root was accepted")
	}
}

func TestValidateSafeEVMRewindTargetRejectsHeadBelowTarget(t *testing.T) {
	state := corestate.NewBlockState(13)
	state.SetOrder(100)
	setTestEVMState(state, 100)
	header := &types.Header{Number: new(big.Int).SetUint64(99), Root: common.HexToHash("0x01")}

	if err := validateSafeEVMRewindTarget(header, state, nil, nil); !isUnsafeEVMRewindError(err) {
		t.Fatalf("head below target returned %v, want typed unsafe rewind error", err)
	}
}

func TestValidateExactEVMRewindPostconditionRequiresBodyAndState(t *testing.T) {
	header := &types.Header{Number: new(big.Int).SetUint64(7), Root: common.HexToHash("0x07"), Extra: []byte("target")}
	state := corestate.NewBlockState(7)
	state.SetOrder(7)
	state.SetEVM(header)
	block := types.NewBlockWithHeader(header)

	for name, tc := range map[string]struct {
		getBlock func(common.Hash, uint64) *types.Block
		hasState func(common.Hash) bool
	}{
		"missing body":  {getBlock: func(common.Hash, uint64) *types.Block { return nil }, hasState: func(common.Hash) bool { return true }},
		"missing state": {getBlock: func(common.Hash, uint64) *types.Block { return block }, hasState: func(common.Hash) bool { return false }},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateExactEVMRewindPostcondition(state, header, tc.getBlock, tc.hasState)
			if !isUnsafeEVMRewindError(err) {
				t.Fatalf("postcondition error = %v, want typed unsafe rewind error", err)
			}
		})
	}

	wrongHash := types.CopyHeader(header)
	wrongHash.Extra = []byte("wrong hash")
	if err := validateExactEVMRewindPostcondition(state, wrongHash, func(common.Hash, uint64) *types.Block { return block }, func(common.Hash) bool { return true }); !isUnsafeEVMRewindError(err) {
		t.Fatalf("wrong hash postcondition error = %v, want typed unsafe rewind error", err)
	}
	wrongRoot := types.CopyHeader(header)
	wrongRoot.Root = common.HexToHash("0x08")
	if err := validateExactEVMRewindPostcondition(state, wrongRoot, func(common.Hash, uint64) *types.Block { return block }, func(common.Hash) bool { return true }); !isUnsafeEVMRewindError(err) {
		t.Fatalf("wrong root postcondition error = %v, want typed unsafe rewind error", err)
	}

	if err := validateExactEVMRewindPostcondition(state, header, func(common.Hash, uint64) *types.Block { return block }, func(common.Hash) bool { return true }); err != nil {
		t.Fatalf("complete exact postcondition rejected: %v", err)
	}
}

type countedRewindChain struct {
	*ethcore.BlockChain
	setHeadCalls int
	setHeadErr   error
	noOpSetHead  bool
	postSet      bool
	postHead     *types.Header
	postBodySet  bool
	postBody     *types.Block
	postStateSet bool
	postHasState bool
	currentHead  *types.Header
}

func (c *countedRewindChain) SetHead(number uint64) error {
	c.setHeadCalls++
	if c.setHeadErr != nil {
		return c.setHeadErr
	}
	if c.noOpSetHead {
		c.postSet = true
		return nil
	}
	err := c.BlockChain.SetHead(number)
	c.postSet = true
	return err
}

func (c *countedRewindChain) CurrentBlock() *types.Header {
	if c.currentHead != nil {
		return c.currentHead
	}
	if c.postSet && c.postHead != nil {
		return c.postHead
	}
	return c.BlockChain.CurrentBlock()
}

func TestRewindToActualCallerRejectsHeadBelowTarget(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	target := corestate.NewBlockState(1)
	target.SetOrder(1)
	target.SetEVM(blocks[1].Header())
	wrapped := &countedRewindChain{
		BlockChain:  chain,
		currentHead: types.CopyHeader(blocks[0].Header()),
	}
	previousProvider := evmRewindChainProvider
	evmRewindChainProvider = func(*BdagChain) evmRewindChain { return wrapped }
	t.Cleanup(func() { evmRewindChainProvider = previousProvider })

	b := &BdagChain{}
	err := b.RewindTo(target)
	if !isUnsafeEVMRewindError(err) {
		t.Fatalf("RewindTo below-target error = %v, want typed unsafe rewind error", err)
	}
	if wrapped.setHeadCalls != 0 {
		t.Fatalf("below-target RewindTo SetHead calls = %d, want 0", wrapped.setHeadCalls)
	}
	if head := chain.CurrentBlock(); head == nil || head.Number.Uint64() != blocks[1].Header().Number.Uint64() {
		t.Fatalf("below-target RewindTo changed underlying head: %v", head)
	}
}

func (c *countedRewindChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	if c.postSet && c.postBodySet {
		return c.postBody
	}
	return c.BlockChain.GetBlock(hash, number)
}

func (c *countedRewindChain) HasState(root common.Hash) bool {
	if c.postSet && c.postStateSet {
		return c.postHasState
	}
	return c.BlockChain.HasState(root)
}

func newRewindTestChain(t *testing.T) (*ethcore.BlockChain, []*types.Block) {
	t.Helper()
	engine := ethash.NewFaker()
	genesis := &ethcore.Genesis{Config: params.AllEthashProtocolChanges, GasLimit: 30_000_000}
	db := rawdb.NewMemoryDatabase()
	chain, err := ethcore.NewBlockChain(db, nil, genesis, nil, engine, vm.Config{}, nil)
	if err != nil {
		t.Fatalf("NewBlockChain() error = %v", err)
	}
	_, blocks, _ := ethcore.GenerateChainWithGenesis(genesis, engine, 2, func(int, *ethcore.BlockGen) {})
	if _, err := chain.InsertChain(blocks); err != nil {
		chain.Stop()
		t.Fatalf("InsertChain() error = %v", err)
	}
	t.Cleanup(chain.Stop)
	return chain, blocks
}

func TestRewindEVMToChainUsesExactLandingAndBodyState(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	target := corestate.NewBlockState(1)
	target.SetOrder(1)
	target.SetEVM(blocks[0].Header())

	wrapped := &countedRewindChain{BlockChain: chain}
	if err := rewindEVMToChain(target, wrapped); err != nil {
		t.Fatalf("rewindEVMToChain() error = %v", err)
	}
	if wrapped.setHeadCalls != 1 {
		t.Fatalf("SetHead calls = %d, want 1", wrapped.setHeadCalls)
	}
	head := chain.CurrentBlock()
	if err := validateExactEVMRewindPostcondition(target, head, chain.GetBlock, chain.HasState); err != nil {
		t.Fatalf("post-rewind exact observation failed: %v", err)
	}
}

func TestRewindEVMToChainRejectsBelowTargetWithoutSetHead(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	targetHeader := types.CopyHeader(blocks[1].Header())
	targetHeader.Number = new(big.Int).SetUint64(3)
	target := corestate.NewBlockState(2)
	target.SetOrder(2)
	target.SetEVM(targetHeader)
	wrapped := &countedRewindChain{BlockChain: chain}

	err := rewindEVMToChain(target, wrapped)
	if !isUnsafeEVMRewindError(err) {
		t.Fatalf("below-target error = %v, want typed unsafe rewind error", err)
	}
	if wrapped.setHeadCalls != 0 {
		t.Fatalf("SetHead calls = %d, want 0", wrapped.setHeadCalls)
	}
}

func TestRewindEVMToChainRejectsUnchangedAboveTarget(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	target := corestate.NewBlockState(1)
	target.SetOrder(1)
	target.SetEVM(blocks[0].Header())
	wrapped := &countedRewindChain{BlockChain: chain, noOpSetHead: true}

	err := rewindEVMToChain(target, wrapped)
	if !isUnsafeEVMRewindError(err) {
		t.Fatalf("unchanged-above-target error = %v, want typed unsafe rewind error", err)
	}
	if wrapped.setHeadCalls != 1 {
		t.Fatalf("SetHead calls = %d, want 1", wrapped.setHeadCalls)
	}
}

func TestRewindEVMToChainEqualHeadRequiresBodyAndState(t *testing.T) {
	chain, _ := newRewindTestChain(t)
	current := chain.CurrentBlock()
	target := corestate.NewBlockState(2)
	target.SetOrder(2)
	target.SetEVM(current)

	for name, configure := range map[string]func(*countedRewindChain){
		"missing body": func(w *countedRewindChain) {
			w.postBodySet = true
			w.postBody = nil
		},
		"missing state": func(w *countedRewindChain) {
			w.postStateSet = true
			w.postHasState = false
		},
		"exact": func(*countedRewindChain) {},
	} {
		t.Run(name, func(t *testing.T) {
			wrapped := &countedRewindChain{BlockChain: chain, postSet: true}
			configure(wrapped)
			err := rewindEVMToChain(target, wrapped)
			if name == "exact" {
				if err != nil {
					t.Fatalf("equal-head exact landing failed: %v", err)
				}
			} else if !isUnsafeEVMRewindError(err) {
				t.Fatalf("equal-head %s error = %v, want typed unsafe rewind error", name, err)
			}
			if wrapped.setHeadCalls != 0 {
				t.Fatalf("equal-head %s SetHead calls = %d, want 0", name, wrapped.setHeadCalls)
			}
		})
	}
}

func TestRewindEVMToChainPostSetHeadFailuresRemainTyped(t *testing.T) {
	for name, configure := range map[string]func(*countedRewindChain){
		"below target": func(w *countedRewindChain) {
			w.postHead = w.BlockChain.Genesis().Header()
		},
		"wrong hash": func(w *countedRewindChain) {
			w.postHead = types.CopyHeader(w.BlockChain.GetBlockByNumber(1).Header())
			w.postHead.Extra = []byte("wrong hash")
		},
		"wrong root": func(w *countedRewindChain) {
			w.postHead = types.CopyHeader(w.BlockChain.GetBlockByNumber(1).Header())
			w.postHead.Root = common.HexToHash("0xbeef")
		},
		"missing body": func(w *countedRewindChain) {
			w.postBodySet = true
			w.postBody = nil
		},
		"missing state": func(w *countedRewindChain) {
			w.postStateSet = true
			w.postHasState = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			chain, blocks := newRewindTestChain(t)
			target := corestate.NewBlockState(1)
			target.SetOrder(1)
			target.SetEVM(blocks[0].Header())
			wrapped := &countedRewindChain{BlockChain: chain}
			configure(wrapped)
			err := rewindEVMToChain(target, wrapped)
			if !isUnsafeEVMRewindError(err) {
				t.Fatalf("post-SetHead %s error = %v, want typed unsafe rewind error", name, err)
			}
			if wrapped.setHeadCalls != 1 {
				t.Fatalf("post-SetHead %s SetHead calls = %d, want 1", name, wrapped.setHeadCalls)
			}
		})
	}
}

func TestRewindEVMToChainDepthRefusalAvoidsSetHead(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	target := corestate.NewBlockState(1)
	target.SetOrder(1)
	target.SetEVM(blocks[0].Header())
	current := types.CopyHeader(chain.CurrentBlock())
	current.Number = new(big.Int).SetUint64(target.GetEVMNumber() + params.FullImmutabilityThreshold + 1)
	wrapped := &countedRewindChain{BlockChain: chain, postSet: true, postHead: current}

	err := rewindEVMToChain(target, wrapped)
	if !isUnsafeEVMRewindError(err) {
		t.Fatalf("depth refusal error = %v, want typed unsafe rewind error", err)
	}
	if wrapped.setHeadCalls != 0 {
		t.Fatalf("depth refusal SetHead calls = %d, want 0", wrapped.setHeadCalls)
	}
}

func TestPrepareEnvironmentRereadsHeadAfterRewindFailure(t *testing.T) {
	stale := &types.Header{Number: new(big.Int).SetUint64(9), Extra: []byte("stale")}
	fresh := &types.Header{Number: new(big.Int).SetUint64(4), Extra: []byte("fresh")}
	rewindErr := newUnsafeEVMRewindError("postcondition mismatch")

	got, err := rereadEVMHeadAfterRewindFailure(rewindErr, func() *types.Header { return fresh })
	if err != nil {
		t.Fatalf("reread after rewind failure returned error: %v", err)
	}
	if got != fresh || got == stale {
		t.Fatalf("reread returned stale head %v, want fresh head %v", got, fresh)
	}

	if _, err := rereadEVMHeadAfterRewindFailure(rewindErr, func() *types.Header { return nil }); err == nil || !strings.Contains(err.Error(), "after rewind failure") {
		t.Fatalf("missing reread head error = %v, want bounded rewind failure", err)
	}
}

func TestPrepareEnvironmentUsesActualRewindCallerPostSetHeadEvidence(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	target := corestate.NewBlockState(0)
	target.SetOrder(0)
	target.SetEVM(blocks[0].Header())
	before := chain.CurrentBlock()
	if before == nil || before.Number.Uint64() != blocks[1].Header().Number.Uint64() {
		t.Fatalf("test setup current head = %#v, want block 2", before)
	}

	// Exercise BdagChain.PrepareEnvironment -> BdagChain.RewindTo -> the
	// production rewind postcondition. SetHead changes the real chain to the
	// target, then the fresh caller read reports a wrong identity, so stale
	// pre-rewind evidence cannot be accepted as success.
	postHead := types.CopyHeader(blocks[0].Header())
	postHead.Extra = []byte("post-sethead wrong identity")
	wrapped := &countedRewindChain{
		BlockChain: chain,
		postHead:   postHead,
	}
	previousProvider := evmRewindChainProvider
	evmRewindChainProvider = func(*BdagChain) evmRewindChain { return wrapped }
	t.Cleanup(func() { evmRewindChainProvider = previousProvider })
	memo := &evmRecoveryFailureMemo{err: errors.New("existing recovery hold")}
	b := &BdagChain{lastEVMRecoveryFailure: memo}
	_, err := b.PrepareEnvironment(target)
	if !isUnsafeEVMRewindError(err) {
		t.Fatalf("prepareEnvironment post-SetHead error = %v, want typed unsafe rewind error", err)
	}
	if wrapped.setHeadCalls != 1 {
		t.Fatalf("prepareEnvironment SetHead calls = %d, want 1", wrapped.setHeadCalls)
	}
	after := chain.CurrentBlock()
	if after == nil || before.Hash() == after.Hash() || after.Number.Uint64() != blocks[0].Header().Number.Uint64() {
		t.Fatalf("SetHead did not change the real chain head: before=%v after=%v", before, after)
	}
	if head := wrapped.CurrentBlock(); head == nil || head.Hash() == blocks[0].Header().Hash() || !strings.Contains(err.Error(), head.Hash().String()) {
		t.Fatalf("prepareEnvironment did not use fresh post-SetHead identity: head=%v err=%v", head, err)
	}
	if b.lastEVMRecoveryFailure != memo {
		t.Fatal("prepareEnvironment failure cleared existing recovery memo")
	}
}

func TestRewindEVMToChainPreservesSetHeadError(t *testing.T) {
	chain, blocks := newRewindTestChain(t)
	target := corestate.NewBlockState(1)
	target.SetOrder(1)
	target.SetEVM(blocks[0].Header())
	wantErr := errors.New("set head failed")
	wrapped := &countedRewindChain{BlockChain: chain, setHeadErr: wantErr}

	if err := rewindEVMToChain(target, wrapped); !errors.Is(err, wantErr) {
		t.Fatalf("SetHead error = %v, want %v", err, wantErr)
	}
}

func TestRecoverableEVMStateAtOrderAcceptsMismatchedEmbeddedSentinel(t *testing.T) {
	unordered := corestate.NewBlockState(1)
	unordered.SetOrder(uint64(blockdag.MaxBlockOrder))
	setTestEVMState(unordered, 42)

	if !isRecoverableEVMStateAtOrder(unordered, 42) {
		t.Fatal("state with sentinel embedded order was not recoverable at a valid requested order")
	}

	if isRecoverableEVMStateAtOrder(unordered, uint64(blockdag.MaxBlockOrder)) {
		t.Fatal("sentinel requested order was treated as recoverable")
	}
}

func TestRecoverableEVMFallbackRejectsInvalidState(t *testing.T) {
	state := corestate.NewBlockState(3)
	state.SetOrder(43)
	setTestEVMState(state, 43)

	if !isRecoverableEVMFallbackState(state) {
		t.Fatal("valid ordered state was not accepted as fallback")
	}

	state.Invalid()

	if isRecoverableEVMFallbackState(state) {
		t.Fatal("invalid state was accepted as fallback")
	}
}

func TestRecoverableEVMStateRejectsEmptyEVMCommitment(t *testing.T) {
	state := corestate.NewBlockState(18)
	state.SetOrder(43)

	if isRecoverableEVMState(state) {
		t.Fatal("empty EVM state was accepted as recoverable")
	}
	if isRecoverableEVMStateAtOrder(state, 43) {
		t.Fatal("empty EVM state was accepted as recoverable at requested order")
	}
	if isRecoverableEVMAnchorCandidate(state, 100) {
		t.Fatal("empty EVM state was accepted as a local recovery anchor")
	}
}

func TestRecoverableEVMAnchorCandidateNeverUsesFutureState(t *testing.T) {
	state := corestate.NewBlockState(4)
	state.SetOrder(44)
	state.SetEVM(&types.Header{Number: new(big.Int).SetUint64(101)})

	if isRecoverableEVMAnchorCandidate(state, 100) {
		t.Fatal("future EVM state was accepted as a local recovery anchor")
	}

	if !isRecoverableEVMAnchorCandidate(state, 101) {
		t.Fatal("current EVM state was rejected as a local recovery anchor")
	}

	state.SetEVM(&types.Header{Number: new(big.Int).SetUint64(99)})
	if !isRecoverableEVMAnchorCandidate(state, 100) {
		t.Fatal("past EVM state was rejected as a local recovery anchor")
	}

	state.Invalid()
	if isRecoverableEVMAnchorCandidate(state, 100) {
		t.Fatal("invalid state was accepted as a local recovery anchor")
	}
}

func TestRecoverableEVMAnchorCandidateAtOrderUsesRequestedOrder(t *testing.T) {
	state := corestate.NewBlockState(5)
	state.SetOrder(uint64(blockdag.MaxBlockOrder))
	state.SetEVM(&types.Header{Number: new(big.Int).SetUint64(100)})

	if !isRecoverableEVMAnchorCandidateAtOrder(state, 45, 100) {
		t.Fatal("state with sentinel embedded order was not accepted at a valid requested order")
	}

	if isRecoverableEVMAnchorCandidateAtOrder(state, uint64(blockdag.MaxBlockOrder), 100) {
		t.Fatal("sentinel requested order was accepted as a local recovery anchor")
	}

	if isRecoverableEVMAnchorCandidateAtOrder(state, 45, 99) {
		t.Fatal("future EVM state was accepted as a local recovery anchor by requested order")
	}
}

func TestRecordedEVMParentGapDetection(t *testing.T) {
	if !hasRecordedEVMParentGap(10343221, 10343226, false) {
		t.Fatal("missing recorded parent gap was not detected")
	}

	if hasRecordedEVMParentGap(10343221, 10343222, false) {
		t.Fatal("next sequential EVM height was treated as a parent gap")
	}

	if hasRecordedEVMParentGap(10343221, 10343226, true) {
		t.Fatal("existing recorded parent was treated as a parent gap")
	}
}

func TestSequentialEVMRebuildAvoidsRecoveryRewind(t *testing.T) {
	current := common.HexToHash("0x01")
	target := common.HexToHash("0x02")

	if !shouldForceSequentialEVMRebuild(10343225, current, 10343223, target) {
		t.Fatal("recorded target behind current recovery head should force sequential rebuild")
	}

	if !shouldForceSequentialEVMRebuild(10343225, current, 10343225, target) {
		t.Fatal("recorded target with mismatched hash at current head should force sequential rebuild")
	}

	if shouldForceSequentialEVMRebuild(10343225, current, 10343225, current) {
		t.Fatal("matching current head should not force rebuild")
	}

	if shouldForceSequentialEVMRebuild(10343225, current, 10343226, target) {
		t.Fatal("next recorded target should not force sequential rebuild")
	}
}

func TestNativeEVMRebuildAnchorRequiresCurrentHeaderMatch(t *testing.T) {
	header := &types.Header{Number: new(big.Int).SetUint64(100)}
	targetHeader := &types.Header{Number: new(big.Int).SetUint64(200)}

	state := corestate.NewBlockState(6)
	state.SetOrder(60)
	state.SetEVM(header)

	target := corestate.NewBlockState(7)
	target.SetOrder(200)
	target.SetEVM(targetHeader)

	if !isNativeEVMRebuildAnchor(state, 60, header, target) {
		t.Fatal("matching native state was not accepted as an EVM rebuild anchor")
	}

	mismatched := corestate.NewBlockState(8)
	mismatched.SetOrder(60)
	mismatched.SetEVM(&types.Header{Number: new(big.Int).SetUint64(100), Extra: []byte("different")})

	if isNativeEVMRebuildAnchor(mismatched, 60, header, target) {
		t.Fatal("native state with mismatched EVM hash was accepted as an anchor")
	}
}

func TestNativeEVMRebuildAnchorRejectsFutureTarget(t *testing.T) {
	header := &types.Header{Number: new(big.Int).SetUint64(100)}

	state := corestate.NewBlockState(9)
	state.SetOrder(100)
	state.SetEVM(header)

	target := corestate.NewBlockState(10)
	target.SetOrder(99)
	target.SetEVM(&types.Header{Number: new(big.Int).SetUint64(99)})

	if isNativeEVMRebuildAnchor(state, 100, header, target) {
		t.Fatal("anchor ahead of target was accepted")
	}

	state.Invalid()
	if isNativeEVMRebuildAnchor(state, 100, header, state) {
		t.Fatal("invalid native state was accepted as an anchor")
	}
}

func TestCurrentEVMGenesisRequiresExactGenesisHead(t *testing.T) {
	genesis := &types.Header{
		Number: new(big.Int).SetUint64(0),
		Extra:  []byte("genesis"),
	}

	if !isCurrentEVMGenesis(genesis, genesis) {
		t.Fatal("exact genesis head was not accepted")
	}

	liveHead := &types.Header{
		Number: new(big.Int).SetUint64(1),
		Extra:  []byte("live"),
	}
	if isCurrentEVMGenesis(liveHead, genesis) {
		t.Fatal("live EVM head was accepted as genesis")
	}

	differentGenesis := &types.Header{
		Number: new(big.Int).SetUint64(0),
		Extra:  []byte("different-genesis"),
	}
	if isCurrentEVMGenesis(differentGenesis, genesis) {
		t.Fatal("different zero-height EVM header was accepted as genesis")
	}
}

func TestRecoveryScanSkipLoggingIsThrottled(t *testing.T) {
	for _, count := range []uint64{1, 10, 100, 10000, 20000} {
		if !shouldLogRecoveryScanSkip(count) {
			t.Fatalf("expected skip count %d to be logged", count)
		}
	}

	for _, count := range []uint64{0, 2, 9, 11, 99, 101, 9999, 10001} {
		if shouldLogRecoveryScanSkip(count) {
			t.Fatalf("expected skip count %d to be suppressed", count)
		}
	}
}

func TestBetterEVMRecoveryAnchorPrefersClosestStatefulLiveAnchor(t *testing.T) {
	best := corestate.NewBlockState(21)
	best.SetOrder(100)
	best.SetEVM(&types.Header{Number: new(big.Int).SetUint64(90)})

	closer := corestate.NewBlockState(22)
	closer.SetOrder(110)
	closer.SetEVM(&types.Header{Number: new(big.Int).SetUint64(99)})

	if !isBetterEVMRecoveryAnchor(closer, 110, best, 100, 120) {
		t.Fatal("closer EVM height was not preferred as the recovery anchor")
	}

	older := corestate.NewBlockState(23)
	older.SetOrder(120)
	older.SetEVM(&types.Header{Number: new(big.Int).SetUint64(80)})
	if isBetterEVMRecoveryAnchor(older, 120, best, 100, 120) {
		t.Fatal("older EVM height was preferred over a closer anchor")
	}
}

func TestBetterEVMRecoveryAnchorRejectsGenesisForLiveHead(t *testing.T) {
	genesis := corestate.NewBlockState(24)
	genesis.SetOrder(0)
	genesis.SetEVM(&types.Header{Number: new(big.Int).SetUint64(0), Root: common.HexToHash("0x01")})

	if isBetterEVMRecoveryAnchor(genesis, 0, nil, 0, 120) {
		t.Fatal("genesis was accepted as a recovery anchor for a live EVM head")
	}

	if !isBetterEVMRecoveryAnchor(genesis, 0, nil, 0, 0) {
		t.Fatal("genesis was rejected as the bootstrap anchor for a genesis EVM head")
	}
}

func TestRecoveryFailureMemoRequiresExactCurrentAndTarget(t *testing.T) {
	current := &types.Header{
		Number: new(big.Int).SetUint64(120),
		Extra:  []byte("current"),
	}
	targetHeader := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Extra:  []byte("target"),
	}
	target := corestate.NewBlockState(25)
	target.SetOrder(1000)
	target.SetEVM(targetHeader)

	memo := &evmRecoveryFailureMemo{
		currentNumber: current.Number.Uint64(),
		currentHash:   current.Hash(),
		targetOrder:   target.GetOrder(),
		targetNumber:  target.GetEVMNumber(),
		targetHash:    target.GetEVMHash(),
		err:           newUnsafeEVMRewindError("test"),
	}

	if !recoveryFailureMemoMatches(memo, current, target) {
		t.Fatal("exact current/target recovery miss did not match memo")
	}

	changedTargetHeader := &types.Header{
		Number: new(big.Int).SetUint64(101),
		Extra:  []byte("target"),
	}
	changedTarget := corestate.NewBlockState(26)
	changedTarget.SetOrder(1001)
	changedTarget.SetEVM(changedTargetHeader)
	if recoveryFailureMemoMatches(memo, current, changedTarget) {
		t.Fatal("changed target incorrectly matched recovery failure memo")
	}

	changedCurrent := &types.Header{
		Number: new(big.Int).SetUint64(121),
		Extra:  []byte("current"),
	}
	if recoveryFailureMemoMatches(memo, changedCurrent, target) {
		t.Fatal("changed current head incorrectly matched recovery failure memo")
	}
}

func TestNativeEVMRebuildFromGenesisLimit(t *testing.T) {
	if nativeEVMRebuildFromGenesisExceedsLimit(0, maxNativeEVMRebuildFromGenesis) {
		t.Fatal("genesis rebuild at the safe replay limit was rejected")
	}

	if !nativeEVMRebuildFromGenesisExceedsLimit(0, maxNativeEVMRebuildFromGenesis+1) {
		t.Fatal("genesis rebuild beyond the safe replay limit was accepted")
	}

	if nativeEVMRebuildFromGenesisExceedsLimit(1, maxNativeEVMRebuildFromGenesis+1) {
		t.Fatal("non-genesis rebuild anchor was rejected by the genesis replay limit")
	}
}

func TestStrictRecoveredEVMStateAcceptsExactRecordedHeader(t *testing.T) {
	header := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("recorded"),
	}
	state := corestate.NewBlockState(11)
	state.SetOrder(100)
	state.SetEVM(header)

	if err := validateStrictRecoveredEVMState(100, state, header); err != nil {
		t.Fatalf("exact recovered EVM header was rejected: %v", err)
	}
}

func TestStrictRecoveredEVMStateRejectsMismatchWithoutMutatingState(t *testing.T) {
	recorded := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("recorded"),
	}
	rebuilt := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("rebuilt"),
	}
	state := corestate.NewBlockState(12)
	state.SetOrder(100)
	state.SetEVM(recorded)
	recordedHash := state.GetEVMHash()

	err := validateStrictRecoveredEVMState(100, state, rebuilt)
	if err == nil {
		t.Fatal("mismatched recovered EVM header was accepted")
	}
	if !strings.Contains(err.Error(), "strict EVM recovery mismatch") {
		t.Fatalf("unexpected strict recovery error: %v", err)
	}
	if state.GetEVMHash() != recordedHash {
		t.Fatal("strict recovery mismatch mutated recorded EVM hash")
	}
}

func TestSafeEVMRewindTargetAcceptsExactStatefulTarget(t *testing.T) {
	current := &types.Header{Number: new(big.Int).SetUint64(120)}
	targetHeader := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
		Extra:  []byte("target"),
	}
	target := corestate.NewBlockState(13)
	target.SetOrder(100)
	target.SetEVM(targetHeader)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(hash common.Hash, number uint64) *types.Header {
			if hash == targetHeader.Hash() && number == targetHeader.Number.Uint64() {
				return targetHeader
			}
			return nil
		},
		func(root common.Hash) bool {
			return root == targetHeader.Root
		},
	)
	if err != nil {
		t.Fatalf("stateful exact EVM rewind target was rejected: %v", err)
	}
}

func TestEVMSetHeadDepthRejectsDeepHeuristicRewind(t *testing.T) {
	err := validateEVMSetHeadDepth(1_000_000, 1)
	if err == nil {
		t.Fatal("deep EVM SetHead rewind was accepted")
	}
	if !strings.Contains(err.Error(), "exceeds state search threshold") {
		t.Fatalf("unexpected deep rewind error: %v", err)
	}
}

func TestEVMSetHeadDepthAcceptsShallowRewind(t *testing.T) {
	if err := validateEVMSetHeadDepth(100, 99); err != nil {
		t.Fatalf("shallow EVM SetHead rewind was rejected: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsEmptyTargetState(t *testing.T) {
	current := &types.Header{Number: new(big.Int).SetUint64(120)}
	target := corestate.NewBlockState(19)
	target.SetOrder(100)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(common.Hash, uint64) *types.Header { return nil },
		func(common.Hash) bool { return false },
	)
	if err == nil || !strings.Contains(err.Error(), "target EVM state is empty") {
		t.Fatalf("empty rewind target state was not rejected with the expected error: %v", err)
	}
}

func TestSafeEVMRewindTargetAcceptsMatchingCurrentHead(t *testing.T) {
	current := &types.Header{
		Number: new(big.Int).SetUint64(120),
		Root:   common.HexToHash("0x120"),
		Extra:  []byte("current"),
	}
	target := corestate.NewBlockState(14)
	target.SetOrder(120)
	target.SetEVM(current)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(common.Hash, uint64) *types.Header { return nil },
		func(root common.Hash) bool { return root == current.Root },
	)
	if err != nil {
		t.Fatalf("matching current head was rejected: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsMatchingCurrentHeadWithoutState(t *testing.T) {
	current := &types.Header{
		Number: new(big.Int).SetUint64(120),
		Root:   common.HexToHash("0x120"),
		Extra:  []byte("current"),
	}
	target := corestate.NewBlockState(20)
	target.SetOrder(120)
	target.SetEVM(current)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(common.Hash, uint64) *types.Header { return nil },
		func(common.Hash) bool { return false },
	)
	if err == nil || !strings.Contains(err.Error(), "current EVM state is missing") {
		t.Fatalf("missing current state was not rejected with the expected error: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsSameHeightHashMismatch(t *testing.T) {
	current := &types.Header{
		Number: new(big.Int).SetUint64(120),
		Root:   common.HexToHash("0x120"),
		Extra:  []byte("current"),
	}
	targetHeader := &types.Header{
		Number: new(big.Int).SetUint64(120),
		Root:   common.HexToHash("0x120"),
		Extra:  []byte("different"),
	}
	target := corestate.NewBlockState(15)
	target.SetOrder(120)
	target.SetEVM(targetHeader)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(common.Hash, uint64) *types.Header { return nil },
		func(common.Hash) bool { return false },
	)
	if err == nil || !strings.Contains(err.Error(), "current EVM head hash mismatch") {
		t.Fatalf("same-height hash mismatch was not rejected with the expected error: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsGenesisTargetFromLiveHead(t *testing.T) {
	current := &types.Header{Number: new(big.Int).SetUint64(120)}
	targetHeader := &types.Header{Number: new(big.Int).SetUint64(0), Root: common.HexToHash("0x01")}
	target := corestate.NewBlockState(16)
	target.SetOrder(0)
	target.SetEVM(targetHeader)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(common.Hash, uint64) *types.Header { return targetHeader },
		func(common.Hash) bool { return true },
	)
	if err == nil || !strings.Contains(err.Error(), "rewound to genesis") {
		t.Fatalf("genesis rewind was not rejected with the expected error: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsMissingTargetHeader(t *testing.T) {
	current := &types.Header{Number: new(big.Int).SetUint64(120)}
	targetHeader := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
		Extra:  []byte("target"),
	}
	target := corestate.NewBlockState(17)
	target.SetOrder(100)
	target.SetEVM(targetHeader)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(common.Hash, uint64) *types.Header { return nil },
		func(common.Hash) bool { return true },
	)
	if err == nil || !strings.Contains(err.Error(), "target EVM header is missing") {
		t.Fatalf("missing target header was not rejected with the expected error: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsHeaderHashMismatch(t *testing.T) {
	current := &types.Header{Number: new(big.Int).SetUint64(120)}
	targetHeader := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
		Extra:  []byte("target"),
	}
	target := corestate.NewBlockState(19)
	target.SetOrder(100)
	target.SetEVM(targetHeader)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(hash common.Hash, number uint64) *types.Header {
			if hash == targetHeader.Hash() && number == targetHeader.Number.Uint64() {
				return &types.Header{
					Number: new(big.Int).SetUint64(number),
					Root:   common.HexToHash("0x200"),
					Extra:  targetHeader.Extra,
				}
			}
			return nil
		},
		func(common.Hash) bool { return true },
	)
	if err == nil || !strings.Contains(err.Error(), "target EVM header hash mismatch") {
		t.Fatalf("header hash mismatch was not rejected with the expected error: %v", err)
	}
}

func TestSafeEVMRewindTargetRejectsMissingTargetState(t *testing.T) {
	current := &types.Header{Number: new(big.Int).SetUint64(120)}
	targetHeader := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
		Extra:  []byte("target"),
	}
	target := corestate.NewBlockState(18)
	target.SetOrder(100)
	target.SetEVM(targetHeader)

	err := validateSafeEVMRewindTarget(
		current,
		target,
		func(hash common.Hash, number uint64) *types.Header {
			if hash == targetHeader.Hash() && number == targetHeader.Number.Uint64() {
				return targetHeader
			}
			return nil
		},
		func(common.Hash) bool { return false },
	)
	if err == nil || !strings.Contains(err.Error(), "target EVM state is missing") {
		t.Fatalf("missing target state was not rejected with the expected error: %v", err)
	}
}

func TestValidateEVMRewindTargetHeaderAcceptsExactHeader(t *testing.T) {
	header := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("target"),
	}
	state := corestate.NewBlockState(13)
	state.SetOrder(100)
	state.SetEVM(header)

	if err := validateEVMRewindTargetHeader(state, header); err != nil {
		t.Fatalf("exact rewind target header was rejected: %v", err)
	}
}

func TestValidateEVMRewindTargetHeaderRejectsLowerLanding(t *testing.T) {
	target := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("target"),
	}
	state := corestate.NewBlockState(14)
	state.SetOrder(100)
	state.SetEVM(target)

	genesisLanding := &types.Header{
		Number: new(big.Int).SetUint64(0),
		Root:   common.HexToHash("0x02"),
		Extra:  []byte("genesis"),
	}
	err := validateEVMRewindTargetHeader(state, genesisLanding)
	if err == nil {
		t.Fatal("rewind landing below target was accepted")
	}
	if !strings.Contains(err.Error(), "EVM rewind target header mismatch") {
		t.Fatalf("unexpected rewind validation error: %v", err)
	}
}

func TestValidateEVMRewindTargetHeaderRejectsSameNumberWrongHash(t *testing.T) {
	target := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("target"),
	}
	state := corestate.NewBlockState(15)
	state.SetOrder(100)
	state.SetEVM(target)

	wrong := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("wrong"),
	}
	if err := validateEVMRewindTargetHeader(state, wrong); err == nil {
		t.Fatal("rewind landing at wrong hash was accepted")
	}
}

func TestValidateEVMRewindTargetStateRejectsMissingState(t *testing.T) {
	target := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("target"),
	}
	state := corestate.NewBlockState(16)
	state.SetOrder(100)
	state.SetEVM(target)

	err := validateEVMRewindTargetState(state, target, false)
	if err == nil {
		t.Fatal("rewind target with missing state was accepted")
	}
	if !strings.Contains(err.Error(), "target state is unavailable") {
		t.Fatalf("unexpected missing-state error: %v", err)
	}
}

func TestValidateEVMRewindTargetStateAcceptsAvailableState(t *testing.T) {
	target := &types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x01"),
		Extra:  []byte("target"),
	}
	state := corestate.NewBlockState(17)
	state.SetOrder(100)
	state.SetEVM(target)

	if err := validateEVMRewindTargetState(state, target, true); err != nil {
		t.Fatalf("available rewind target state was rejected: %v", err)
	}
}

func TestCollectMissingEVMStatePathReturnsForwardLineage(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
		Extra:  []byte("anchor"),
	})
	middle := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x101"),
		Extra:      []byte("middle"),
	})
	target := types.NewBlockWithHeader(&types.Header{
		ParentHash: middle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0x102"),
		Extra:      []byte("target"),
	})
	blocks := map[common.Hash]*types.Block{
		anchor.Hash(): anchor,
		middle.Hash(): middle,
		target.Hash(): target,
	}

	gotAnchor, replay, err := collectMissingEVMStatePath(
		target,
		target.NumberU64(),
		target.Hash(),
		target.Root(),
		func(hash common.Hash, number uint64) *types.Block {
			block := blocks[hash]
			if block == nil || block.NumberU64() != number {
				return nil
			}
			return block
		},
		func(root common.Hash) bool { return root == anchor.Root() },
		evmLineageLimits{maxDepth: 10, maxBytes: maxAutomaticEVMReconstructionBytes},
	)
	if err != nil {
		t.Fatalf("collect missing state path: %v", err)
	}
	if gotAnchor.Hash() != anchor.Hash() {
		t.Fatalf("wrong anchor: got %s want %s", gotAnchor.Hash(), anchor.Hash())
	}
	if len(replay) != 2 || replay[0].Hash() != middle.Hash() || replay[1].Hash() != target.Hash() {
		t.Fatalf("replay lineage is not forward and contiguous: %#v", replay)
	}
}

func TestCollectMissingEVMStatePathHonorsDepthBound(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
	})
	target := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x101"),
	})
	_, _, err := collectMissingEVMStatePath(
		target,
		target.NumberU64(),
		target.Hash(),
		target.Root(),
		func(hash common.Hash, number uint64) *types.Block {
			if hash == anchor.Hash() && number == anchor.NumberU64() {
				return anchor
			}
			return nil
		},
		func(common.Hash) bool { return false },
		evmLineageLimits{maxDepth: 1, maxBytes: maxAutomaticEVMReconstructionBytes},
	)
	if err == nil || !strings.Contains(err.Error(), "within 1 blocks") {
		t.Fatalf("missing bounded-anchor error: %v", err)
	}
}

func TestCollectMissingEVMStatePathFollowsExactRecordedFork(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
		Extra:  []byte("anchor"),
	})
	canonicalMiddle := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0xc101"),
		Extra:      []byte("canonical-middle"),
	})
	canonicalTarget := types.NewBlockWithHeader(&types.Header{
		ParentHash: canonicalMiddle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0xc102"),
		Extra:      []byte("canonical-target"),
	})
	recordedMiddle := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0xf101"),
		Extra:      []byte("recorded-middle"),
	})
	recordedTarget := types.NewBlockWithHeader(&types.Header{
		ParentHash: recordedMiddle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0xf102"),
		Extra:      []byte("recorded-target"),
	})
	byHash := map[common.Hash]*types.Block{
		anchor.Hash():          anchor,
		canonicalMiddle.Hash(): canonicalMiddle,
		canonicalTarget.Hash(): canonicalTarget,
		recordedMiddle.Hash():  recordedMiddle,
		recordedTarget.Hash():  recordedTarget,
	}

	gotAnchor, replay, err := collectMissingEVMStatePath(
		recordedTarget,
		recordedTarget.NumberU64(),
		recordedTarget.Hash(),
		recordedTarget.Root(),
		func(hash common.Hash, number uint64) *types.Block {
			block := byHash[hash]
			if block == nil || block.NumberU64() != number {
				return nil
			}
			return block
		},
		func(root common.Hash) bool { return root == anchor.Root() },
		evmLineageLimits{maxDepth: 10, maxBytes: maxAutomaticEVMReconstructionBytes},
	)
	if err != nil {
		t.Fatalf("collect exact recorded fork: %v", err)
	}
	if gotAnchor.Hash() != anchor.Hash() || len(replay) != 2 {
		t.Fatalf("unexpected recorded fork lineage: anchor=%v replay=%v", gotAnchor, replay)
	}
	if replay[0].Hash() != recordedMiddle.Hash() || replay[1].Hash() != recordedTarget.Hash() {
		t.Fatalf("collector followed mutable canonical fork: got %s -> %s", replay[0].Hash(), replay[1].Hash())
	}
	canonicalByNumber := map[uint64]common.Hash{
		100: anchor.Hash(),
		101: canonicalMiddle.Hash(),
		102: canonicalTarget.Hash(),
	}
	if exactEVMLineageIsCanonical(gotAnchor, replay, func(number uint64) common.Hash { return canonicalByNumber[number] }) {
		t.Fatal("recorded fork was incorrectly classified as the mutable canonical lineage")
	}
}

func TestCollectMissingEVMStatePathHonorsByteBound(t *testing.T) {
	target := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(101),
		Root:   common.HexToHash("0x101"),
		Extra:  []byte("target-with-retained-size"),
	})
	_, _, err := collectMissingEVMStatePath(
		target,
		target.NumberU64(),
		target.Hash(),
		target.Root(),
		func(common.Hash, uint64) *types.Block { return nil },
		func(common.Hash) bool { return false },
		evmLineageLimits{maxDepth: 10, maxBytes: target.Size() - 1},
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds byte limit") {
		t.Fatalf("missing reconstruction byte-limit error: %v", err)
	}
}

func TestCollectMissingEVMStatePathHonorsInterrupt(t *testing.T) {
	target := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(101),
		Root:   common.HexToHash("0x101"),
	})
	interrupt := make(chan struct{})
	close(interrupt)
	lookups := 0
	_, _, err := collectMissingEVMStatePath(
		target,
		target.NumberU64(),
		target.Hash(),
		target.Root(),
		func(common.Hash, uint64) *types.Block {
			lookups++
			return nil
		},
		func(common.Hash) bool { return false },
		evmLineageLimits{maxDepth: 10, maxBytes: maxAutomaticEVMReconstructionBytes, interrupt: interrupt},
	)
	if !errors.Is(err, errEVMStateReconstructionInterrupted) {
		t.Fatalf("interrupted reconstruction returned %v", err)
	}
	if lookups != 0 {
		t.Fatalf("interrupted reconstruction performed %d parent lookups", lookups)
	}
}

func TestFindRecordedEVMReplayTargetStateHandlesNativeNumberGap(t *testing.T) {
	states := make(map[uint64]*corestate.BlockState)
	for order, number := range map[uint64]uint64{
		0: 0,
		1: 1,
		2: 3,
		3: 3,
		4: 4,
	} {
		state := corestate.NewBlockState(order + 100)
		state.SetOrder(order)
		state.SetEVM(&types.Header{
			Number: new(big.Int).SetUint64(number),
			Root:   common.BigToHash(new(big.Int).SetUint64(number + 1)),
			Extra:  []byte{byte(order)},
		})
		states[order] = state
	}
	getState := func(order uint64) model.BlockState { return states[order] }

	target := findRecordedEVMReplayTargetState(3, 4, getState)
	if target == nil || target.GetOrder() != 2 || target.GetEVMNumber() != 3 {
		t.Fatalf("wrong first recorded target after native number gap: %#v", target)
	}
	if missing := findRecordedEVMReplayTargetState(2, 4, getState); missing != nil {
		t.Fatalf("unrecorded EVM number resolved to native state: %#v", missing)
	}
}

func TestRecordedEVMReplayTargetUsesSuppliedImmutableState(t *testing.T) {
	preferredHeader := &types.Header{
		ParentHash: common.HexToHash("0x1111"),
		Number:     new(big.Int).SetUint64(303),
		Root:       common.HexToHash("0xaaaa"),
		Extra:      []byte("preferred native record"),
	}
	preferred := corestate.NewBlockState(1)
	preferred.SetOrder(404)
	preferred.SetEVM(preferredHeader)

	resolved, err := (&BdagChain{}).recordedEVMReplayTarget(303, preferred)
	if err != nil {
		t.Fatalf("resolve supplied recorded target: %v", err)
	}
	if resolved != preferred || resolved.GetEVMHash() != preferredHeader.Hash() || resolved.GetEVMRoot() != preferredHeader.Root {
		t.Fatalf("supplied immutable target changed during resolution: got=%v", resolved)
	}
}

func TestRecordedNativeEVMImportRejectsCompetingCandidateBeforeStaging(t *testing.T) {
	recordedHeader := &types.Header{
		ParentHash: common.HexToHash("0x1111"),
		Number:     new(big.Int).SetUint64(303),
		Root:       common.HexToHash("0xaaaa"),
		Extra:      []byte("recorded native lineage"),
	}
	recorded := corestate.NewBlockState(1)
	recorded.SetOrder(404)
	recorded.SetEVM(recordedHeader)

	if err := validateRecordedEVMImportTarget(types.NewBlockWithHeader(recordedHeader), recorded); err != nil {
		t.Fatalf("matching recorded candidate rejected: %v", err)
	}
	competingHeader := types.CopyHeader(recordedHeader)
	competingHeader.Extra = []byte("competing local lineage")
	err := validateRecordedEVMImportTarget(types.NewBlockWithHeader(competingHeader), recorded)
	if err == nil || !strings.Contains(err.Error(), "candidate mismatch") {
		t.Fatalf("competing candidate was not rejected before staging: %v", err)
	}
}

func TestStageExactEVMLineageFailureDoesNotActivateCanonicalHead(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
	})
	middle := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x101"),
	})
	target := types.NewBlockWithHeader(&types.Header{
		ParentHash: middle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0x102"),
	})
	states := map[common.Hash]bool{anchor.Root(): true}
	fault := errors.New("injected import fault")
	committed := false
	activated := false

	_, err := stageAndActivateExactEVMLineage(
		anchor,
		types.Blocks{middle, target},
		func(root common.Hash) bool { return states[root] },
		func(block *types.Block) error {
			if block.Hash() == target.Hash() {
				return fault
			}
			states[block.Root()] = true
			return nil
		},
		func(common.Hash) error {
			committed = true
			return nil
		},
		func() bool { return true },
		func(*types.Block) error {
			activated = true
			return nil
		},
		nil,
	)
	if !errors.Is(err, fault) {
		t.Fatalf("staging fault returned %v", err)
	}
	if committed {
		t.Fatal("partial lineage was committed after an import fault")
	}
	if activated {
		t.Fatal("canonical activation ran after a partial lineage import fault")
	}
}

func TestStageExactEVMLineageCommitFailureDoesNotActivateCanonicalHead(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
	})
	target := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x101"),
	})
	states := map[common.Hash]bool{anchor.Root(): true}
	fault := errors.New("injected commit fault")
	activated := false

	_, err := stageAndActivateExactEVMLineage(
		anchor,
		types.Blocks{target},
		func(root common.Hash) bool { return states[root] },
		func(block *types.Block) error {
			states[block.Root()] = true
			return nil
		},
		func(common.Hash) error { return fault },
		func() bool { return true },
		func(*types.Block) error {
			activated = true
			return nil
		},
		nil,
	)
	if !errors.Is(err, fault) {
		t.Fatalf("commit fault returned %v", err)
	}
	if activated {
		t.Fatal("canonical activation ran after the staged root commit failed")
	}
}

func TestStageExactEVMLineageActivatesOnceAfterCommit(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
	})
	target := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x101"),
	})
	states := map[common.Hash]bool{anchor.Root(): true}
	committed := false
	activations := 0

	replayed, err := stageAndActivateExactEVMLineage(
		anchor,
		types.Blocks{target},
		func(root common.Hash) bool { return states[root] },
		func(block *types.Block) error {
			states[block.Root()] = true
			return nil
		},
		func(root common.Hash) error {
			if root != target.Root() || !states[root] {
				t.Fatalf("commit ran before target state was ready: root=%s", root)
			}
			committed = true
			return nil
		},
		func() bool { return true },
		func(block *types.Block) error {
			if !committed {
				t.Fatal("canonical activation ran before the target root commit")
			}
			if block.Hash() != target.Hash() {
				t.Fatalf("activated wrong target: got %s want %s", block.Hash(), target.Hash())
			}
			activations++
			return nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("stage exact lineage: %v", err)
	}
	if replayed != 1 || activations != 1 {
		t.Fatalf("unexpected staged result: replayed=%d activations=%d", replayed, activations)
	}
}

func TestAutomaticEVMReconstructionLimitsStayBounded(t *testing.T) {
	if maxAutomaticEVMReconstructionDepth > 2048 {
		t.Fatalf("automatic reconstruction depth is too large: %d", maxAutomaticEVMReconstructionDepth)
	}
	if maxAutomaticEVMReconstructionBytes > 64*1024*1024 {
		t.Fatalf("automatic reconstruction byte allowance is too large: %d", maxAutomaticEVMReconstructionBytes)
	}
	if maxStoredEVMRecoveryReplay >= maxAutomaticEVMReconstructionDepth {
		t.Fatalf("stored replay leaves no room to validate the exact target: replay=%d depth=%d",
			maxStoredEVMRecoveryReplay, maxAutomaticEVMReconstructionDepth)
	}
}

func TestValidateBoundedEVMForkActivationLimitsBothBranches(t *testing.T) {
	commonBlock := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
	})
	oldMiddle := types.NewBlockWithHeader(&types.Header{
		ParentHash: commonBlock.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x201"),
		Extra:      []byte("old-middle"),
	})
	oldHead := types.NewBlockWithHeader(&types.Header{
		ParentHash: oldMiddle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0x202"),
		Extra:      []byte("old-head"),
	})
	newMiddle := types.NewBlockWithHeader(&types.Header{
		ParentHash: commonBlock.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x301"),
		Extra:      []byte("new-middle"),
	})
	newHead := types.NewBlockWithHeader(&types.Header{
		ParentHash: newMiddle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0x302"),
		Extra:      []byte("new-head"),
	})
	byHash := map[common.Hash]*types.Block{
		commonBlock.Hash(): commonBlock,
		oldMiddle.Hash():   oldMiddle,
		oldHead.Hash():     oldHead,
		newMiddle.Hash():   newMiddle,
		newHead.Hash():     newHead,
	}
	getBlock := func(hash common.Hash, number uint64) *types.Block {
		block := byHash[hash]
		if block == nil || block.NumberU64() != number {
			return nil
		}
		return block
	}

	err := validateBoundedEVMForkActivation(oldHead.Header(), newHead, getBlock, evmLineageLimits{
		maxDepth: 3,
		maxBytes: maxAutomaticEVMReconstructionBytes,
	})
	if err == nil || !strings.Contains(err.Error(), "block limit 3") {
		t.Fatalf("combined divergent branches exceeded the bound without rejection: %v", err)
	}
	if err := validateBoundedEVMForkActivation(oldHead.Header(), newHead, getBlock, evmLineageLimits{
		maxDepth: 4,
		maxBytes: maxAutomaticEVMReconstructionBytes,
	}); err != nil {
		t.Fatalf("bounded two-block fork activation rejected: %v", err)
	}
}

func TestValidateCanonicalEVMLineageWindowRejectsAncestorIndexMismatch(t *testing.T) {
	anchor := types.NewBlockWithHeader(&types.Header{
		Number: new(big.Int).SetUint64(100),
		Root:   common.HexToHash("0x100"),
	})
	middle := types.NewBlockWithHeader(&types.Header{
		ParentHash: anchor.Hash(),
		Number:     new(big.Int).SetUint64(101),
		Root:       common.HexToHash("0x101"),
	})
	target := types.NewBlockWithHeader(&types.Header{
		ParentHash: middle.Hash(),
		Number:     new(big.Int).SetUint64(102),
		Root:       common.HexToHash("0x102"),
	})
	byHash := map[common.Hash]*types.Block{
		anchor.Hash(): anchor,
		middle.Hash(): middle,
		target.Hash(): target,
	}
	canonical := map[uint64]common.Hash{
		100: anchor.Hash(),
		101: common.HexToHash("0xbad101"),
		102: target.Hash(),
	}
	err := validateCanonicalEVMLineageWindow(
		target,
		func(hash common.Hash, number uint64) *types.Block {
			block := byHash[hash]
			if block == nil || block.NumberU64() != number {
				return nil
			}
			return block
		},
		func(number uint64) common.Hash { return canonical[number] },
		evmLineageLimits{maxDepth: 3, maxBytes: maxAutomaticEVMReconstructionBytes},
	)
	if err == nil || !strings.Contains(err.Error(), "index mismatch at block 101") {
		t.Fatalf("ancestor canonical-index corruption was not rejected: %v", err)
	}
}

func TestValidateBoundedCanonicalTailDeletionRejectsLongStaleTail(t *testing.T) {
	canonical := map[uint64]common.Hash{
		100: common.HexToHash("0x100"),
		101: common.HexToHash("0x101"),
		102: common.HexToHash("0x102"),
	}
	getCanonicalHash := func(number uint64) common.Hash { return canonical[number] }

	if err := validateBoundedCanonicalTailDeletion(100, getCanonicalHash, 3, nil); err != nil {
		t.Fatalf("bounded canonical tail rejected: %v", err)
	}
	err := validateBoundedCanonicalTailDeletion(100, getCanonicalHash, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "entry limit 2") {
		t.Fatalf("long stale canonical tail was not rejected: %v", err)
	}

	interrupt := make(chan struct{})
	close(interrupt)
	if err := validateBoundedCanonicalTailDeletion(100, getCanonicalHash, 3, interrupt); !errors.Is(err, errEVMStateReconstructionInterrupted) {
		t.Fatalf("interrupted canonical tail validation returned %v", err)
	}
}

func setTestEVMState(state *corestate.BlockState, number uint64) {
	state.SetEVM(&types.Header{
		Number: new(big.Int).SetUint64(number),
		Root:   common.HexToHash("0x01"),
	})
}
