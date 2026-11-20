package eth

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/eth/gasprice"
	ethapi "github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

type testNativeValidityOracle struct {
	valid bool
	known bool
}

type nativeValidityTuple struct {
	hash   common.Hash
	number uint64
	root   common.Hash
}

type selectiveNativeValidityOracle struct {
	valid map[nativeValidityTuple]bool
}

func (o selectiveNativeValidityOracle) IsNativeValidEVMBlock(hash common.Hash, number uint64, root common.Hash) (bool, bool) {
	valid, known := o.valid[nativeValidityTuple{hash: hash, number: number, root: root}]
	return valid, known
}

func (o testNativeValidityOracle) IsNativeValidEVMBlock(common.Hash, uint64, common.Hash) (bool, bool) {
	return o.valid, o.known
}

func TestNativeHeaderVisible(t *testing.T) {
	header := &types.Header{Number: big.NewInt(1), Root: common.HexToHash("0x1234")}

	if !((&EthAPIBackend{}).nativeHeaderVisible(header)) {
		t.Fatal("backend without native oracle must preserve plain geth visibility")
	}
	if !((&EthAPIBackend{}).nativeHeaderVisible(nil)) {
		t.Fatal("backend without native oracle must preserve plain geth nil-header behavior")
	}
	if !((&EthAPIBackend{nativeValidity: testNativeValidityOracle{valid: true, known: true}}).nativeHeaderVisible(header)) {
		t.Fatal("known valid native mapping should be visible")
	}
	if (&EthAPIBackend{nativeValidity: testNativeValidityOracle{valid: true, known: true}}).nativeHeaderVisible(nil) {
		t.Fatal("nil header must fail closed when native oracle is installed")
	}
	if (&EthAPIBackend{nativeValidity: testNativeValidityOracle{valid: false, known: true}}).nativeHeaderVisible(header) {
		t.Fatal("known invalid native mapping must be hidden")
	}
	if (&EthAPIBackend{nativeValidity: testNativeValidityOracle{valid: false, known: false}}).nativeHeaderVisible(header) {
		t.Fatal("unknown native mapping must fail closed when oracle is installed")
	}
}

