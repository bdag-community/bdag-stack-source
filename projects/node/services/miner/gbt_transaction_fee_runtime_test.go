package miner

import (
	"fmt"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	blockdagconsensus "github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	bdagtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	svccommon "github.com/BlockdagNetworkLabs/bdag/services/common"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestGBTTransactionFeeActualPendingBuilder(t *testing.T) {
	originalActiveNetParams := params.ActiveNetParams
	params.ActiveNetParams = &params.PrivNetParam
	t.Cleanup(func() {
		params.ActiveNetParams = originalActiveNetParams
	})

	senderKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)

	cfg := svccommon.DefaultConfig(t.TempDir())
	cfg.NoFileLogging = true
	cfg.DataDir = filepath.Join(cfg.DataDir, params.ActiveNetParams.Name)
	cfg.LogDir = t.TempDir()
	cfg.Miner = true
	cfg.No24hCheck = true
	cfg.GBTTimeOut = 3000
	cfg.GenesisAlloc = []string{fmt.Sprintf("%s:100", sender.Hex())}

	db, err := chaindb.Open(cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("open temp chain db: %v", err)
	}
	t.Cleanup(db.Close)

	consensus := blockdagconsensus.NewPure(cfg, db)
	if err := consensus.Init(); err != nil {
		t.Fatalf("init consensus: %v", err)
	}

	chain, ok := consensus.BlockChain().(*blockchain.BlockChain)
	if !ok {
		t.Fatalf("consensus chain type = %T, want *blockchain.BlockChain", consensus.BlockChain())
	}
	if err := chain.Start(); err != nil {
		t.Fatalf("start blockchain: %v", err)
	}
	t.Cleanup(func() {
		if err := chain.Stop(); err != nil {
			t.Errorf("stop blockchain: %v", err)
		}
	})

	evmChain := chain.EVMChain()
	if evmChain == nil || evmChain.ETHChain() == nil || evmChain.ETHChain().Ether() == nil {
		t.Fatal("EVM chain backend is not initialized")
	}
	evmChain.BdagPool().SetNotify(noopMiningRuntimeNotify{})

	ethBackend := evmChain.ETHChain().Ether()
	signer := ethtypes.LatestSigner(ethBackend.BlockChain().Config())
	recipient := ethcommon.HexToAddress("0x1000000000000000000000000000000000000001")
	baseFee := ethBackend.BlockChain().CurrentBlock().BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(1_000_000_000)
	}
	gasPrice := new(big.Int).Mul(baseFee, big.NewInt(2))
	tx := ethtypes.NewTransaction(0, recipient, big.NewInt(1), 21_000, gasPrice, nil)
	signedTx, err := ethtypes.SignTx(tx, signer, senderKey)
	if err != nil {
		t.Fatalf("sign pending transaction: %v", err)
	}

	errs := ethBackend.TxPool().Add([]*ethtypes.Transaction{signedTx}, true, true)
	if len(errs) != 1 || errs[0] != nil {
		t.Fatalf("add pending transaction errors = %v", errs)
	}
	if pending := ethBackend.TxPool().Pending(txpool.PendingFilter{}); len(pending) != 1 {
		t.Fatalf("pending account count = %d, want 1", len(pending))
	}
	if err := evmChain.BdagPool().ResetTemplateForMining(); err != nil {
		t.Fatalf("reset pending template: %v", err)
	}

	m := newRemoteGBTResultTestMiner()
	template, err := mining.NewBlockTemplate(
		&mining.Policy{BlockMaxSize: uint32(cfg.BlockMaxSize)},
		params.ActiveNetParams.Params,
		consensus.SigCache(),
		nil,
		consensus.MedianTimeSource(),
		consensus,
		m.coinbaseAddress,
		nil,
		pow.SCRYPT,
		mining.CoinbaseFlagsStatic,
	)
	if err != nil {
		t.Fatalf("NewBlockTemplate returned error: %v", err)
	}
	if template == nil || template.Block == nil || template.SerializedBlock == nil {
		t.Fatalf("template incomplete: %#v", template)
	}
	if template.Height != 1 {
		t.Fatalf("template height = %d, want 1", template.Height)
	}
	if got := len(template.Block.Transactions); got != 1 {
		t.Fatalf("template transactions = %d, want 1", got)
	}
	if got := template.Block.Transactions[0].Hash(); got != signedTx.Hash() {
		t.Fatalf("template tx hash = %s, want %s", got, signedTx.Hash())
	}
	wantFee := gasPrice.Int64() * int64(signedTx.Gas())
	if len(template.Fees) != 2 || template.Fees[0] != -wantFee || template.Fees[1] != wantFee {
		t.Fatalf("template fees = %v, want [%d %d]", template.Fees, -wantFee, wantFee)
	}
	if template.BlockFees != wantFee {
		t.Fatalf("template block fees = %d, want %d", template.BlockFees, wantFee)
	}
	m.template = template
	prepareFeeTestMiner(m)
	m.timeSource = fixedMedianTimeSource{now: time.Now()}
	result, err := (&GBTWorker{miner: m}).getResult(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.BlockFees != wantFee || len(result.Transactions) != 1 || result.Transactions[0].Fee != wantFee || result.Transactions[0].Hash != signedTx.Hash().String() {
		t.Fatal("actual pending builder fee/identity did not survive getter")
	}
}

type noopMiningRuntimeNotify struct{}

func (noopMiningRuntimeNotify) AnnounceNewTransactions([]*bdagtypes.TxDesc, []peer.ID) {}
func (noopMiningRuntimeNotify) RelayInventory(*bdagtypes.SerializedBlock, uint32, *peer.ID) {
}
func (noopMiningRuntimeNotify) BroadcastMessage(any)                          {}
func (noopMiningRuntimeNotify) TransactionConfirmed(*ethtypes.Transaction)    {}
func (noopMiningRuntimeNotify) TransactionsConfirmed([]*ethtypes.Transaction) {}
func (noopMiningRuntimeNotify) AddRebroadcastInventory([]*bdagtypes.TxDesc)   {}
func (noopMiningRuntimeNotify) RelayTransaction(*ethtypes.Transaction) error  { return nil }

var _ model.Notify = noopMiningRuntimeNotify{}
