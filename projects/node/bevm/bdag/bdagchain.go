package bdag

import (
	"fmt"
	"math/big"
	"os"
	"reflect"

	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	qcommon "github.com/BlockdagNetworkLabs/bdag/bevm/common"
	"github.com/BlockdagNetworkLabs/bdag/bevm/eth"
	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	mbdag "github.com/BlockdagNetworkLabs/bdag/consensus/model/bdag"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/rpc/api"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	"github.com/ethereum/go-ethereum/common"
	ethconsensus "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/uint256"
)

const (
	txSlotSize = 32 * 1024
	txMaxSize  = 4 * txSlotSize
)

type BdagChain struct {
	service.Service
	chain     *eth.ETHChain
	bdagpool  *BdagPool
	consensus model.Consensus
	statedb   *state.StateDB
	ethclient *ethclient.Client

	lastEVMRecoveryFailure *evmRecoveryFailureMemo
	poolRewardIPCStop      func()
}

func (b *BdagChain) GetEthClient() *ethclient.Client {
	return b.ethclient
}

func (b *BdagChain) Start() error {
	if err := b.Service.Start(); err != nil {
		return err
	}
	//
	log.Info("Start BdagChain...")
	err := b.chain.Start()
	if err != nil {
		return err
	}
	stopRewardIPC, err := startPoolRewardIPC(b, os.Getenv("BDAG_POOL_REWARD_IPC_PATH"))
	if err != nil {
		_ = b.chain.Stop()
		_ = b.Service.Stop()
		return err
	}
	b.poolRewardIPCStop = stopRewardIPC
	//
	rpcClient := b.chain.Node().Attach()
	client := ethclient.NewClient(rpcClient)
	b.ethclient = client
	blockNum, err := client.BlockNumber(b.Context())
	if err != nil {
		log.Error(err.Error())
	} else {
		log.Debug(fmt.Sprintf("bdagETH block chain current block number:%d", blockNum))
	}

	cbh := b.chain.Ether().BlockChain().CurrentBlock()
	if cbh != nil {
		log.Debug(fmt.Sprintf("bdagETH block chain current block:number=%d hash=%s", cbh.Number.Uint64(), cbh.Hash().String()))
	}

	//
	state, err := b.chain.Ether().BlockChain().State()
	if err != nil {
		return nil
	}

	log.Debug(fmt.Sprintf("Etherbase:%v balance:%v", b.chain.Config().Eth.Miner.Etherbase, state.GetBalance(b.chain.Config().Eth.Miner.Etherbase)))

	//
	for addr := range b.chain.Config().Eth.Genesis.Alloc {
		log.Debug(fmt.Sprintf("Alloc address:%v balance:%v", addr.String(), state.GetBalance(addr)))
	}

	b.bdagpool.Start()
	return nil
}

func (b *BdagChain) Stop() error {
	log.Info("try stop BdagChain")
	if b.poolRewardIPCStop != nil {
		b.poolRewardIPCStop()
		b.poolRewardIPCStop = nil
	}
	if err := b.Service.Stop(); err != nil {
		return err
	}
	log.Info("Stop BdagChain...")

	err := b.chain.Stop()
	if err != nil {
		log.Error(err.Error())
	}

	b.bdagpool.Stop()
	return nil
}

func (b *BdagChain) CheckConnectBlock(block *mbdag.Block, miningaddr common.Address, reward uint64, admissibility GovernanceAdmissibility) error {
	parent := b.chain.Ether().BlockChain().CurrentBlock()
	return b.CheckConnectBlockWithParent(parent, block, miningaddr, reward, admissibility)
}

func (b *BdagChain) CheckConnectBlockWithParent(parent *types.Header, block *mbdag.Block, miningaddr common.Address, reward uint64, admissibility GovernanceAdmissibility) error {
	if parent == nil {
		return fmt.Errorf("No EVM parent")
	}

	mblock, _, _, err := b.buildBlock(parent, block.Transactions(), block.Timestamp().Unix(), miningaddr, reward, admissibility)
	if err != nil {
		return err
	}
	block.EvmBlock = mblock

	return nil
}

func (b *BdagChain) InsertBlockWithoutSetHead(block *mbdag.Block) error {
	mblock := block.EvmBlock
	if mblock == nil {
		return fmt.Errorf("No EVM block:%d", block.ID())
	}
	_, err := b.chain.Ether().BlockChain().InsertBlockWithoutSetHead(mblock, false)
	return err
}

func (b *BdagChain) SetCanonicalBlock(block *mbdag.Block) error {
	mblock := block.EvmBlock
	if mblock == nil {
		return fmt.Errorf("No EVM block:%d", block.ID())
	}
	_, err := b.chain.Ether().BlockChain().SetCanonical(mblock)
	return err
}