func TestEthAPIBackendLatestUsesStateBackedHead(t *testing.T) {
	ctx := context.Background()
	address := common.HexToAddress("0x0000000000000000000000000000000000000042")
	genesis := &core.Genesis{
		Config:   params.AllEthashProtocolChanges,
		GasLimit: 30_000_000,
		BaseFee:  big.NewInt(13_000_000_000_000),
		Alloc:    types.GenesisAlloc{address: {Balance: big.NewInt(12345)}},
	}
	engine := ethash.NewFaker()
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, nil, genesis, nil, engine, vm.Config{}, nil)
	if err != nil {
		t.Fatalf("NewBlockChain() error = %v", err)
	}
	_, blocks, _ := core.GenerateChainWithGenesis(genesis, engine, 1, func(int, *core.BlockGen) {})
	if _, err := chain.InsertChain(blocks); err != nil {
		chain.Stop()
		t.Fatalf("InsertChain() error = %v", err)
	}
	t.Cleanup(chain.Stop)

	backend := &EthAPIBackend{eth: &Ethereum{
		blockchain: chain,
		chainDb:    db,
		config:     &ethconfig.Config{RPCGasCap: 50_000_000},
	}}
	backend.gpo = gasprice.NewOracle(backend, gasprice.Config{
		Blocks: 1, Percentile: 0, MaxHeaderHistory: 1, MaxBlockHistory: 1,
		MaxPrice: gasprice.DefaultMaxPrice, IgnorePrice: gasprice.DefaultIgnorePrice,
	}, big.NewInt(2))

	safe := chain.CurrentBlock()
	unsafe := types.CopyHeader(chain.CurrentHeader())
	unsafe.Number = new(big.Int).Add(unsafe.Number, big.NewInt(1))
	unsafe.ParentHash = safe.Hash()
	unsafe.Time = safe.Time + 1
	unsafe.Difficulty = ethash.CalcDifficulty(chain.Config(), unsafe.Time, safe)
	unsafe.BaseFee = eip1559.CalcBaseFee(chain.Config(), safe)
	if safe.BaseFee == nil || unsafe.BaseFee == nil || safe.BaseFee.Cmp(unsafe.BaseFee) == 0 {
		t.Fatalf("safe and unsafe base fees must differ: safe=%v unsafe=%v", safe.BaseFee, unsafe.BaseFee)
	}
	if _, err := chain.InsertHeaderChain([]*types.Header{unsafe}); err != nil {
		t.Fatalf("InsertHeaderChain() error = %v", err)
	}
	if got := backend.CurrentHeader(); got == nil || got.Number.Uint64() <= safe.Number.Uint64() {
		t.Fatalf("CurrentHeader() = %v, want unsafe header above state-backed head %d", got, safe.Number.Uint64())
	}

	backend.SetNativeValidityOracle(selectiveNativeValidityOracle{valid: map[nativeValidityTuple]bool{
		{hash: safe.Hash(), number: safe.Number.Uint64(), root: safe.Root}: true,
	}})
	latest, err := backend.HeaderByNumber(ctx, rpc.LatestBlockNumber)
	if err != nil {
		t.Fatalf("HeaderByNumber(latest) error = %v", err)
	}
	if latest == nil || latest.Hash() != safe.Hash() || latest.Number.Uint64() != safe.Number.Uint64() || latest.Root != safe.Root {
		t.Fatalf("latest = %v, want state-backed head #%d %s", latest, safe.Number.Uint64(), safe.Hash())
	}
	state, stateHeader, err := backend.StateAndHeaderByNumber(ctx, rpc.LatestBlockNumber)
	if err != nil {
		t.Fatalf("StateAndHeaderByNumber(latest) error = %v", err)
	}
	if state == nil || stateHeader == nil || stateHeader.Hash() != latest.Hash() || state.GetBalance(address).ToBig().Cmp(big.NewInt(12345)) != 0 {
		var balance *big.Int
		if state != nil {
			balance = state.GetBalance(address).ToBig()
		}
		t.Fatalf("latest state/header mismatch: state=%v header=%v balance=%v", state != nil, stateHeader, balance)
	}

	blockAPI := ethapi.NewBlockChainAPI(backend)
	blockNumber, err := blockAPI.BlockNumber(ctx)
	if err != nil || uint64(blockNumber) != safe.Number.Uint64() {
		t.Fatalf("BlockNumber() = %d, %v; want %d", blockNumber, err, safe.Number.Uint64())
	}
	balance, err := blockAPI.GetBalance(ctx, address, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber))
	if err != nil || (*big.Int)(balance).Cmp(big.NewInt(12345)) != 0 {
		t.Fatalf("GetBalance(latest) = %v, %v; want 12345", balance, err)
	}

	gasPrice, err := ethapi.NewEthereumAPI(backend).GasPrice(ctx)
	if err != nil {
		t.Fatalf("GasPrice() error = %v", err)
	}
	if (*big.Int)(gasPrice).Cmp(new(big.Int).Add(big.NewInt(2), safe.BaseFee)) != 0 {
		t.Fatalf("GasPrice() = %s, want safe base fee %s plus tip 2", gasPrice, safe.BaseFee)
	}

	backend.SetNativeValidityOracle(selectiveNativeValidityOracle{valid: map[nativeValidityTuple]bool{}})
	if hidden, err := backend.HeaderByNumber(ctx, rpc.LatestBlockNumber); err != nil || hidden != nil {
		t.Fatalf("unknown native latest visibility = header %v, err %v; want hidden", hidden, err)
	}
	if _, err := blockAPI.BlockNumber(ctx); err == nil {
		t.Fatal("BlockNumber accepted unknown native latest visibility")
	}
	if _, err := blockAPI.GetBalance(ctx, address, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)); err == nil {
		t.Fatal("GetBalance accepted unknown native latest visibility")
	}
}
