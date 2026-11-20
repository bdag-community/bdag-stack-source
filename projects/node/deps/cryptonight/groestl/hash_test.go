// SPDX-License-Identifier: GPL-3.0-or-later
package groestl

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Standard published digests; exercise the hash.Hash streaming contract too.
func TestKnownAnswersAndStreaming(t *testing.T) {
	cases := []struct{ input, want string }{{"", "1a52d11d550039be16107f9c58db9ebcc417f16f736adb2502567119f0083467"},
		{"abc", "f3c1bb19c048801326a7efbcf16e3d7887446249829c379e1840d1a3a1e7d4d2"}}
	for _, v := range cases {
		want, err := hex.DecodeString(v.want)
		if err != nil {
			t.Fatal(err)
		}
		msg := []byte(v.input)
		h := New256()
		for _, b := range msg {
			h.Write([]byte{b})
		}
		if got := h.Sum(nil); !bytes.Equal(got, want) {
			t.Fatalf("%q: %x != %x", v.input, got, want)
		}
		if got := h.Sum([]byte{42}); len(got) != 33 || got[0] != 42 || !bytes.Equal(got[1:], want) {
			t.Fatal("Sum changed state or prefix")
		}
		h.Reset()
		h.Write(msg)
		if !bytes.Equal(h.Sum(nil), want) {
			t.Fatal("Reset mismatch")
		}
	}
}
