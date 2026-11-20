package snapshot

import (
	"testing"

	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	ethrawdb "github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
)

// stubAncientWriter implements ethdb.AncientWriter for import ordering tests.
type stubAncientWriter struct {
	appendKinds []string
	appendNums  []uint64
	modifyCalls int
}

func (s *stubAncientWriter) ModifyAncients(fn func(ethdb.AncientWriteOp) error) (int64, error) {
	s.modifyCalls++
	op := &stubAncientOp{s: s}
	return 0, fn(op)
}

func (s *stubAncientWriter) TruncateHead(n uint64) (uint64, error) { return n, nil }

func (s *stubAncientWriter) TruncateTail(n uint64) (uint64, error) { return n, nil }

func (s *stubAncientWriter) Sync() error { return nil }

type stubAncientOp struct {
	s *stubAncientWriter
}

func (o *stubAncientOp) Append(kind string, number uint64, item interface{}) error {
	return nil
}

func (o *stubAncientOp) AppendRaw(kind string, number uint64, item []byte) error {
	o.s.appendKinds = append(o.s.appendKinds, kind)
	o.s.appendNums = append(o.s.appendNums, number)
	return nil
}

func mustAncientKey(t *testing.T, kind string, num uint64) []byte {
	t.Helper()
	k, err := encodeAncientSnapKey(kind, num)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestImportBdagAncientSortOrder(t *testing.T) {
	stub := &stubAncientWriter{}
	// Deliberately out of canonical freeze order.
	recs := []keyValue{
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerDAGBlockTable, 0), Value: []byte{3}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerBlockTable, 0), Value: []byte{2}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerHeaderTable, 0), Value: []byte{1}},
	}
	if err := importAncientTables(SourceChain, stub, recs); err != nil {
		t.Fatal(err)
	}
	want := []string{
		bdagrawdb.ChainFreezerHeaderTable,
		bdagrawdb.ChainFreezerBlockTable,
		bdagrawdb.ChainFreezerDAGBlockTable,
	}
	if len(stub.appendKinds) != len(want) {
		t.Fatalf("got %v", stub.appendKinds)
	}
	for i := range want {
		if stub.appendKinds[i] != want[i] {
			t.Fatalf("index %d: got %q want %q", i, stub.appendKinds[i], want[i])
		}
	}
}

func TestImportEvmAncientSortOrder(t *testing.T) {
	stub := &stubAncientWriter{}
	recs := []keyValue{
		{Key: mustAncientKey(t, ethrawdb.ChainFreezerBodiesTable, 0), Value: []byte{3}},
		{Key: mustAncientKey(t, ethrawdb.ChainFreezerHashTable, 0), Value: []byte{1}},
		{Key: mustAncientKey(t, ethrawdb.ChainFreezerHeaderTable, 0), Value: []byte{2}},
	}
	if err := importAncientTables(SourceEVM, stub, recs); err != nil {
		t.Fatal(err)
	}
	want := []string{
		ethrawdb.ChainFreezerHashTable,
		ethrawdb.ChainFreezerHeaderTable,
		ethrawdb.ChainFreezerBodiesTable,
	}
	if len(stub.appendKinds) != len(want) {
		t.Fatalf("got %v", stub.appendKinds)
	}
	for i := range want {
		if stub.appendKinds[i] != want[i] {
			t.Fatalf("index %d: got %q want %q", i, stub.appendKinds[i], want[i])
		}
	}
}

func TestAncientStreamImporterFlushesCompleteItems(t *testing.T) {
	stub := &stubAncientWriter{}
	im := newAncientStreamImporter(SourceChain, stub, 1)

	recs := []keyValue{
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerDAGBlockTable, 0), Value: []byte{3}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerHeaderTable, 0), Value: []byte{1}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerBlockTable, 0), Value: []byte{2}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerDAGBlockTable, 1), Value: []byte{6}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerHeaderTable, 1), Value: []byte{4}},
		{Key: mustAncientKey(t, bdagrawdb.ChainFreezerBlockTable, 1), Value: []byte{5}},
	}
	for _, rec := range recs {
		if err := im.Add(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := im.Finish(); err != nil {
		t.Fatal(err)
	}

	wantKinds := []string{
		bdagrawdb.ChainFreezerHeaderTable,
		bdagrawdb.ChainFreezerBlockTable,
		bdagrawdb.ChainFreezerDAGBlockTable,
		bdagrawdb.ChainFreezerHeaderTable,
		bdagrawdb.ChainFreezerBlockTable,
		bdagrawdb.ChainFreezerDAGBlockTable,
	}
	wantNums := []uint64{0, 0, 0, 1, 1, 1}
	if stub.modifyCalls != 2 {
		t.Fatalf("modify calls: got %d want 2", stub.modifyCalls)
	}
	for i := range wantKinds {
		if stub.appendKinds[i] != wantKinds[i] || stub.appendNums[i] != wantNums[i] {
			t.Fatalf("append %d: got %s #%d want %s #%d", i, stub.appendKinds[i], stub.appendNums[i], wantKinds[i], wantNums[i])
		}
	}
}

var _ ethdb.AncientWriter = (*stubAncientWriter)(nil)
