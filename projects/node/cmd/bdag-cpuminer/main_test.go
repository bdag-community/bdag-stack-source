package main

import (
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestHeaderFromTemplateCopiesPowRoot(t *testing.T) {
	tpl := &corejson.GetBlockTemplateResult{
		Version:          0x30000000,
		PreviousHash:     "0x0100000000000000000000000000000000000000000000000000000000000000",
		TxRoot:           "0x0200000000000000000000000000000000000000000000000000000000000000",
		StateRoot:        "0x0300000000000000000000000000000000000000000000000000000000000000",
		PowRoot:          "0x0400000000000000000000000000000000000000000000000000000000000000",
		CurTime:          time.Unix(1_700_000_000, 0).Unix(),
		Height:           42,
		CoinbaseAddress:  common.HexToAddress("0x1234"),
		Reward:           100,
		PowDiffReference: corejson.PowDiffReference{NBits: "1"},
	}

	header, err := headerFromTemplate(tpl, pow.SCRYPT, 7, "mainnet")
	if err != nil {
		t.Fatalf("headerFromTemplate: %v", err)
	}
	if got, want := header.PowRoot, common.HexToHash(tpl.PowRoot); got != want {
		t.Fatalf("PowRoot = %s, want %s", got, want)
	}
}

func TestBadMinerCorruptStateRootDefaultsToProtocol46Gate(t *testing.T) {
	t.Setenv("BDAG_TEST_BAD_MINER_CORRUPT_STATE_ROOT", "1")
	t.Setenv("BDAG_TEST_BAD_MINER_MIN_PROTOCOL", "")

	cfg := configFromEnv()
	if !cfg.corruptStateRoot {
		t.Fatal("corruptStateRoot = false, want true")
	}
	if got, want := cfg.badMinerMinProtocol, protocol.EVMCheckpointProtocolVersion; got != want {
		t.Fatalf("badMinerMinProtocol = %d, want %d", got, want)
	}
}

func TestCorruptStateRootChangesRoot(t *testing.T) {
	header := &coretypes.BlockHeader{
		StateRoot: common.HexToHash("0x0100000000000000000000000000000000000000000000000000000000000000"),
	}
	before := header.StateRoot

	corruptStateRoot(header)

	if header.StateRoot == before {
		t.Fatal("state root did not change")
	}
}

func TestBlockFromTemplateBuildsFullBlock(t *testing.T) {
	tx := ethtypes.NewTransaction(1, common.HexToAddress("0x1234"), big.NewInt(1), 21_000, big.NewInt(1), nil)
	txBytes, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal tx: %v", err)
	}
	parent := "0x0100000000000000000000000000000000000000000000000000000000000000"
	tpl := &corejson.GetBlockTemplateResult{
		Parents: []corejson.GetBlockTemplateResultPt{
			{Hash: parent},
		},
		Transactions: []corejson.GetBlockTemplateResultTx{
			{Data: hex.EncodeToString(txBytes)},
		},
	}
	header := &coretypes.BlockHeader{
		Version:    1,
		ParentRoot: common.HexToHash(parent),
		StateRoot:  common.HexToHash("0x0200000000000000000000000000000000000000000000000000000000000000"),
	}

	block, err := blockFromTemplate(tpl, header)
	if err != nil {
		t.Fatalf("blockFromTemplate: %v", err)
	}
	if len(block.Parents) != 1 || *block.Parents[0] != common.HexToHash(parent) {
		t.Fatalf("parents = %v, want %s", block.Parents, parent)
	}
	if len(block.Transactions) != 1 || block.Transactions[0].Hash() != tx.Hash() {
		t.Fatalf("transactions = %v, want tx %s", block.Transactions, tx.Hash())
	}
	if block.Header.StateRoot != header.StateRoot {
		t.Fatalf("StateRoot = %s, want %s", block.Header.StateRoot, header.StateRoot)
	}
}
