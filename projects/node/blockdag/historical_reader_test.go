package Blockdag

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/ethereum/go-ethereum/common"
)

type historicalTestDB struct{ model.DataBase }

func (historicalTestDB) GetDAGBlock(id uint) ([]byte, error) {
	if id == 0 {
		return nil, fmt.Errorf("missing block")
	}
	parents := NewIdSet()
	parents.Add(id - 1)
	block := &PhantomBlock{Block: &Block{id: id, hash: common.BigToHash(new(big.Int).SetUint64(uint64(id))), mainParent: id - 1, layer: id, parents: parents, state: createBlockState(uint64(id))}}
	var output bytes.Buffer
	if err := block.Encode(&output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func TestHistoricalReadsDoNotPopulateDAGCache(t *testing.T) {
	bd := &BDAG{db: historicalTestDB{}, blocks: map[uint]IBlock{}}
	bd.instance = &Phantom{bd: bd}
	for id := uint(50000); id > 0; id-- {
		block, err := bd.ReadBlockById(id)
		if err != nil {
			t.Fatal(err)
		}
		if block.GetID() != id || block.GetMainParent() != id-1 || block.GetLayer() != id {
			t.Fatalf("metadata mismatch for %d", id)
		}
		if len(bd.blocks) != 0 {
			t.Fatalf("historical read populated DAG cache: %d entries", len(bd.blocks))
		}
	}
	if _, err := bd.ReadBlockById(0); err == nil {
		t.Fatal("missing block accepted")
	}
	if _, err := bd.ReadBlockById(MaxId); err == nil {
		t.Fatal("invalid ID accepted")
	}
	cached := &Block{id: 7, layer: 99}
	bd.blocks[7] = cached
	got, err := bd.ReadBlockById(7)
	if err != nil || got != cached {
		t.Fatal("cached metadata not preserved")
	}
	if _, err := bd.ReadBlockById(8); err != nil {
		t.Fatal(err)
	}
	if cached.HasChildren() || len(bd.blocks) != 1 {
		t.Fatal("historical read linked into live cache")
	}
	legacy := bd.GetBlockById(9)
	detached, err := bd.ReadBlockById(10)
	if legacy == nil || err != nil || detached.GetMainParent() != legacy.GetID() {
		t.Fatal("persisted ancestry semantics changed")
	}
}
