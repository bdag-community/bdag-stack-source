package types_test

import (
	"bufio"
	"bytes"
	"fmt"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"io"
	"testing"
)

// Hiding ReadByte reproduces the public io.Reader contract, without a peer,
// storage fixture, signed transaction, or chain-validity assumption.
type compositionReader struct{ io.Reader }
type shortCompositionReader struct{ io.Reader }

func (r shortCompositionReader) Read(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return r.Reader.Read(p)
}

func TestNativeGenericReaderComposition(t *testing.T) {
	all := txLocationTransactions()
	readers := map[string]func([]byte) io.Reader{
		"bytes":           func(b []byte) io.Reader { return bytes.NewReader(b) },
		"generic":         func(b []byte) io.Reader { return compositionReader{bytes.NewReader(b)} },
		"short":           func(b []byte) io.Reader { return shortCompositionReader{bytes.NewReader(b)} },
		"caller-buffered": func(b []byte) io.Reader { return bufio.NewReader(bytes.NewReader(b)) },
	}
	for name, makeReader := range readers {
		for _, count := range []int{0, 1, 2, 4} {
			t.Run(fmt.Sprintf("%s/%d", name, count), func(t *testing.T) {
				original, wire := txLocationBlock(t, all[:count])
				suffix := []byte{0xab, 0xcd, 0xef}
				reader := makeReader(append(append([]byte(nil), wire...), suffix...))
				var decoded native.Block
				if err := decoded.Decode(reader, 0); err != nil {
					t.Fatal(err)
				}
				if len(decoded.Transactions) != count || decoded.Header.BlockHash() != original.Header.BlockHash() {
					t.Fatal("record identity changed")
				}
				for i, tx := range decoded.Transactions {
					if tx.Hash() != all[i].Hash() {
						t.Fatal("transaction identity changed")
					}
				}
				tail, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(tail, suffix) {
					t.Fatalf("trailing record bytes changed: %x, %v", tail, err)
				}
			})
		}
	}
}

func TestNativeGenericReaderConsecutiveBlocks(t *testing.T) {
	all := txLocationTransactions()
	_, first := txLocationBlock(t, all[:2])
	_, second := txLocationBlock(t, all)
	reader := compositionReader{bytes.NewReader(append(append([]byte(nil), first...), second...))}
	for _, count := range []int{2, 4} {
		var decoded native.Block
		if err := decoded.Decode(reader, 0); err != nil || len(decoded.Transactions) != count {
			t.Fatalf("composed block count=%d error=%v decoded=%d", count, err, len(decoded.Transactions))
		}
	}
	tail, err := io.ReadAll(reader)
	if err != nil || len(tail) != 0 {
		t.Fatal("unexpected trailing data")
	}
}
