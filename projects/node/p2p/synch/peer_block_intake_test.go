package synch

import (
	"errors"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestIsDuplicateBlockProcessError(t *testing.T) {
	err := blockchain.RuleError{
		ErrorCode:   blockchain.ErrDuplicateBlock,
		Description: "already have block (orphan) 0xabc",
	}
	if !isDuplicateBlockProcessError(err) {
		t.Fatal("duplicate block rule error was not classified as duplicate")
	}
	if isDuplicateBlockProcessError(errors.New("already have block (orphan) 0xabc")) {
		t.Fatal("plain string error should not be classified as duplicate rule error")
	}
	if isDuplicateBlockProcessError(blockchain.RuleError{
		ErrorCode:   blockchain.ErrMissingParent,
		Description: "missing parent",
	}) {
		t.Fatal("non-duplicate rule error classified as duplicate")
	}
}

func TestPeerSyncInFlightReservations(t *testing.T) {
	ps := NewPeerSync(nil)
	hash := ethcommon.HexToHash("0x1")

	if !ps.reserveSyncBlock(hash) {
		t.Fatal("first reservation should succeed")
	}
	if ps.reserveSyncBlock(hash) {
		t.Fatal("duplicate reservation should be suppressed")
	}

	ps.releaseSyncBlocks([]ethcommon.Hash{hash})
	if !ps.reserveSyncBlock(hash) {
		t.Fatal("reservation should succeed after release")
	}
}