func (b *BdagChain) ConnectBlock(block *mbdag.Block) (uint64, error) {
	mblock := block.EvmBlock
	if mblock == nil {
		return 0, fmt.Errorf("No EVM block:%d", block.ID())
	}

	inserted, err := b.chain.Ether().BlockChain().InsertChain(types.Blocks{mblock})
	if err != nil {
		return 0, err
	}
	if inserted != 1 {
		return 0, fmt.Errorf("BuildBlock error")
	}

	log.Debug(fmt.Sprintf(
		"BEVM Block inserted: number=%d hash=%s txs=%d  => blockID=%s",
		mblock.Number().Uint64(),
		mblock.Hash().String(),
		len(mblock.Transactions()),
		block.ID().Hex(),
	))

	return mblock.NumberU64(), nil
}

func (b *BdagChain) buildBlock(parent *types.Header, txs []*types.Transaction, timestamp int64, miningaddr common.Address, blockReward uint64, admissibility GovernanceAdmissibility) (*types.Block, types.Receipts, *state.StateDB, error) {
	config := b.chain.Config().Eth.Genesis.Config
	engine := b.chain.Ether().Engine()
	parentBlock := types.NewBlockWithHeader(parent)

	uncles := []*types.Header{}

	chainreader := b.chain.Ether().BlockChain()
	statedb, err := b.chain.Ether().BlockChain().StateAt(parentBlock.Root())
	if err != nil {
		return nil, nil, nil, err
	}
	gaslimit := core.CalcGasLimit(parentBlock.GasLimit(), b.bdagpool.config.GasCeil)
	if mparams.IsMainnetChainID(config.ChainID) {
		gaslimit = mparams.MainnetFixedGasLimit
	}

	header := makeHeader(&b.chain.Config().Eth, parentBlock, statedb, timestamp, gaslimit)
	header.Coinbase = miningaddr
	if admissibility.rewardGovernance {
		header.Extra = bdagconsensus.EncodeRewardExtraWithLayer(blockReward, admissibility.nativeLayer)
	} else {
		header.Extra = bdagconsensus.EncodeRewardExtra(blockReward)
	}
	if err := bdagconsensus.ValidateRewardExtraAtLayer(
		header.Extra,
		blockReward,
		admissibility.nativeLayer,
		admissibility.rewardGovernance,
	); err != nil {
		return nil, nil, nil, err
	}
	if config.DAOForkSupport && config.DAOForkBlock != nil && config.DAOForkBlock.Cmp(header.Number) == 0 {
		misc.ApplyDAOHardFork(statedb)
	}
	// Engine-defined pre-block state changes (system-contract upgrades at a fork
	// height). Mirrors deps/go-ethereum/core/state_processor.go Process so the
	// template root and the import root agree.
	bdagconsensus.ApplyPreBlockStateChanges(engine, chainreader, header, statedb)
	txs, receipts, err := b.fillBlock(txs, header, statedb, admissibility)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := b.checkRewardCredits(chainreader, header, statedb, admissibility); err != nil {
		return nil, nil, nil, err
	}
	block, err := engine.FinalizeAndAssemble(chainreader, header, statedb, &types.Body{Transactions: txs, Uncles: uncles}, receipts)
	if err != nil {
		return nil, nil, nil, err
	}
	return block, receipts, statedb, nil
}

func (b *BdagChain) checkRewardCredits(chainreader ethconsensus.ChainHeaderReader, header *types.Header, statedb *state.StateDB, admissibility GovernanceAdmissibility) error {
	engine, ok := b.chain.Ether().Engine().(interface {
		CheckRewardCredits(ethconsensus.ChainHeaderReader, *types.Header, *state.StateDB, bdagconsensus.RewardCreditChecker) error
	})
	if !ok {
		return nil
	}
	return engine.CheckRewardCredits(chainreader, header, statedb, func(to common.Address, amount *uint256.Int) error {
		return admissibility.CheckNativeCredit(statedb, to, amount, vm.NativeTransferReasonReward)
	})
}

func (b *BdagChain) fillBlock(txs []*types.Transaction, header *types.Header, statedb *state.StateDB, admissibility GovernanceAdmissibility) ([]*types.Transaction, []*types.Receipt, error) {
	etxs := []*types.Transaction{}
	receipts := []*types.Receipt{}

	gasPool := new(core.GasPool).AddGas(header.GasLimit)

	for _, tx := range txs {
		// txmb := tx.(*mbdag.VMTx).ETx
		err := b.addTx(tx, header, statedb, &etxs, &receipts, gasPool, admissibility)
		if err != nil {
			return nil, nil, err
		}
	}
	return etxs, receipts, nil
}

