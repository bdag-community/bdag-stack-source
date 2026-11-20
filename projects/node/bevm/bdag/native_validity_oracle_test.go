package bdag

import (
	"math/big"
	"testing"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	qstate "github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type nativeOracleConsensus struct {
	model.Consensus
	chain model.BlockChain
}

func (c nativeOracleConsensus) BlockChain() model.BlockChain {
	return c.chain
}

type nativeOracleChain struct {
	model.BlockChain
	number uint64
	block  blockdag.IBlock
}

func (c nativeOracleChain) GetBlockByNumber(number uint64) blockdag.IBlock {
	if number != c.number {
		return nil
	}
	return c.block
}

type nativeOracleBlock struct {
	blockdag.IBlock
	state model.BlockState
}

func (b nativeOracleBlock) GetState() model.BlockState {
	return b.state
}

func TestIsNativeValidEVMBlock(t *testing.T) {
	header := &types.Header{
		Number: big.NewInt(7),
		Root:   common.HexToHash("0x1234"),
	}
	st := &qstate.BlockState{}
	st.SetEVM(header)
	block := nativeOracleBlock{state: st}
	chain := nativeOracleChain{number: 7, block: block}
	bc := &BdagChain{consensus: nativeOracleConsensus{chain: chain}}

	if valid, known := bc.IsNativeValidEVMBlock(header.Hash(), 7, header.Root); !valid || !known {
		t.Fatalf("valid mapping got valid=%v known=%v", valid, known)
	}
	if valid, known := bc.IsNativeValidEVMBlock(common.HexToHash("0xbeef"), 7, header.Root); valid || !known {
		t.Fatalf("hash mismatch got valid=%v known=%v", valid, known)
	}
	if valid, known := bc.IsNativeValidEVMBlock(header.Hash(), 99, header.Root); valid || known {
		t.Fatalf("unknown mapping got valid=%v known=%v", valid, known)
	}

	st.Invalid()
	if valid, known := bc.IsNativeValidEVMBlock(header.Hash(), 7, header.Root); valid || !known {
		t.Fatalf("invalid native state got valid=%v known=%v", valid, known)
	}
}
