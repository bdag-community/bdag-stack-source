package miner

import (
	blockdagconsensus "github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	svccommon "github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"path/filepath"
	"testing"
)

func newGBTGenerationRuntimeConsensus(t *testing.T) model.Consensus {
	t.Helper()
	original := params.ActiveNetParams
	params.ActiveNetParams = &params.PrivNetParam
	t.Cleanup(func() { params.ActiveNetParams = original })
	cfg := svccommon.DefaultConfig(t.TempDir())
	cfg.NoFileLogging = true
	cfg.DataDir = filepath.Join(cfg.DataDir, params.ActiveNetParams.Name)
	cfg.LogDir = t.TempDir()
	cfg.Miner = true
	cfg.No24hCheck = true
	cfg.GBTTimeOut = 3000
	db, err := chaindb.Open(cfg, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	consensus := blockdagconsensus.NewPure(cfg, db)
	if err := consensus.Init(); err != nil {
		t.Fatal(err)
	}
	chain := consensus.BlockChain().(*blockchain.BlockChain)
	if err := chain.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := chain.Stop(); err != nil {
			t.Error(err)
		}
	})
	return consensus
}
func newGBTGenerationRuntimeTemplate(t *testing.T, consensus model.Consensus, address ethcommon.Address) *coretypes.BlockTemplate {
	t.Helper()
	template, err := mining.NewBlockTemplateWithoutPendingTransactions(
		&mining.Policy{BlockMaxSize: uint32(consensus.Config().BlockMaxSize)},
		params.ActiveNetParams.Params, consensus.SigCache(), nil, consensus.MedianTimeSource(),
		consensus, address, nil, pow.SCRYPT, mining.CoinbaseFlagsStatic)
	if err != nil {
		t.Fatal(err)
	}
	return template
}