func (b *BdagChain) addTx(tx *types.Transaction, header *types.Header, statedb *state.StateDB, txs *[]*types.Transaction, receipts *[]*types.Receipt, gasPool *core.GasPool, admissibility GovernanceAdmissibility) error {
	config := b.chain.Config().Eth.Genesis.Config
	if err := admissibility.CheckTxAt(statedb, b.chain.Ether().BlockChain().Config(), header, tx); err != nil {
		return err
	}
	statedb.SetTxContext(tx.Hash(), len(*txs))

	bc := b.chain.Ether().BlockChain()
	snap := statedb.Snapshot()
	vmConfig := admissibility.applyToVMConfig(*bc.GetVMConfig())
	receipt, err := core.ApplyTransaction(config, bc, &header.Coinbase, gasPool, statedb, header, tx, &header.GasUsed, vmConfig)
	if err != nil {
		statedb.RevertToSnapshot(snap)
		return err
	}

	*txs = append(*txs, tx)
	*receipts = append(*receipts, receipt)

	return nil
}

func (b *BdagChain) RegisterAPIs(apis []api.API) {
	eapis := []rpc.API{}

	for _, api := range apis {
		eapi := rpc.API{
			Namespace: "bdag",
			Version:   "1.0",
			Service:   api.Service,
			Public:    api.Public,
		}
		eapis = append(eapis, eapi)

		log.Trace(fmt.Sprintf("Bridging API:%s.%s in BDAG => .%s in BEVM", api.NameSpace, reflect.TypeOf(api.Service).Elem(), reflect.TypeOf(api.Service).Elem()))
	}
	b.chain.Node().RegisterAPIs(eapis)
}

func (b *BdagChain) BdagPool() *BdagPool {
	return b.bdagpool
}

func (b *BdagChain) DisablePoolSubscriptionsForRecovery() {
	if b == nil || b.bdagpool == nil {
		return
	}
	b.bdagpool.UnsubscribeForRecovery()
}

func (b *BdagChain) ETHChain() *eth.ETHChain {
	return b.chain
}

func (b *BdagChain) CheckSanity(ETx *types.Transaction) error {
	return b.validateTx(ETx, false)

}

func (b *BdagChain) validateTx(tx *types.Transaction, checkState bool) error {
	// Reject transactions over defined size to prevent DOS attacks
	if tx.Size() > txMaxSize {
		return txpool.ErrOversizedData
	}
	if tx.Value().Sign() < 0 {
		return txpool.ErrNegativeValue
	}
	if tx.GasFeeCap().BitLen() > 256 {
		return core.ErrFeeCapVeryHigh
	}
	if tx.GasTipCap().BitLen() > 256 {
		return core.ErrTipVeryHigh
	}
	if tx.GasFeeCapIntCmp(tx.GasTipCap()) < 0 {
		return core.ErrTipAboveFeeCap
	}
	blockchain := b.chain.Ether().BlockChain()
	head := blockchain.CurrentBlock()
	if head == nil || head.Number == nil {
		return fmt.Errorf("EVM head unavailable for transaction signing domain")
	}
	from, err := types.Sender(types.MakeSigner(blockchain.Config(), new(big.Int).Add(head.Number, big.NewInt(1)), head.Time), tx)
	if err != nil {
		return txpool.ErrInvalidSender
	}
	if checkState {
		currentState, err := b.chain.Ether().BlockChain().State()
		if err != nil {
			return err
		}
		currentNonce := currentState.GetNonce(from)
		if currentNonce > tx.Nonce() {
			log.Debug("Nonce too low in validateTx", "from", from, "nonce", tx.Nonce(), "currentNonce", currentNonce)
			return core.ErrNonceTooLow
		}
		if currentNonce < tx.Nonce() {
			return fmt.Errorf("nonce too high: got %d, expected %d", tx.Nonce(), currentNonce)
		}
		if currentState.GetBalance(from).ToBig().Cmp(tx.Cost()) < 0 {
			return core.ErrInsufficientFunds
		}
		if err := TemplateGovernanceAdmissibility().CheckTransaction(currentState, from, tx.To(), tx.Value()); err != nil {
			return err
		}
	}
	intrGas, err := core.IntrinsicGas(tx.Data(), tx.AccessList(), tx.To() == nil, true, true, false)
	if err != nil {
		return err
	}
	if tx.Gas() < intrGas {
		return core.ErrIntrinsicGas
	}
	return nil
}

