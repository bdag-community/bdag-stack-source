package bdag

import (
	"fmt"
	"math/big"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	mbdag "github.com/BlockdagNetworkLabs/bdag/consensus/model/bdag"
	qtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/params"
)

func makeHeader(cfg *ethconfig.Config, parent *types.Block, state *state.StateDB, timestamp int64, gaslimit uint64) *types.Header {
	headerTime := ResolveEVMBlockTimestamp(parent.Time(), timestamp, cfg.Genesis.Config.ChainID)

	header := &types.Header{
		Root:       state.IntermediateRoot(cfg.Genesis.Config.IsEIP158(parent.Number())),
		ParentHash: parent.Hash(),
		Coinbase:   parent.Coinbase(),
		Difficulty: common.Big1,
		GasLimit:   gaslimit,
		Number:     new(big.Int).Add(parent.Number(), common.Big1),
		Time:       headerTime,
	}
	if cfg.Genesis.Config.IsLondon(header.Number) {
		header.BaseFee = eip1559.CalcBaseFee(cfg.Genesis.Config, parent.Header())
		if !cfg.Genesis.Config.IsLondon(parent.Number()) {
			parentGasLimit := parent.GasLimit() * cfg.Genesis.Config.ElasticityMultiplier()
			header.GasLimit = core.CalcGasLimit(parentGasLimit, parentGasLimit)
		}
	}
	if mparams.IsMainnetChainID(cfg.Genesis.Config.ChainID) {
		header.GasLimit = mparams.MainnetFixedGasLimit
	}
	return header
}

type fakeChainReader struct {
	config *params.ChainConfig
}

// Config returns the chain configuration.
func (cr *fakeChainReader) Config() *params.ChainConfig {
	return cr.config
}

func (cr *fakeChainReader) CurrentHeader() *types.Header                            { return nil }
func (cr *fakeChainReader) GetHeaderByNumber(number uint64) *types.Header           { return nil }
func (cr *fakeChainReader) GetHeaderByHash(hash common.Hash) *types.Header          { return nil }
func (cr *fakeChainReader) GetHeader(hash common.Hash, number uint64) *types.Header { return nil }
func (cr *fakeChainReader) GetBlock(hash common.Hash, number uint64) *types.Block   { return nil }
func (cr *fakeChainReader) GetTd(hash common.Hash, number uint64) *big.Int          { return nil }

func BuildEVMBlock(block *qtypes.SerializedBlock) (*mbdag.Block, error) {
	result := &mbdag.Block{Id: block.Hash(), Txs: block.Block().Transactions, Time: block.Block().Header.Timestamp}

	return result, nil
}

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
	commitInterruptTimeout
)

// signalToErr converts the interruption signal to a concrete error type for return.
// The given signal must be a valid interruption signal.
func signalToErr(signal int32) error {
	switch signal {
	case commitInterruptNewHead:
		return errBlockInterruptedByNewHead
	case commitInterruptResubmit:
		return errBlockInterruptedByRecommit
	case commitInterruptTimeout:
		return errBlockInterruptedByTimeout
	default:
		panic(fmt.Errorf("undefined signal %d", signal))
	}
}
