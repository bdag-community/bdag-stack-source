package synch

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p2pcommon "github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

type fakeEVMCheckpointChain struct {
	valid       bool
	known       bool
	validations []storedEVMCheckpoint
	stored      []storedEVMCheckpoint
}

type storedEVMCheckpoint struct {
	pid    peer.ID
	number uint64
	hash   ethcommon.Hash
	root   ethcommon.Hash
}

func (c *fakeEVMCheckpointChain) ValidateEVMCheckpoint(number uint64, hash ethcommon.Hash, root ethcommon.Hash) (bool, bool) {
	c.validations = append(c.validations, storedEVMCheckpoint{
		number: number,
		hash:   hash,
		root:   root,
	})
	return c.valid, c.known
}

func (c *fakeEVMCheckpointChain) SetPeerEVMCheckpoint(pid peer.ID, number uint64, hash ethcommon.Hash, root ethcommon.Hash) {
	c.stored = append(c.stored, storedEVMCheckpoint{
		pid:    pid,
		number: number,
		hash:   hash,
		root:   root,
	})
}

func TestValidateChainStateEVMCheckpointRejectsMissingCheckpoint(t *testing.T) {
	err := validateChainStateEVMCheckpointWithChain(&fakeEVMCheckpointChain{}, testCheckpointPeerID(t), &pb.ChainState{})
	if err == nil {
		t.Fatal("missing checkpoint accepted")
	}
	if err.Code != p2pcommon.ErrDAGConsensus {
		t.Fatalf("error code = %s, want %s", err.Code, p2pcommon.ErrDAGConsensus)
	}
}

func TestValidateChainStateEVMCheckpointRejectsNilMessage(t *testing.T) {
	err := validateChainStateEVMCheckpointWithChain(&fakeEVMCheckpointChain{}, testCheckpointPeerID(t), nil)
	if err == nil || err.Code != p2pcommon.ErrDAGConsensus {
		t.Fatalf("nil message error = %v, want ErrDAGConsensus", err)
	}
}

func TestValidateChainStateEVMCheckpointNilChainIsIgnored(t *testing.T) {
	if err := validateChainStateEVMCheckpointWithChain(nil, testCheckpointPeerID(t), nil); err != nil {
		t.Fatalf("nil chain returned error: %v", err)
	}
}

func TestValidateChainStateEVMCheckpointRejectsBadHashLength(t *testing.T) {
	err := validateChainStateEVMCheckpointWithChain(&fakeEVMCheckpointChain{}, testCheckpointPeerID(t), &pb.ChainState{
		EvmCheckpoint: &pb.EVMCheckpoint{
			Number:    100,
			Hash:      &pb.Hash{Hash: []byte{1}},
			StateRoot: &pb.Hash{Hash: ethcommon.Hash{}.Bytes()},
		},
	})
	if err == nil {
		t.Fatal("bad checkpoint hash accepted")
	}
	if err.Code != p2pcommon.ErrDAGConsensus {
		t.Fatalf("error code = %s, want %s", err.Code, p2pcommon.ErrDAGConsensus)
	}
}

func TestValidateChainStateEVMCheckpointRejectsBadRootLength(t *testing.T) {
	err := validateChainStateEVMCheckpointWithChain(&fakeEVMCheckpointChain{}, testCheckpointPeerID(t), &pb.ChainState{
		EvmCheckpoint: &pb.EVMCheckpoint{
			Number:    100,
			Hash:      &pb.Hash{Hash: ethcommon.Hash{}.Bytes()},
			StateRoot: &pb.Hash{Hash: []byte{1}},
		},
	})
	if err == nil {
		t.Fatal("bad checkpoint root accepted")
	}
	if err.Code != p2pcommon.ErrDAGConsensus {
		t.Fatalf("error code = %s, want %s", err.Code, p2pcommon.ErrDAGConsensus)
	}
}

