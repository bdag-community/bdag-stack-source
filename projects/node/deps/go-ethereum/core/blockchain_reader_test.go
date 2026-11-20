package core

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

func TestHasStateSentinelRootsDoNotOpenTrie(t *testing.T) {
	_, _, chain, err := newCanonical(ethash.NewFaker(), 0, true, rawdb.HashScheme)
	if err != nil {
		t.Fatalf("failed to create test chain: %v", err)
	}
	defer chain.Stop()

	var logs bytes.Buffer
	previousLogger := log.Root()
	log.SetDefault(log.NewLogger(log.NewTerminalHandlerWithLevel(&logs, log.LevelTrace, false)))
	defer log.SetDefault(previousLogger)

	for _, root := range []common.Hash{{}, types.EmptyRootHash} {
		if !chain.HasState(root) {
			t.Fatalf("expected sentinel root %s to be treated as available", root)
		}
	}
	if strings.Contains(logs.String(), "Zero state root hash") {
		t.Fatalf("HasState opened the trie for a sentinel root: %s", logs.String())
	}
}
