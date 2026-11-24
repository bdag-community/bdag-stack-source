package blockchain

import (
	"testing"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/ethereum/go-ethereum/common"
)

type proofTestBlock struct {
	blockdag.IBlock
	id, height, layer uint
	hash              common.Hash
	parent            *proofTestBlock
}

func (b *proofTestBlock) GetID() uint           { return b.id }
func (b *proofTestBlock) GetHeight() uint       { return b.height }
func (b *proofTestBlock) GetLayer() uint        { return b.layer }
func (b *proofTestBlock) GetHash() *common.Hash { return &b.hash }

func TestUpgradeReorgProofRequiresExactAncestry(t *testing.T) {
	ancestor := &proofTestBlock{id: 1, height: 1, layer: 10, hash: common.HexToHash("01")}
	old := &proofTestBlock{id: 2, height: 2, layer: 11, hash: common.HexToHash("02"), parent: ancestor}
	oldTip := &proofTestBlock{id: 3, height: 3, layer: 12, hash: common.HexToHash("03"), parent: old}
	newBlock := &proofTestBlock{id: 4, height: 2, layer: 11, hash: common.HexToHash("04"), parent: ancestor}
	newTip := &proofTestBlock{id: 5, height: 3, layer: 12, hash: common.HexToHash("05"), parent: newBlock}
	parent := func(b blockdag.IBlock) (blockdag.IBlock, bool) {
		p := b.(*proofTestBlock).parent
		if p == nil {
			return nil, false
		}
		return p, true
	}
	marker := upgradeRebuildState{TipID: uint64(oldTip.id), TipLayer: uint64(oldTip.layer), TipHash: oldTip.hash}
	if !upgradeMarkerIsAncestor(marker, oldTip, parent) {
		t.Fatal("checkpoint was not ancestor of old tip")
	}
	if upgradeMarkerIsAncestor(marker, newTip, parent) {
		t.Fatal("reorged tip falsely accepted as descendant")
	}
	changed, ok := selectedParentReorgChangedFrom(oldTip, newTip, parent)
	if !ok || changed != 11 {
		t.Fatalf("changed layer=%d proved=%v", changed, ok)
	}
	missing := func(blockdag.IBlock) (blockdag.IBlock, bool) { return nil, false }
	if _, ok := selectedParentReorgChangedFrom(oldTip, newTip, missing); ok {
		t.Fatal("missing ancestry was accepted")
	}
	wrongHash := marker
	wrongHash.TipHash = common.HexToHash("abcd")
	if upgradeMarkerIsAncestor(wrongHash, oldTip, parent) {
		t.Fatal("mismatched checkpoint hash was accepted")
	}
}

func TestUpgradeWindowScanFailsClosedOnMissingData(t *testing.T) {
	first := &proofTestBlock{id: 1, height: 1, layer: 10}
	second := &proofTestBlock{id: 2, height: 2, layer: 11, parent: first}
	third := &proofTestBlock{id: 3, height: 3, layer: 12, parent: second}
	def := upgradeRebuildDefinition{Name: "vote", BitMask: 1, WindowSize: 3, Threshold: 2, StartLayer: 10}
	window := func() []reconcileWindow {
		return []reconcileWindow{{definition: def, start: 10, end: 12, tally: newUpgradeSignalTally(3)}}
	}
	parent := func(b blockdag.IBlock) (blockdag.IBlock, error) {
		p := b.(*proofTestBlock).parent
		if p == nil {
			return nil, nil
		}
		return p, nil
	}
	header := func(blockdag.IBlock) (uint32, bool) { return 1, true }
	windows := window()
	count, err := scanUpgradeReconcileWindows(third, windows, 10, header, parent)
	if err != nil || count != 3 || windows[0].tally.yesLayers != 3 {
		t.Fatalf("count=%d tally=%d err=%v", count, windows[0].tally.yesLayers, err)
	}
	missingHeader := func(b blockdag.IBlock) (uint32, bool) { return 1, b.GetID() != 2 }
	if _, err := scanUpgradeReconcileWindows(third, window(), 10, missingHeader, parent); err == nil {
		t.Fatal("missing header accepted")
	}
	missingParent := func(blockdag.IBlock) (blockdag.IBlock, error) { return nil, nil }
	if _, err := scanUpgradeReconcileWindows(third, window(), 10, header, missingParent); err == nil {
		t.Fatal("missing parent accepted")
	}
	badParent := func(b blockdag.IBlock) (blockdag.IBlock, error) { return b, nil }
	if _, err := scanUpgradeReconcileWindows(third, window(), 10, header, badParent); err == nil {
		t.Fatal("non-decreasing parent height accepted")
	}
}
