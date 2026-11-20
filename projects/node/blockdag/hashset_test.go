package Blockdag

import (
	"fmt"
	"sort"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/ethereum/go-ethereum/common"
)

func Test_Add(t *testing.T) {
	t.Parallel()
	hs := NewHashSet()
	hs.Add(hash.ZeroHash.ToEVMHashPtr())

	if !hs.Has(hash.ZeroHash.ToEVMHashPtr()) {
		t.FailNow()
	}
}

func Test_AddSet(t *testing.T) {
	t.Parallel()
	hs := NewHashSet()
	other := NewHashSet()
	other.Add(hash.ZeroHash.ToEVMHashPtr())

	hs.AddSet(other)
	if !hs.Has(hash.ZeroHash.ToEVMHashPtr()) {
		t.FailNow()
	}
}

func Test_AddPair(t *testing.T) {
	t.Parallel()
	var intData int = 123
	hs := NewHashSet()
	hs.AddPair(hash.ZeroHash.ToEVMHashPtr(), int(intData))

	if !hs.Has(hash.ZeroHash.ToEVMHashPtr()) || hs.Get(hash.ZeroHash.ToEVMHashPtr()).(int) != intData {
		t.FailNow()
	}
}

func Test_Remove(t *testing.T) {
	t.Parallel()
	hs := NewHashSet()
	hs.Add(hash.ZeroHash.ToEVMHashPtr())
	hs.Remove(hash.ZeroHash.ToEVMHashPtr())

	if hs.Has(hash.ZeroHash.ToEVMHashPtr()) {
		t.FailNow()
	}
}

func Test_RemoveSet(t *testing.T) {
	t.Parallel()
	hs := NewHashSet()
	other := NewHashSet()
	other.Add(hash.ZeroHash.ToEVMHashPtr())

	hs.AddSet(other)
	hs.RemoveSet(other)

	if hs.Has(hash.ZeroHash.ToEVMHashPtr()) {
		t.FailNow()
	}
}

func Test_SortList(t *testing.T) {
	t.Parallel()
	hs := NewHashSet()
	hl := HashSlice{}
	var hashNum int = 5
	for i := 0; i < hashNum; i++ {
		hashStr := fmt.Sprintf("%064x", i+1)
		h := hash.MustHexToHash(hashStr)
		evmHash := h.ToEVMHashPtr()
		hs.Add(evmHash)
		hl = append(hl, evmHash)
	}
	shs := hs.SortList(false)
	if len(shs) != hashNum {
		t.Fatalf("expected %d hashes in sorted list, got %d", hashNum, len(shs))
	}
	expectedAsc := append(HashSlice{}, hl...)
	sort.Sort(expectedAsc)

	for i := 0; i < hashNum; i++ {
		if expectedAsc[i].Cmp(*shs[i]) != 0 {
			t.Fatalf("ascending sort mismatch at %d", i)
		}
	}
	rshs := hs.SortList(true)
	if len(rshs) != hashNum {
		t.Fatalf("expected %d hashes in reverse-sorted list, got %d", hashNum, len(rshs))
	}

	for i := 0; i < hashNum; i++ {
		if expectedAsc[i].Cmp(*rshs[hashNum-i-1]) != 0 {
			t.Fatalf("reverse sort mismatch at %d", i)
		}
	}
}

func Test_For(t *testing.T) {
	t.Parallel()
	hs := NewHashSet()
	var hashNum int = 5
	for i := 0; i < hashNum; i++ {
		hashStr := fmt.Sprintf("%d", i)
		h := hash.MustHexToDecodedHash(hashStr)
		hs.AddPair(h.ToEVMHashPtr(), hashStr)
	}
	for k, v := range hs.GetMap() {
		fmt.Printf("%s - %s\n", v.(string), k)
	}
}

func TestHashSetOrderingAndDuplicateOracle(t *testing.T) {
	a := common.HexToHash("0x03")
	b := common.HexToHash("0x01")
	c := common.HexToHash("0x02")
	inputs := []*common.Hash{
		&a, &b, &a, &c,
	}
	hs := NewHashSet()
	for _, input := range inputs {
		hs.Add(input)
	}
	expected := []common.Hash{*inputs[0], *inputs[1], *inputs[3]}
	sort.Slice(expected, func(i, j int) bool { return expected[i].Cmp(expected[j]) < 0 })
	got := hs.SortList(false)
	if len(got) != len(expected) {
		t.Fatalf("unique hash count = %d, want %d", len(got), len(expected))
	}
	for i := range expected {
		if *got[i] != expected[i] {
			t.Fatalf("sorted hash %d = %s, want %s", i, got[i], expected[i].String())
		}
	}
}
