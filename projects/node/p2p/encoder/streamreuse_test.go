package encoder

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"

	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
)

// newTestGetBlocks builds a GetBlocks message with n locator hashes, each a
// distinct 32-byte value, so decoded content can be checked precisely and
// encoded size is deterministic: 4 + 32*n bytes (see GetBlocks.SizeSSZ).
func newTestGetBlocks(n int) *pb.GetBlocks {
	locator := make([]*pb.Hash, n)
	for i := range locator {
		locator[i] = &pb.Hash{Hash: bytesOf(byte(i%256), 32)}
	}
	return &pb.GetBlocks{Locator: locator}
}

func assertGetBlocksEqual(t *testing.T, got, want *pb.GetBlocks) {
	t.Helper()
	if len(got.Locator) != len(want.Locator) {
		t.Fatalf("locator len = %d, want %d", len(got.Locator), len(want.Locator))
	}
	for i := range want.Locator {
		if !bytes.Equal(got.Locator[i].Hash, want.Locator[i].Hash) {
			t.Fatalf("locator[%d] = %x, want %x", i, got.Locator[i].Hash, want.Locator[i].Hash)
		}
	}
}

// TestDecodeWithMaxLengthSequentialMessages verifies that DecodeWithMaxLength
// bounds its read to the varint-declared length instead of reading to EOF,
// so multiple messages written back-to-back into one stream can each be
// decoded in turn without the writer ever closing/half-closing in between.
// This is the load-bearing property that a reused (not one-shot) stream
// depends on.
func TestDecodeWithMaxLengthSequentialMessages(t *testing.T) {
	for _, snappy := range []bool{false, true} {
		snappy := snappy
		t.Run(mapBoolName(snappy), func(t *testing.T) {
			enc := SszNetworkEncoder{UseSnappyCompression: snappy}

			msgs := []*pb.GetBlocks{newTestGetBlocks(0), newTestGetBlocks(3), newTestGetBlocks(50)}

			var buf bytes.Buffer
			for _, m := range msgs {
				if _, err := enc.EncodeWithMaxLength(&buf, m); err != nil {
					t.Fatalf("encode: %v", err)
				}
			}

			for i, want := range msgs {
				var got pb.GetBlocks
				if err := enc.DecodeWithMaxLength(&buf, &got); err != nil {
					t.Fatalf("decode message %d: %v", i, err)
				}
				assertGetBlocksEqual(t, &got, want)
			}

			if buf.Len() != 0 {
				t.Fatalf("expected no leftover bytes after decoding all messages, got %d remaining", buf.Len())
			}
		})
	}
}

// TestDecodeWithMaxLengthVariousSizes sweeps a range of payload sizes
// (including small and near the GetBlocks.Locator ssz-max of 2000) to guard
// against an off-by-one in the exact-length read.
func TestDecodeWithMaxLengthVariousSizes(t *testing.T) {
	for _, snappy := range []bool{false, true} {
		snappy := snappy
		t.Run(mapBoolName(snappy), func(t *testing.T) {
			enc := SszNetworkEncoder{UseSnappyCompression: snappy}
			for _, n := range []int{0, 1, 10, 100, 500, 2000} {
				want := newTestGetBlocks(n)
				var buf bytes.Buffer
				if _, err := enc.EncodeWithMaxLength(&buf, want); err != nil {
					t.Fatalf("n=%d encode: %v", n, err)
				}
				var got pb.GetBlocks
				if err := enc.DecodeWithMaxLength(&buf, &got); err != nil {
					t.Fatalf("n=%d decode: %v", n, err)
				}
				assertGetBlocksEqual(t, &got, want)
				if buf.Len() != 0 {
					t.Fatalf("n=%d: expected no leftover bytes, got %d", n, buf.Len())
				}
			}
		})
	}
}

