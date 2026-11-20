package p2p

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestRetrievePrivKeyFromFileTrimsWhitespace(t *testing.T) {
	keyHex := "0000000000000000000000000000000000000000000000000000000000000101"
	keyPath := filepath.Join(t.TempDir(), "network.key")
	if err := os.WriteFile(keyPath, []byte(keyHex+"\n"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	key, err := retrievePrivKeyFromFile(keyPath)
	if err != nil {
		t.Fatalf("retrieve key: %v", err)
	}
	raw, err := key.Raw()
	if err != nil {
		t.Fatalf("raw key: %v", err)
	}
	if got := hex.EncodeToString(raw); got != keyHex {
		t.Fatalf("raw key mismatch: got %s want %s", got, keyHex)
	}
}
