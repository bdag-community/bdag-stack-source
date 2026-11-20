package Blockdag

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestGraphStateTipsRoundTrip(t *testing.T) {
	tips := []*common.Hash{
		hashFromByte(1),
		hashFromByte(2),
		hashFromByte(3),
	}
	gs := NewGraphState()

	gs.SetTips(tips)
	got := gs.GetTipsList()

	if len(got) != len(tips) {
		t.Fatalf("tip count = %d, want %d", len(got), len(tips))
	}
	for i := range tips {
		if *got[i] != *tips[i] {
			t.Fatalf("tip %d = %s, want %s", i, got[i], tips[i])
		}
	}

	*tips[0] = *hashFromByte(9)
	if *gs.GetTipsList()[0] == *tips[0] {
		t.Fatal("SetTips should copy tip values")
	}
}

func hashFromByte(v byte) *common.Hash {
	var h common.Hash
	h[0] = v
	return &h
}
