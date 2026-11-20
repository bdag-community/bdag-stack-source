package blockchain

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"math/big"
	"testing"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type localRecoveryReader struct {
	head    *gethtypes.Header
	failure error
	probes  int
	last    model.BlockState
}

func (r *localRecoveryReader) GetCurHeader() *gethtypes.Header { return r.head }
func (r *localRecoveryReader) EVMHeaderForState(anchor model.BlockState) (*gethtypes.Header, error) {
	r.probes++
	r.last = anchor
	if r.failure != nil {
		return nil, r.failure
	}
	if anchor.GetEVMNumber() != r.head.Number.Uint64() || anchor.GetEVMHash() != r.head.Hash() || anchor.GetEVMRoot() != r.head.Root {
		return nil, fmt.Errorf("exact anchor tuple mismatch")
	}
	return r.head, nil
}

type localRecoveryFixture struct {
	bc      *BlockChain
	db      *chaindb.ChainDB
	cfg     *config.Config
	reader  *localRecoveryReader
	genesis blockdag.IBlock
}

func newLocalRecoveryFixture(t *testing.T) *localRecoveryFixture {
	t.Helper()
	cfg := &config.Config{DataDir: ""}
	db, err := chaindb.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	bd := blockdag.New(blockdag.PHANTOM, -1, db, nil)
	_, _, genesis, _ := bd.AddBlock(rollbackTestBlockData{hash: ethcommon.HexToHash("0x01"), timestamp: 1})
	if genesis == nil {
		t.Fatal("genesis not added")
	}
	reader := &localRecoveryReader{head: &gethtypes.Header{Number: big.NewInt(100), Root: ethcommon.HexToHash("0x1234")}}
	bc := &BlockChain{bd: bd, consensus: &mockConsensus{db: db}}
	fixture := &localRecoveryFixture{bc: bc, db: db, cfg: cfg, reader: reader, genesis: genesis}
	fixture.commit(t, genesis)
	return fixture
}