// TestDecodeWithMaxLengthTruncatedStream verifies that a stream cut short
// mid-message produces an error rather than a corrupted/short successful
// decode.
func TestDecodeWithMaxLengthTruncatedStream(t *testing.T) {
	for _, snappy := range []bool{false, true} {
		snappy := snappy
		t.Run(mapBoolName(snappy), func(t *testing.T) {
			enc := SszNetworkEncoder{UseSnappyCompression: snappy}
			want := newTestGetBlocks(50)

			var full bytes.Buffer
			if _, err := enc.EncodeWithMaxLength(&full, want); err != nil {
				t.Fatalf("encode: %v", err)
			}

			truncated := full.Bytes()[:full.Len()-5]
			var got pb.GetBlocks
			err := enc.DecodeWithMaxLength(bytes.NewReader(truncated), &got)
			if err == nil {
				t.Fatalf("expected error decoding truncated stream, got nil")
			}
			if err != io.ErrUnexpectedEOF && err != io.EOF {
				// Some snappy-path failures surface as decompression errors
				// rather than a bare EOF; either is an acceptable failure
				// mode here, we just must not succeed.
				t.Logf("truncated decode failed with non-EOF error (acceptable): %v", err)
			}
		})
	}
}

// TestDecodeWithMaxLengthEOFClassification pins down the exact distinction
// a caller reading a stream that may carry multiple sequential messages
// depends on: a bare io.EOF must mean "the reader had nothing left to give,
// right at a message boundary" (safe to treat as "no more messages"), while
// any failure after that point -- even zero further bytes arriving -- must
// never be a bare io.EOF, since the caller has already committed to
// decoding one specific message and any interruption is a truncation, not a
// clean end.
func TestDecodeWithMaxLengthEOFClassification(t *testing.T) {
	enc := SszNetworkEncoder{UseSnappyCompression: false}
	want := newTestGetBlocks(10)

	var full bytes.Buffer
	if _, err := enc.EncodeWithMaxLength(&full, want); err != nil {
		t.Fatalf("encode: %v", err)
	}
	encoded := full.Bytes()

	t.Run("clean boundary (empty reader) is bare EOF", func(t *testing.T) {
		var got pb.GetBlocks
		err := enc.DecodeWithMaxLength(bytes.NewReader(nil), &got)
		if !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("expected bare io.EOF, got %v", err)
		}
	})

	t.Run("truncated mid-varint is not a bare EOF", func(t *testing.T) {
		// A GetBlocks with 10 hashes encodes to well over 127 bytes, so its
		// varint length prefix is at least 2 bytes; cut after the first.
		var got pb.GetBlocks
		err := enc.DecodeWithMaxLength(bytes.NewReader(encoded[:1]), &got)
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}
		if errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("mid-varint truncation must not surface as a bare io.EOF, got %v", err)
		}
	})

	t.Run("varint complete but zero payload bytes is not a bare EOF", func(t *testing.T) {
		// Cut the stream exactly at the end of the varint prefix, before
		// any payload byte.
		varintLen := len(encoded) - int(newTestGetBlocks(10).SizeSSZ())
		var got pb.GetBlocks
		err := enc.DecodeWithMaxLength(bytes.NewReader(encoded[:varintLen]), &got)
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}
		if errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("zero-payload-after-varint must not surface as a bare io.EOF, got %v", err)
		}
	})
}

// TestDecodeWithMaxLengthPoolReuseNoCorruption exercises bufReaderPool
// (the pooled *snappy.Reader used by DecodeWithMaxLength) heavily and
// concurrently, since stream reuse means the pool now services many
// sequential decodes per stream instead of exactly one. Run with -race.
func TestDecodeWithMaxLengthPoolReuseNoCorruption(t *testing.T) {
	enc := SszNetworkEncoder{UseSnappyCompression: true}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				n := (g*50 + i) % 40
				want := newTestGetBlocks(n)
				var buf bytes.Buffer
				if _, err := enc.EncodeWithMaxLength(&buf, want); err != nil {
					t.Errorf("goroutine %d iter %d encode: %v", g, i, err)
					return
				}
				var got pb.GetBlocks
				if err := enc.DecodeWithMaxLength(&buf, &got); err != nil {
					t.Errorf("goroutine %d iter %d decode: %v", g, i, err)
					return
				}
				assertGetBlocksEqual(t, &got, want)
			}
		}()
	}
	wg.Wait()
}

func mapBoolName(snappy bool) string {
	if snappy {
		return "snappy"
	}
	return "raw"
}
