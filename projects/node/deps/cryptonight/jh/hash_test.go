// SPDX-License-Identifier: GPL-3.0-or-later
package jh

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Standard published digests; exercise the hash.Hash streaming contract too.
func TestKnownAnswersAndStreaming(t *testing.T) {
	cases := []struct{ input, want string }{{"", "46e64619c18bb0a92a5e87185a47eef83ca747b8fcc8e1412921357e326df434"}}
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
