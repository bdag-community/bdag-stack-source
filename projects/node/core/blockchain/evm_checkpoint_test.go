package blockchain

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestValidateBuiltEVMCheckpoint(t *testing.T) {
	root := common.HexToHash("0x01")
	block := gethtypes.NewBlockWithHeader(&gethtypes.Header{
		Number: big.NewInt(100),
		Root:   root,
	})

	if err := validateBuiltEVMCheckpoint(block, &EVMCheckpoint{
		Number: 100,
		Hash:   block.Hash(),
		Root:   root,
	}); err != nil {
		t.Fatalf("matching checkpoint rejected: %v", err)
	}

	if err := validateBuiltEVMCheckpoint(block, &EVMCheckpoint{
		Number: 101,
		Hash:   common.HexToHash("0x02"),
		Root:   common.HexToHash("0x03"),
	}); err != nil {
		t.Fatalf("non-matching height should not be checked: %v", err)
	}

	err := validateBuiltEVMCheckpoint(block, &EVMCheckpoint{
		Number: 100,
		Hash:   common.HexToHash("0x02"),
		Root:   root,
	})
	if err == nil {
		t.Fatal("mismatched checkpoint accepted")
	}
	var ruleErr RuleError
	if !errors.As(err, &ruleErr) || ruleErr.ErrorCode != ErrEVMCheckpointMismatch {
		t.Fatalf("error = %T %[1]v, want ErrEVMCheckpointMismatch", err)
	}

	err = validateBuiltEVMCheckpoint(block, &EVMCheckpoint{
		Number: 100,
		Hash:   block.Hash(),
		Root:   common.HexToHash("0x04"),
	})
	if err == nil {
		t.Fatal("mismatched checkpoint root accepted")
	}
	if !errors.As(err, &ruleErr) || ruleErr.ErrorCode != ErrEVMCheckpointMismatch {
		t.Fatalf("error = %T %[1]v, want ErrEVMCheckpointMismatch", err)
	}
}