func TestValidateChainStateEVMCheckpointAllowsUnknownCheckpoint(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	chain := &fakeEVMCheckpointChain{valid: false, known: false}

	err := validateChainStateEVMCheckpointWithChain(chain, testCheckpointPeerID(t), &pb.ChainState{
		EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
	})
	if err != nil {
		t.Fatalf("unknown checkpoint rejected: %v", err)
	}
	assertValidatedCheckpoint(t, chain, 100, hash, root)
}

func TestValidateChainStateEVMCheckpointRetainsUnknownEdgeNumbers(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	for _, number := range []uint64{0, ^uint64(0)} {
		t.Run(fmt.Sprintf("number-%d", number), func(t *testing.T) {
			chain := &fakeEVMCheckpointChain{valid: false, known: false}
			pid := peer.ID(fmt.Sprintf("peer-%d", number))
			if err := validateChainStateEVMCheckpointWithChain(chain, pid, &pb.ChainState{
				EvmCheckpoint: testPBEVMCheckpoint(number, hash, root),
			}); err != nil {
				t.Fatalf("unknown edge checkpoint rejected: %v", err)
			}
			if len(chain.stored) != 1 || chain.stored[0].number != number {
				t.Fatalf("stored = %#v, want one checkpoint at %d", chain.stored, number)
			}
		})
	}
}

func TestValidateChainStateEVMCheckpointRejectsKnownMismatch(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	chain := &fakeEVMCheckpointChain{valid: false, known: true}

	err := validateChainStateEVMCheckpointWithChain(chain, testCheckpointPeerID(t), &pb.ChainState{
		EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
	})
	if err == nil {
		t.Fatal("known mismatched checkpoint accepted")
	}
	if err.Code != p2pcommon.ErrDAGConsensus {
		t.Fatalf("error code = %s, want %s", err.Code, p2pcommon.ErrDAGConsensus)
	}
	if !strings.Contains(err.String(), "evm checkpoint mismatch") {
		t.Fatalf("error = %q, want mismatch detail", err.String())
	}
	assertValidatedCheckpoint(t, chain, 100, hash, root)
}

func TestValidateChainStateEVMCheckpointRejectsKnownMismatchRegardlessOfPeerCount(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	for _, peerCount := range []int{1, 3, 64} {
		t.Run(fmt.Sprintf("%d peers", peerCount), func(t *testing.T) {
			chain := &fakeEVMCheckpointChain{valid: false, known: true}
			for i := 0; i < peerCount; i++ {
				err := validateChainStateEVMCheckpointWithChain(chain, peer.ID(fmt.Sprintf("peer-%d", i)), &pb.ChainState{
					EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
				})
				if err == nil {
					t.Fatalf("peer %d known mismatch accepted", i)
				}
				if err.Code != p2pcommon.ErrDAGConsensus {
					t.Fatalf("peer %d error code = %s, want %s", i, err.Code, p2pcommon.ErrDAGConsensus)
				}
			}
			if len(chain.stored) != 0 {
				t.Fatalf("stored mismatching checkpoints = %d, want 0", len(chain.stored))
			}
		})
	}
}

func TestValidatePeerEVMCheckpointStoresUnknownCheckpoint(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	pid := testCheckpointPeerID(t)
	chain := &fakeEVMCheckpointChain{valid: false, known: false}

	err := validatePeerEVMCheckpointWithChain(chain, pid, &pb.ChainState{
		EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
	})
	if err != nil {
		t.Fatalf("unknown peer checkpoint rejected: %v", err)
	}
	assertStoredCheckpoint(t, chain, pid, 100, hash, root)
}

