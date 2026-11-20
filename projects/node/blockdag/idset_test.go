package Blockdag

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/ethereum/go-ethereum/common"
)

func Test_AddId(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	hs.Add(1)

	if !hs.Has(1) {
		t.FailNow()
	}
}

func Test_AddSetId(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	other := NewIdSet()
	other.Add(1)

	hs.AddSet(other)
	if !hs.Has(1) {
		t.FailNow()
	}
}

func Test_AddPairId(t *testing.T) {
	t.Parallel()
	var intData int = 123
	hs := NewIdSet()
	hs.AddPair(1, int(intData))

	if !hs.Has(1) || hs.Get(1).(int) != intData {
		t.FailNow()
	}
}

func Test_RemoveId(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	hs.Add(1)
	hs.Remove(1)

	if hs.Has(1) {
		t.FailNow()
	}
}

func Test_RemoveSetId(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	other := NewIdSet()
	other.Add(1)

	hs.AddSet(other)
	hs.RemoveSet(other)

	if hs.Has(1) {
		t.FailNow()
	}
}

func Test_SortListId(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	hl := IdSlice{}
	var hashNum uint = 5
	for i := uint(0); i < hashNum; i++ {
		hs.Add(i)
		hl = append(hl, i)
	}
	shs := hs.SortList(false)

	for i := uint(0); i < hashNum; i++ {
		if hl[i] != shs[i] {
			t.FailNow()
		}
	}
	rshs := hs.SortList(true)

	for i := uint(0); i < hashNum; i++ {
		if hl[i] != rshs[hashNum-i-1] {
			t.FailNow()
		}
	}
}

func Test_SortListHash(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	hl := BlockHashSlice{}
	var hashNum uint = 5
	for i := uint(0); i < hashNum; i++ {
		hashStr := fmt.Sprintf("%064x", i+1)
		h := hash.MustHexToHash(hashStr)
		block := &Block{id: i, hash: h.ToEVMHash()}
		hs.AddPair(block.GetID(), block)
		hl = append(hl, block)
	}
	shs := hs.SortHashList(false)
	if uint(len(shs)) != hashNum {
		t.Fatalf("expected %d ids in sorted list, got %d", hashNum, len(shs))
	}
	expectedAsc := append(BlockHashSlice{}, hl...)
	sort.Sort(expectedAsc)

	for i := uint(0); i < hashNum; i++ {
		if expectedAsc[i].GetID() != shs[i] {
			t.Fatalf("ascending hash sort mismatch at %d", i)
		}
	}
	rshs := hs.SortHashList(true)
	if uint(len(rshs)) != hashNum {
		t.Fatalf("expected %d ids in reverse-sorted list, got %d", hashNum, len(rshs))
	}

	for i := uint(0); i < hashNum; i++ {
		if expectedAsc[i].GetID() != rshs[hashNum-i-1] {
			t.Fatalf("reverse hash sort mismatch at %d", i)
		}
	}
}

func Test_ForId(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	var hashNum uint = 5
	for i := uint(0); i < hashNum; i++ {
		hs.AddPair(i, i)
	}
	for k, v := range hs.GetMap() {
		fmt.Printf("%d - %d\n", v, k)
	}
}

// DAG block data
type TestBlock struct {
}

// Return the hash
func (tb *TestBlock) GetHash() *common.Hash {
	return hash.ZeroHash.ToEVMHashPtr()
}

// Get all parents set,the dag block has more than one parent
func (tb *TestBlock) GetParents() []*common.Hash {
	return nil
}

func (tb *TestBlock) GetMainParent() *common.Hash {
	return nil
}

func (tb *TestBlock) GetTimestamp() int64 {
	return time.Now().Unix()
}

// Acquire the weight of block
func (tb *TestBlock) GetWeight() uint64 {
	return 1
}

func (tb *TestBlock) GetPriority() int {
	return MaxPriority
}

// func Test_SortListPriority(t *testing.T) {
// 	t.Parallel()
// 	hs := NewIdSet()
// 	hl := BlockPrioritySlice{}
// 	var hashNum uint = 5
// 	for i := uint(0); i < hashNum; i++ {
// 		hashStr := fmt.Sprintf("%d", i)
// 		h := hash.MustHexToDecodedHash(hashStr)
// 		block := &PhantomBlock{Block: &Block{id: i, hash: h, data: &TestBlock{}}, blueNum: i}
// 		hs.AddPair(block.GetID(), block)
// 		hl = append(hl, block)
// 	}

// 	shs := hs.SortPriorityList(false)

// 	for i := uint(0); i < hashNum; i++ {
// 		if hl[i].GetID() != shs[i] {
// 			t.FailNow()
// 		}
// 	}
// 	rshs := hs.SortPriorityList(true)

// 	for i := uint(0); i < hashNum; i++ {
// 		if hl[i].GetID() != rshs[hashNum-i-1] {
// 			t.FailNow()
// 		}
// 	}
// }

// func Test_SortListHeight(t *testing.T) {
// 	t.Parallel()
// 	hs := NewIdSet()
// 	hl := BlockHeightSlice{}
// 	var hashNum uint = 5
// 	for i := uint(0); i < hashNum; i++ {
// 		hashStr := fmt.Sprintf("%d", i)
// 		h := hash.MustHexToDecodedHash(hashStr)
// 		block := &Block{id: i, hash: h, height: i}
// 		hs.AddPair(block.GetID(), block)
// 		hl = append(hl, block)
// 	}
// 	shs := hs.SortHeightList(false)

// 	for i := uint(0); i < hashNum; i++ {
// 		if hl[i].GetID() != shs[i] {
// 			t.FailNow()
// 		}
// 	}
// 	rshs := hs.SortHeightList(true)

// 	for i := uint(0); i < hashNum; i++ {
// 		if hl[i].GetID() != rshs[hashNum-i-1] {
// 			t.FailNow()
// 		}
// 	}
// }

func TestIsDataEmpty(t *testing.T) {
	t.Parallel()
	hs := NewIdSet()
	hs.Add(1)
	hs.AddPair(2, int(2))

	if !hs.IsDataEmpty(1) {
		t.Fatalf("IsDataEmpty:%d = %v is not %v", 1, true, hs.IsDataEmpty(1))
	}
	if hs.IsDataEmpty(2) {
		t.Fatalf("IsDataEmpty:%d = %v is not %v", 1, false, hs.IsDataEmpty(1))
	}
}