func (f *localRecoveryFixture) commit(t *testing.T, block blockdag.IBlock) {
	t.Helper()
	block.GetState().(*state.BlockState).SetEVM(f.reader.head)
	if err := f.bc.bd.Commit(); err != nil {
		t.Fatal(err)
	}
	tip := f.bc.bd.GetMainChainTip()
	f.bc.stateSnapshot = &BestState{Hash: *tip.GetHash(), GraphState: f.bc.bd.GetGraphState(), StateRoot: *tip.GetState().Root()}
	if err := dbPutBestState(f.db, f.bc.stateSnapshot, big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	f.bc.publishCommittedEVMHead()
}

func (f *localRecoveryFixture) databaseBytes(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	iterator := f.db.DB().NewIterator(nil, nil)
	defer iterator.Release()
	for iterator.Next() {
		fmt.Fprintf(&out, "%x=%x\n", iterator.Key(), iterator.Value())
	}
	if err := iterator.Error(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Build a deterministic competing branch using the actual Phantom DAG, not a
// mocked Rollback. Return its first selected reorganization without committing.
func (f *localRecoveryFixture) reorg(t *testing.T) (rollbackTestBlockData, blockdag.IBlock, *list.List, *list.List, *localReorgBoundary, []byte) {
	t.Helper()
	parent := f.genesis
	for i := 0; i < 3; i++ {
		data := rollbackTestBlockData{hash: ethcommon.BigToHash(big.NewInt(int64(10 + i))), parents: []*ethcommon.Hash{parent.GetHash()}, mainParent: parent.GetHash(), timestamp: int64(2 + i)}
		_, _, block, _ := f.bc.bd.AddBlock(data)
		if block == nil {
			t.Fatal("spine block not added")
		}
		f.commit(t, block)
		parent = block
	}
	parent = f.genesis
	for i := 0; i < 12; i++ {
		data := rollbackTestBlockData{hash: ethcommon.BigToHash(big.NewInt(int64(100 + i))), parents: []*ethcommon.Hash{parent.GetHash()}, mainParent: parent.GetHash(), timestamp: int64(10 + i)}
		before, err := f.bc.captureLocalReorgBoundary(f.reader, data.GetHash())
		if err != nil {
			t.Fatal(err)
		}
		persisted := f.databaseBytes(t)
		attach, detach, block, _ := f.bc.bd.AddBlock(data)
		if block == nil {
			t.Fatal("fork block not added")
		}
		if detach.Len() != 0 {
			return data, block, attach, detach, before, persisted
		}
		f.commit(t, block)
		parent = block
	}
	t.Fatal("bounded fixture did not cause a Phantom reorg")
	return rollbackTestBlockData{}, nil, nil, nil, nil, nil
}

func TestLocalEVMReorgPreflightRollbackAndSameCandidateRetry(t *testing.T) {
	for _, failure := range []string{"missing EVM header", "missing EVM state", "mismatched exact EVM root"} {
		t.Run(failure, func(t *testing.T) {
			f := newLocalRecoveryFixture(t)
			data, candidate, attach, detach, before, persisted := f.reorg(t)
			f.reader.failure = errors.New(failure)
			err := f.bc.preflightLocalEVMReorg(f.reader, candidate, attach, detach, before)
			var local *LocalEVMStateUnavailableError
			if !errors.As(err, &local) || local.Fatal {
				t.Fatalf("expected retryable local failure, got %v", err)
			}
			if isEVMStateGuardRuleError(err) {
				t.Fatal("local failure became a consensus violation")
			}
			if !bytes.Equal(persisted, f.databaseBytes(t)) {
				t.Fatal("failed preflight changed native database bytes/indexes/markers")
			}
			if f.db.HasBlock(data.GetHash()) || f.bc.bd.HasBlock(data.GetHash()) {
				t.Fatal("candidate is no longer eligible for retry")
			}
			if held, _ := f.bc.LocalEVMRecoveryStatus(); !held {
				t.Fatal("missing mining hold")
			}
			if f.reader.head.Hash() != before.actualEVM.hash {
				t.Fatal("preflight changed EVM head")
			}
			f.reader.failure = nil
			attach2, detach2, retry, _ := f.bc.bd.AddBlock(data)
			if retry == nil || retry.GetHash().Cmp(*data.GetHash()) != 0 {
				t.Fatal("same candidate retry failed")
			}
			if err := f.bc.preflightLocalEVMReorg(f.reader, retry, attach2, detach2, before); err != nil {
				t.Fatal(err)
			}
			if held, _ := f.bc.LocalEVMRecoveryStatus(); !held {
				t.Fatal("read-only successful probe prematurely cleared hold")
			}
			f.commit(t, retry)
			f.bc.clearAppliedLocalEVMRecovery(*retry.GetHash(), f.reader)
			if held, reason := f.bc.LocalEVMRecoveryStatus(); held {
				t.Fatalf("coherent durable same-candidate adoption did not clear hold: %s", reason)
			}
		})
	}
}

func TestLocalEVMReorgRollbackMismatchIsStickyFatal(t *testing.T) {
	f := newLocalRecoveryFixture(t)
	_, candidate, attach, detach, before, _ := f.reorg(t)
	f.reader.failure = errors.New("state absent")
	before.order++ // Fault injection: acceptance must not trust Rollback alone.
	err := f.bc.preflightLocalEVMReorg(f.reader, candidate, attach, detach, before)
	var local *LocalEVMStateUnavailableError
	if !errors.As(err, &local) || !local.Fatal {
		t.Fatalf("unproven rollback must fail closed: %v", err)
	}
	f.bc.clearAppliedLocalEVMRecovery(*candidate.GetHash(), f.reader)
	if held, _ := f.bc.LocalEVMRecoveryStatus(); !held {
		t.Fatal("fatal hold was cleared")
	}
	if _, err := f.bc.maybeAcceptBlock(nil, 0, nil); !IsLocalEVMStateUnavailable(err) {
		t.Fatal("fatal hold permitted further native acceptance")
	}
}

func TestLocalEVMReorgRollbackFailureIsStickyFatal(t *testing.T) {
	f := newLocalRecoveryFixture(t)
	_, candidate, attach, detach, before, _ := f.reorg(t)
	f.reader.failure = errors.New("state absent")
	// Simulate loss of the speculative snapshot. The handler must not claim
	// retry-safe rollback merely because the local EVM failure is understood.
	if err := f.bc.bd.Commit(); err != nil {
		t.Fatal(err)
	}
	err := f.bc.preflightLocalEVMReorg(f.reader, candidate, attach, detach, before)
	var local *LocalEVMStateUnavailableError
	if !errors.As(err, &local) || !local.Fatal {
		t.Fatalf("missing rollback snapshot did not fail closed: %v", err)
	}
}

func TestLocalEVMReorgAnchorIsImmutableAcrossNativeDefaultUpdates(t *testing.T) {
	f := newLocalRecoveryFixture(t)
	_, _, attach, _, _, _ := f.reorg(t)
	anchor, first, err := f.bc.evmReorgAnchor(attach)
	if err != nil || anchor == nil || first == nil {
		t.Fatalf("anchor: %v", err)
	}
	wantHash, wantRoot := anchor.GetEVMHash(), anchor.GetEVMRoot()
	previous := f.bc.bd.GetBlockByOrder(first.GetOrder() - 1)
	previous.GetState().SetEVM(&gethtypes.Header{Number: big.NewInt(999), Root: ethcommon.HexToHash("0x999")})
	if anchor.GetEVMHash() != wantHash || anchor.GetEVMRoot() != wantRoot {
		t.Fatal("selected anchor aliased mutable native state")
	}
}

func TestLocalEVMRecoveryHoldsAreCandidateBoundAndDoNotAlterValidity(t *testing.T) {
	f := newLocalRecoveryFixture(t)
	first, second := *f.genesis.GetHash(), ethcommon.HexToHash("0x22")
	committed := f.bc.committedEVMHead.Load()
	f.bc.holdLocalEVMRecovery(&LocalEVMStateUnavailableError{Candidate: first, Cause: errors.New("first")})
	f.bc.holdLocalEVMRecovery(&LocalEVMStateUnavailableError{Candidate: second, Cause: errors.New("second")})
	f.bc.clearAppliedLocalEVMRecovery(ethcommon.HexToHash("0x33"), f.reader)
	if len(f.bc.localEVMRecovery.Load().pending) != 2 {
		t.Fatal("unrelated adoption cleared a hold")
	}
	f.bc.clearAppliedLocalEVMRecovery(first, f.reader)
	if len(f.bc.localEVMRecovery.Load().pending) != 1 || f.bc.localEVMRecovery.Load().pending[second] == nil {
		t.Fatal("matching adoption erased another candidate")
	}
	if f.bc.committedEVMHead.Load() != committed {
		t.Fatal("mining hold falsified native commitment validity")
	}
}

func recoveredGovernanceFixture(t *testing.T, overlay ...bool) (*localRecoveryFixture, blockdag.IBlock, error) {
	t.Helper()
	f := newLocalRecoveryFixture(t)
	data, candidate, attach, detach, before, _ := f.reorg(t)
	f.reader.failure = errors.New("original anchor state missing")
	if err := f.bc.preflightLocalEVMReorg(f.reader, candidate, attach, detach, before); !IsLocalEVMStateUnavailable(err) {
		t.Fatalf("missing-state preflight: %v", err)
	}
	f.reader.failure = nil
	attach, detach, candidate, _ = f.bc.bd.AddBlock(data)
	if err := f.bc.preflightLocalEVMReorg(f.reader, candidate, attach, detach, before); err != nil {
		t.Fatal(err)
	}
	rejection := f.bc.bindLocalGovernanceRejection(nil, candidate, ruleError(ErrGovernanceAdmissibility, "intrinsic governance rejection at this candidate"))
	f.bc.bd.InvalidBlock(candidate)
	if len(overlay) != 0 && overlay[0] {
		f.cfg.Cache, f.cfg.CacheSnapshot = 1024, 5
		if err := f.db.Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	f.commit(t, candidate)
	return f, candidate, rejection
}

func TestLocalEVMRecoveredGovernanceRejectionResolvesOnlyItsHold(t *testing.T) {
	f, candidate, rejection := recoveredGovernanceFixture(t)
	other := ethcommon.HexToHash("0xbeef")
	f.bc.holdLocalEVMRecovery(&LocalEVMStateUnavailableError{Candidate: other, Cause: errors.New("another pending candidate")})
	stops := 0
	completed, err := f.bc.completeLocalGovernanceRejections(f.reader, rejection, func() error { stops++; return nil })
	if !completed || err != nil || stops != 1 {
		t.Fatalf("coherent committed rejection not completed: handled=%t stops=%d err=%v", completed, stops, err)
	}
	holds := f.bc.localEVMRecovery.Load()
	if holds.pending[*candidate.GetHash()] != nil || holds.pending[other] == nil || holds.fatal != nil {
		t.Fatal("governance completion changed an unrelated hold")
	}
	if !candidate.GetState().GetStatus().KnownInvalid() || f.bc.committedEVMHead.Load().valid {
		t.Fatal("resolving local recovery falsified consensus-invalid tip semantics")
	}
}

func TestLocalEVMGovernanceResolutionRejectsBatchMisattributionAndCheckpoint(t *testing.T) {
	f, candidate, _ := recoveredGovernanceFixture(t)
	for _, rejection := range []error{
		f.bc.bindLocalGovernanceRejection(nil, f.genesis, ruleError(ErrGovernanceAdmissibility, "different batch culprit")),
		f.bc.bindLocalGovernanceRejection(nil, candidate, ruleError(ErrEVMCheckpointMismatch, "peer checkpoint")),
		f.bc.bindLocalGovernanceRejection(nil, candidate, errors.New("storage failure")),
		f.bc.bindLocalGovernanceRejection(nil, candidate, f.bc.evmStateGuardRecoveryError(candidate, nil, errors.New("rewind failed"))),
	} {
		completed, err := f.bc.completeLocalGovernanceRejections(f.reader, rejection, func() error { t.Fatal("unbound rejection completed marker"); return nil })
		if completed || err != nil || f.bc.localEVMRecovery.Load().pending[*candidate.GetHash()] == nil {
			t.Fatalf("unproven rejection changed hold: %v", rejection)
		}
	}
}

func TestLocalEVMGovernanceFailedProofRetainsOperationMarker(t *testing.T) {
	for _, fault := range []string{"best total", "tip state", "original state unavailable", "actual EVM head", "order index"} {
		t.Run(fault, func(t *testing.T) {
			f, candidate, rejection := recoveredGovernanceFixture(t)
			switch fault {
			case "best total":
				data, _ := f.db.GetBestChainState()
				best, _ := DeserializeBestChainState(data)
				best.total++
				if err := f.db.PutBestChainState(serializeBestChainState(best)); err != nil {
					t.Fatal(err)
				}
			case "tip state":
				f.bc.bd.GetMainChainTip().GetState().(*state.BlockState).SetRoot(&ethcommon.Hash{1})
			case "original state unavailable":
				f.reader.failure = errors.New("state unavailable")
			case "actual EVM head":
				f.reader.head = &gethtypes.Header{Number: big.NewInt(101), Root: ethcommon.HexToHash("0x4321")}
			case "order index":
				if err := f.db.PutBlockIdByOrder(candidate.GetOrder(), blockdag.MaxId); err != nil {
					t.Fatal(err)
				}
			}
			markerPending := true
			completed, err := f.bc.completeLocalGovernanceRejections(f.reader, rejection, func() error { markerPending = false; return nil })
			if !completed || !IsLocalEVMStateUnavailable(err) || !markerPending || f.bc.localEVMRecovery.Load().fatal == nil {
				t.Fatalf("failed proof did not retain marker and fatal hold: handled=%t marker=%t err=%v", completed, markerPending, err)
			}
		})
	}
}

func TestLocalEVMGovernanceStopFailureCannotClearHold(t *testing.T) {
	f, candidate, rejection := recoveredGovernanceFixture(t)
	_, err := f.bc.completeLocalGovernanceRejections(f.reader, rejection, func() error { return errors.New("marker completion failed") })
	if !IsLocalEVMStateUnavailable(err) || f.bc.localEVMRecovery.Load().fatal == nil || f.bc.localEVMRecovery.Load().pending[*candidate.GetHash()] == nil {
		t.Fatal("failed marker completion cleared recovery")
	}
}

func TestLocalEVMGovernanceOverlayAheadOfRawCannotCompleteMarker(t *testing.T) {
	f, candidate, rejection := recoveredGovernanceFixture(t, true)
	view, err := f.db.GetBestChainState()
	if err != nil || bytes.Equal(view, rawdb.ReadBestChainState(f.db.DB())) {
		t.Fatalf("fixture must have a new overlay over an old durable boundary: %v", err)
	}
	markerPending := true
	completed, err := f.bc.completeLocalGovernanceRejections(f.reader, rejection, func() error { markerPending = false; return nil })
	if !completed || !IsLocalEVMStateUnavailable(err) || !markerPending || f.bc.localEVMRecovery.Load().fatal == nil || f.bc.localEVMRecovery.Load().pending[*candidate.GetHash()] == nil {
		t.Fatalf("overlay was mistaken for durable acceptance: completed=%t marker=%t err=%v", completed, markerPending, err)
	}
}

func TestLocalEVMPreflightSkipsNormalExtensionAndDoesNotApplyRewindDepthPolicy(t *testing.T) {
	f := newLocalRecoveryFixture(t)
	f.reader.failure = errors.New("probe must not run on a normal extension")
	if err := f.bc.preflightLocalEVMReorg(f.reader, f.genesis, list.New(), list.New(), nil); err != nil {
		t.Fatal(err)
	}
	if f.reader.probes != 0 {
		t.Fatal("normal extension unexpectedly preflighted")
	}
	f.reader.failure = nil
	_, candidate, attach, detach, before, _ := f.reorg(t)
	if err := f.bc.preflightLocalEVMReorg(f.reader, candidate, attach, detach, before); err != nil {
		t.Fatal(err)
	}
	if f.reader.probes != 1 {
		t.Fatal("expected exact shared anchor probe")
	}
}

func TestLocalEVMErrorNeverUnwrapsConsensusCause(t *testing.T) {
	err := fmt.Errorf("outer: %w", &LocalEVMStateUnavailableError{Cause: ruleError(ErrEVMStateGuard, "local diagnostic")})
	if !IsLocalEVMStateUnavailable(err) || isEVMStateGuardRuleError(err) {
		t.Fatal("local state failure contaminated consensus classification")
	}
}
