package blockchain

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/params"
)

func TestBlockClone(t *testing.T) {
	block := params.PrivNetParam.GenesisBlock
	cloneBlock, err := block.Block().Clone()
	if err != nil {
		t.Fatal(err)
	}
	cbHash := cloneBlock.BlockHash()
	if block.Hash().Cmp(cbHash) != 0 {
		t.Fatalf("block hash:%s != %s (expect)", cbHash.String(), block.Hash().String())
	}
}