func TestValidatePeerEVMCheckpointRejectsMissingOrMalformedCheckpoint(t *testing.T) {
	tests := []struct {
		name       string
		checkpoint *pb.EVMCheckpoint
	}{
		{name: "missing", checkpoint: nil},
		{name: "missing hash", checkpoint: &pb.EVMCheckpoint{StateRoot: &pb.Hash{Hash: ethcommon.Hash{}.Bytes()}}},
		{name: "bad hash", checkpoint: &pb.EVMCheckpoint{Hash: &pb.Hash{Hash: []byte{1}}, StateRoot: &pb.Hash{Hash: ethcommon.Hash{}.Bytes()}}},
		{name: "missing root", checkpoint: &pb.EVMCheckpoint{Hash: &pb.Hash{Hash: ethcommon.Hash{}.Bytes()}}},
		{name: "bad root", checkpoint: &pb.EVMCheckpoint{Hash: &pb.Hash{Hash: ethcommon.Hash{}.Bytes()}, StateRoot: &pb.Hash{Hash: []byte{1}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chain := &fakeEVMCheckpointChain{}
			if err := validatePeerEVMCheckpointWithChain(chain, testCheckpointPeerID(t), &pb.ChainState{EvmCheckpoint: test.checkpoint}); err == nil {
				t.Fatal("malformed or missing checkpoint accepted")
			}
			if len(chain.stored) != 0 {
				t.Fatalf("stored malformed checkpoint = %d, want 0", len(chain.stored))
			}
		})
	}
}

func TestValidatePeerEVMCheckpointRejectsNilChainState(t *testing.T) {
	chain := &fakeEVMCheckpointChain{}
	if err := validatePeerEVMCheckpointWithChain(chain, testCheckpointPeerID(t), nil); err == nil {
		t.Fatal("nil chain state accepted")
	}
	if len(chain.stored) != 0 {
		t.Fatalf("stored nil chain state = %d, want 0", len(chain.stored))
	}
}

func TestValidatePeerEVMCheckpointNilInputsAreIgnored(t *testing.T) {
	var ps *PeerSync
	if err := ps.validatePeerEVMCheckpoint(nil); err != nil {
		t.Fatalf("nil peer sync returned error: %v", err)
	}
	chain := &fakeEVMCheckpointChain{}
	if err := validatePeerEVMCheckpointWithChain(nil, "", nil); err != nil {
		t.Fatalf("nil chain returned error: %v", err)
	}
	if len(chain.stored) != 0 {
		t.Fatalf("stored nil inputs = %d, want 0", len(chain.stored))
	}
}

func TestValidatePeerEVMCheckpointStoresKnownValidCheckpoint(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	pid := testCheckpointPeerID(t)
	chain := &fakeEVMCheckpointChain{valid: true, known: true}

	err := validatePeerEVMCheckpointWithChain(chain, pid, &pb.ChainState{
		EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
	})
	if err != nil {
		t.Fatalf("known valid peer checkpoint rejected: %v", err)
	}
	assertStoredCheckpoint(t, chain, pid, 100, hash, root)
}

func TestValidatePeerEVMCheckpointRejectsKnownMismatch(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	pid := testCheckpointPeerID(t)
	chain := &fakeEVMCheckpointChain{valid: false, known: true}

	err := validatePeerEVMCheckpointWithChain(chain, pid, &pb.ChainState{
		EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
	})
	if err == nil {
		t.Fatal("known mismatched peer checkpoint accepted")
	}
	if !strings.Contains(err.Error(), "evm checkpoint mismatch") {
		t.Fatalf("error = %q, want mismatch detail", err.Error())
	}
	if len(chain.stored) != 0 {
		t.Fatalf("stored mismatching checkpoints = %d, want 0", len(chain.stored))
	}
}

func TestValidatePeerEVMCheckpointRejectsKnownMismatchRegardlessOfPeerCount(t *testing.T) {
	hash := ethcommon.HexToHash("0x01")
	root := ethcommon.HexToHash("0x02")
	for _, peerCount := range []int{1, 3, 64} {
		t.Run(fmt.Sprintf("%d peers", peerCount), func(t *testing.T) {
			chain := &fakeEVMCheckpointChain{valid: false, known: true}
			for i := 0; i < peerCount; i++ {
				err := validatePeerEVMCheckpointWithChain(chain, peer.ID(fmt.Sprintf("peer-%d", i)), &pb.ChainState{
					EvmCheckpoint: testPBEVMCheckpoint(100, hash, root),
				})
				if err == nil {
					t.Fatalf("peer %d known mismatch accepted", i)
				}
			}
			if len(chain.stored) != 0 {
				t.Fatalf("stored mismatching checkpoints = %d, want 0", len(chain.stored))
			}
		})
	}
}

func TestRecordPeerEVMStateGuardFailureSoftUntilThreshold(t *testing.T) {
	syncPoint := ethcommon.HexToHash("0xabc")
	pe := peers.NewPeer(testCheckpointPeerID(t), &syncPoint)
	ps := NewPeerSync(nil)
	err := errors.New("EVM state commitment mismatch")

	for i := 1; i < evmStateGuardPeerRejectThreshold; i++ {
		decision := ps.recordPeerEVMStateGuardFailure(pe, err)
		if decision.reject {
			t.Fatalf("strike %d rejected peer before threshold %d", i, evmStateGuardPeerRejectThreshold)
		}
		if decision.strikes != i {
			t.Fatalf("strike count = %d, want %d", decision.strikes, i)
		}
		if decision.syncPoint != syncPoint {
			t.Fatalf("sync point = %s, want %s", decision.syncPoint, syncPoint)
		}
	}
	if !pe.IsBad() {
		t.Fatal("state guard failure should temporarily score peer as bad")
	}

	decision := ps.recordPeerEVMStateGuardFailure(pe, err)
	if !decision.reject {
		t.Fatal("peer was not rejected at repeated state guard failure threshold")
	}
	if decision.strikes != evmStateGuardPeerRejectThreshold {
		t.Fatalf("strike count = %d, want %d", decision.strikes, evmStateGuardPeerRejectThreshold)
	}
}

func TestRecordPeerEVMStateGuardFailureResetsWhenSyncPointChanges(t *testing.T) {
	firstSyncPoint := ethcommon.HexToHash("0xabc")
	secondSyncPoint := ethcommon.HexToHash("0xdef")
	pe := peers.NewPeer(testCheckpointPeerID(t), &firstSyncPoint)
	ps := NewPeerSync(nil)
	err := errors.New("EVM state commitment mismatch")

	first := ps.recordPeerEVMStateGuardFailure(pe, err)
	if first.strikes != 1 {
		t.Fatalf("first strike count = %d, want 1", first.strikes)
	}

	pe.UpdateSyncPoint(&secondSyncPoint)
	second := ps.recordPeerEVMStateGuardFailure(pe, err)
	if second.reject {
		t.Fatal("sync point change should reset state guard rejection threshold")
	}
	if second.strikes != 1 {
		t.Fatalf("second sync point strike count = %d, want 1", second.strikes)
	}
	if second.syncPoint != secondSyncPoint {
		t.Fatalf("sync point = %s, want %s", second.syncPoint, secondSyncPoint)
	}
}

func TestClearPeerEVMStateGuardFailuresResetsThreshold(t *testing.T) {
	syncPoint := ethcommon.HexToHash("0xabc")
	pe := peers.NewPeer(testCheckpointPeerID(t), &syncPoint)
	ps := NewPeerSync(nil)
	err := errors.New("EVM state commitment mismatch")

	for i := 0; i < evmStateGuardPeerRejectThreshold-1; i++ {
		decision := ps.recordPeerEVMStateGuardFailure(pe, err)
		if decision.reject {
			t.Fatalf("strike %d rejected before clear", i+1)
		}
	}

	ps.clearPeerEVMStateGuardFailures(pe)
	decision := ps.recordPeerEVMStateGuardFailure(pe, err)
	if decision.reject {
		t.Fatal("strike after clear should not reject")
	}
	if decision.strikes != 1 {
		t.Fatalf("strike count after clear = %d, want 1", decision.strikes)
	}
}

func TestTestEVMCheckpointOverrideDisabled(t *testing.T) {
	t.Setenv(testEVMCheckpointOverrideEnv, "")

	checkpoint, enabled := testEVMCheckpointOverride()
	if enabled {
		t.Fatal("override unexpectedly enabled")
	}
	if checkpoint != nil {
		t.Fatalf("checkpoint = %v, want nil", checkpoint)
	}
}

func TestParseTestEVMCheckpointOverrideFromEnv(t *testing.T) {
	hash := ethcommon.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	t.Setenv(testEVMCheckpointOverrideNumberEnv, "123")
	t.Setenv(testEVMCheckpointOverrideHashEnv, hash.Hex())
	t.Setenv(testEVMCheckpointOverrideRootEnv, "")

	checkpoint, err := parseTestEVMCheckpointOverrideFromEnv()
	if err != nil {
		t.Fatalf("parse override: %v", err)
	}
	if checkpoint.GetNumber() != 123 {
		t.Fatalf("number = %d, want 123", checkpoint.GetNumber())
	}
	if got := ethcommon.BytesToHash(checkpoint.GetHash().GetHash()); got != hash {
		t.Fatalf("hash = %s, want %s", got, hash)
	}
	if got := ethcommon.BytesToHash(checkpoint.GetStateRoot().GetHash()); got != (ethcommon.Hash{}) {
		t.Fatalf("root = %s, want zero hash", got)
	}
}

func TestParseTestEVMCheckpointOverrideRejectsShortHash(t *testing.T) {
	t.Setenv(testEVMCheckpointOverrideHashEnv, "0x01")

	_, err := parseTestEVMCheckpointOverrideFromEnv()
	if err == nil {
		t.Fatal("short override hash accepted")
	}
}

func testPBEVMCheckpoint(number uint64, hash ethcommon.Hash, root ethcommon.Hash) *pb.EVMCheckpoint {
	return &pb.EVMCheckpoint{
		Number:    number,
		Hash:      &pb.Hash{Hash: hash.Bytes()},
		StateRoot: &pb.Hash{Hash: root.Bytes()},
	}
}

func testCheckpointPeerID(t *testing.T) peer.ID {
	t.Helper()
	pid, err := peer.Decode("16Uiu2HAm5AMKGyWfsrjssUryHF37QMM5c4zjJqeNfGM6TNxCvs7g")
	if err != nil {
		t.Fatalf("decode peer ID: %v", err)
	}
	return pid
}

func assertValidatedCheckpoint(t *testing.T, chain *fakeEVMCheckpointChain, number uint64, hash ethcommon.Hash, root ethcommon.Hash) {
	t.Helper()
	if len(chain.validations) != 1 {
		t.Fatalf("validations = %d, want 1", len(chain.validations))
	}
	got := chain.validations[0]
	if got.number != number || got.hash != hash || got.root != root {
		t.Fatalf("validated checkpoint = (%d, %s, %s), want (%d, %s, %s)", got.number, got.hash, got.root, number, hash, root)
	}
}

func assertStoredCheckpoint(t *testing.T, chain *fakeEVMCheckpointChain, pid peer.ID, number uint64, hash ethcommon.Hash, root ethcommon.Hash) {
	t.Helper()
	if len(chain.stored) != 1 {
		t.Fatalf("stored checkpoints = %d, want 1", len(chain.stored))
	}
	got := chain.stored[0]
	if got.pid != pid || got.number != number || got.hash != hash || got.root != root {
		t.Fatalf("stored checkpoint = (%s, %d, %s, %s), want (%s, %d, %s, %s)", got.pid, got.number, got.hash, got.root, pid, number, hash, root)
	}
}