func (b *BdagChain) VerifyTx(tx *types.Transaction) (int64, error) {
	err := b.validateTx(tx, true)
	if err != nil {
		return 0, err
	}
	cost := tx.Cost()
	cost = cost.Sub(cost, tx.Value())
	cost = cost.Div(cost, qcommon.Precision)
	return cost.Int64(), nil

}

func (b *BdagChain) GetCurHeader() *types.Header {
	return b.chain.Ether().BlockChain().CurrentBlock()
}

func (b *BdagChain) SafeEVMCheckpoint(depth uint64) (uint64, common.Hash, common.Hash, bool) {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return 0, common.Hash{}, common.Hash{}, false
	}
	bc := b.chain.Ether().BlockChain()
	head := bc.CurrentBlock()
	if head == nil || head.Number == nil {
		return 0, common.Hash{}, common.Hash{}, false
	}
	number := uint64(0)
	if head.Number.Uint64() > depth {
		number = head.Number.Uint64() - depth
	}
	header := bc.GetHeaderByNumber(number)
	if header == nil {
		return 0, common.Hash{}, common.Hash{}, false
	}
	return number, header.Hash(), header.Root, true
}

func (b *BdagChain) ValidateEVMCheckpoint(number uint64, hash common.Hash, root common.Hash) (bool, bool) {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return false, false
	}
	header := b.chain.Ether().BlockChain().GetHeaderByNumber(number)
	if header == nil {
		return false, false
	}
	if header.Hash() != hash {
		return false, true
	}
	if root != (common.Hash{}) && header.Root != root {
		return false, true
	}
	return true, true
}

func (b *BdagChain) Genesis() *common.Hash {
	mbb := b.chain.Ether().BlockChain().Genesis().Hash().Bytes()
	qcommon.ReverseBytes(&mbb)
	nmbb, err := hash.NewHash(mbb)
	if err != nil {
		return nil
	}
	return nmbb
}

func (b *BdagChain) GetBalance(addre string) (int64, error) {
	var eAddr common.Address
	if common.IsHexAddress(addre) {
		eAddr = common.HexToAddress(addre)
	}
	state, err := b.chain.Ether().BlockChain().State()
	if err != nil {
		return 0, err
	}
	ba := state.GetBalance(eAddr).ToBig()
	if ba == nil {
		return 0, fmt.Errorf("No balance for address %s", eAddr)
	}
	ba = ba.Div(ba, qcommon.Precision)
	return ba.Int64(), nil
}

func (b *BdagChain) GetBlockIDByTxHash(txhash *common.Hash) uint64 {
	ret, tx, _, blockNumber, _, _ := b.chain.Backend().GetTransaction(nil, qcommon.ToEVMHash(txhash))
	if !ret || tx == nil {
		return 0
	}
	return blockNumber
}

func (b *BdagChain) APIs() []api.API {
	return []api.API{
		{
			NameSpace: cmds.DefaultServiceNameSpace,
			Service:   NewPublicBdagChainAPI(b),
			Public:    true,
		},
	}
}

func (b *BdagChain) IsNativeValidEVMBlock(hash common.Hash, number uint64, root common.Hash) (bool, bool) {
	if b == nil || b.consensus == nil || b.consensus.BlockChain() == nil {
		return false, false
	}
	lookup, ok := b.consensus.BlockChain().(evmNumberBlockFinder)
	if !ok {
		return false, false
	}
	block := lookup.GetBlockByNumber(number)
	if block == nil || block.GetState() == nil {
		return false, false
	}
	state := block.GetState()
	if state.GetStatus().KnownInvalid() {
		return false, true
	}
	if state.GetEVMNumber() != number || state.GetEVMHash() != hash {
		return false, true
	}
	if root != (common.Hash{}) && state.GetEVMRoot() != root {
		return false, true
	}
	return true, true
}

func NewBdagChain(consensus model.Consensus) (*BdagChain, error) {
	log.Info("bdag chain", "version", Version)
	cfg := consensus.Config()
	eth.InitLog(cfg.DebugLevel, cfg.DebugPrintOrigins)
	//
	ecfg, args, err := MakeParams(cfg)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	chain, err := eth.NewETHChain(ecfg, args)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}

	mc := &BdagChain{
		chain:     chain,
		bdagpool:  chain.Config().Eth.Miner.External.(*BdagPool),
		consensus: consensus,
	}
	chain.Backend().SetNativeValidityOracle(mc)
	if err := mc.bdagpool.init(consensus, &chain.Config().Eth.Miner, chain.Config().Eth.Genesis.Config, chain.Ether().Engine(), chain.Ether(), chain.Ether().EventMux()); err != nil {
		log.Error(err.Error())
		return nil, err
	}
	return mc, nil
}
