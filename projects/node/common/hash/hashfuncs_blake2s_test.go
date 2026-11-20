package hash

import (
	"bytes"
	"testing"

	blake2s "github.com/BlockdagNetworkLabs/bdag/blake2sext"
	"github.com/ethereum/go-ethereum/common"
)

func TestHashbdagBlake2sMatchesDigestPath(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		[]byte("blockdag"),
		bytes.Repeat([]byte{0x42}, 128),
	}

	for _, input := range inputs {
		got := HashbdagBlake2s(input)
		want := hashbdagBlake2sDigestPath(input)
		if got != want {
			t.Fatalf("HashbdagBlake2s(%x) = %s, want %s", input, got, want)
		}
	}
}

func BenchmarkHashbdagBlake2s(b *testing.B) {
	input := bytes.Repeat([]byte{0x42}, 128)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hashBenchSink = HashbdagBlake2s(input)
	}
}

func BenchmarkHashbdagBlake2sDigestPath(b *testing.B) {
	input := bytes.Repeat([]byte{0x42}, 128)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hashBenchSink = hashbdagBlake2sDigestPath(input)
	}
}

var hashBenchSink common.Hash

func hashbdagBlake2sDigestPath(input []byte) common.Hash {
	hash, err := blake2s.New256(nil)
	if err != nil {
		return common.Hash{}
	}
	if _, err := hash.Write(input); err != nil {
		return common.Hash{}
	}
	res := hash.Sum(nil)
	var result common.Hash
	copy(result[:], res)
	return result
}
