package node

import (
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/common/system/disk"
	cfgutil "github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/p2p"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/rpc/api"

	"github.com/BlockdagNetworkLabs/bdag/services/gateway"
	"github.com/BlockdagNetworkLabs/bdag/services/health"

	"github.com/BlockdagNetworkLabs/bdag/services/miner"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"github.com/BlockdagNetworkLabs/bdag/services/notifymgr"
	"github.com/BlockdagNetworkLabs/bdag/services/tx"

	ethcommon "github.com/ethereum/go-ethereum/common"
	getheth "github.com/ethereum/go-ethereum/eth"
	ethfilters "github.com/ethereum/go-ethereum/eth/filters"
	"github.com/ethereum/go-ethereum/eth/tracers"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// BlockdagFull implements the Blockdag full node service.
type BlockdagFull struct {
	service.Service
	// under node
	node *Node
	// msg notifier
	nfManager model.Notify
	// database
	db model.DataBase
	// address service
	// addressApi *address.AddressApi
}

func parseEVMModules(env string) []string {
	fields := strings.Fields(env)
	for _, f := range fields {
		if strings.HasPrefix(f, "--http.api=") {
			return strings.Split(strings.TrimPrefix(f, "--http.api="), ",")
		}
	}
	return nil
}

func (qm *BlockdagFull) APIs() []api.API {
	apis := qm.Service.APIs()
	// apis = append(apis, qm.addressApi.APIs()...)
	apis = append(apis, qm.apis()...)
	return apis
}

func (qm *BlockdagFull) RegisterP2PService() error {
	peerServer, err := p2p.NewService(qm.node.Config, qm.node.consensus, qm.node.Params)
	if err != nil {
		return err
	}
	return qm.Services().RegisterService(peerServer)
}

func (qm *BlockdagFull) RegisterRpcService() ([]api.API, error) {
	if qm.node.Config.DisableRPC {
		return nil, nil
	}
	rpcServer, err := rpc.NewRPCServer(qm.node.Config, qm.node.consensus)
	if err != nil {
		return nil, err
	}
	qm.Services().RegisterService(rpcServer)

	go func() {
		<-rpcServer.RequestedProcessShutdown()
		system.ShutdownRequestChannel <- struct{}{}
	}()
	// Gather all the possible APIs to surface
	apis := qm.APIs()

	// Generate the whitelist based on the allowed modules
	whitelist := make(map[string]bool)
	for _, module := range qm.node.Config.Modules {
		whitelist[module] = true
	}

	retApis := []api.API{}
	// Register all the APIs exposed by the services
	for _, api := range apis {
		if whitelist[api.NameSpace] || (len(whitelist) == 0 && api.Public) {
			if err := rpcServer.RegisterService(api.NameSpace, api.Service); err != nil {
				return nil, err
			}
			log.Debug(fmt.Sprintf("RPC Service API registered. NameSpace:%s     %s", api.NameSpace, reflect.TypeOf(api.Service)))
			retApis = append(retApis, api)
		}
	}
	return retApis, nil
}

func (qm *BlockdagFull) RegisterTxManagerService() error {
	// txmanager
	tm, err := tx.NewTxManager(qm.node.consensus, qm.nfManager)
	if err != nil {
		return err
	}
	qm.Services().RegisterService(tm)
	return nil
}

func (qm *BlockdagFull) RegisterMinerService() error {
	cfg := qm.node.Config
	txManager := qm.GetTxManager()
	// Cpu Miner
	// Create the mining policy based on the configuration options.
	// NOTE: The CPU miner relies on the mempool, so the mempool has to be
	// created before calling the function to create the CPU miner.
	policy := mining.Policy{
		BlockMinSize:      cfg.BlockMinSize,
		BlockMaxSize:      cfg.BlockMaxSize,
		BlockPrioritySize: cfg.BlockPrioritySize,
		TxMinFreeFee:      cfg.MinTxFee, //TODO, duplicated config item with mem-pool
		TxTimeScope:       cfg.TxTimeScope,
		PeerID:            qm.GetPeerServer().PeerID().String(),
	}
	miner := miner.NewMiner(qm.node.consensus, &policy, txManager.MemPool(), qm.GetPeerServer())
	qm.Services().RegisterService(miner)
	return nil
}

// chainSyncChecker implements gateway.SyncChecker
type chainSyncChecker struct {
	bc *blockchain.BlockChain
	qm *BlockdagFull
}

func (c *chainSyncChecker) IsSyncing() bool {
	if c.qm != nil && c.qm.GetPeerServer() != nil {
		if !c.qm.GetPeerServer().IsCurrent() {
			return true
		}
	}
	return !c.bc.IsCurrent()
}

func (c *chainSyncChecker) EvmHeight() uint64 {
	evmChain := c.bc.EVMChain()
	if evmChain == nil {
		return 0
	}
	ec := evmChain.ETHChain()
	if ec != nil && ec.Ether() != nil && ec.Ether().BlockChain() != nil {
		cb := ec.Ether().BlockChain().CurrentBlock()
		if cb != nil {
			return cb.Number.Uint64()
		}
	}
	return 0
}

func (c *chainSyncChecker) EVMHead() (uint64, bool) {
	if c == nil || c.bc == nil {
		return 0, false
	}
	evmChain := c.bc.EVMChain()
	if evmChain == nil {
		return 0, false
	}
	ethChain := evmChain.ETHChain()
	if ethChain == nil || ethChain.Ether() == nil || ethChain.Ether().BlockChain() == nil {
		return 0, false
	}
	header := ethChain.Ether().BlockChain().CurrentBlock()
	if header == nil || header.Number == nil {
		return 0, false
	}
	nativeHash, nativeNumber, nativeRoot, nativeValid, known := c.bc.CommittedEVMHead()
	visible := known && nativeValid &&
		header.Number.Uint64() == nativeNumber &&
		header.Hash() == nativeHash &&
		header.Root == nativeRoot
	return header.Number.Uint64(), visible
}

func (c *chainSyncChecker) WithCommittedEVMHead(read func()) bool {
	return c != nil && c.bc != nil && c.bc.WithCommittedEVMHead(read)
}

func (c *chainSyncChecker) NativeEVMHeight() uint64 {
	if c == nil || c.bc == nil {
		return 0
	}
	_, number, _, _, ok := c.bc.CommittedEVMHead()
	if !ok {
		return 0
	}
	return number
}

func (c *chainSyncChecker) DagHeight() uint64 {
	return uint64(c.bc.BestSnapshot().GraphState.GetMainHeight())
}

func (c *chainSyncChecker) CalculateStateRoot(block *types.Block) *ethcommon.Hash {
	return c.bc.CalculateStateRoot(block)
}

func (qm *BlockdagFull) RegisterGatewayService() error {
	internalDag := ":" + params.ActiveNetParams.RpcPort
	if len(qm.node.Config.RPCListeners) > 0 {
		internalDag = qm.node.Config.RPCListeners[0]
	}

	dagListen := internalDag
	if qm.node.Config.GatewayDagAddr != "" && qm.node.Config.GatewayDagPort != 0 {
		dagListen = fmt.Sprintf("%s:%d", qm.node.Config.GatewayDagAddr, qm.node.Config.GatewayDagPort)
	}

	host, portStr, err := net.SplitHostPort(dagListen)
	if err != nil {
		return err
	}
	if host == "" {
		host = "0.0.0.0"
	}
	dagPort, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}

	dagHost, _, err := net.SplitHostPort(internalDag)
	if err != nil {
		return err
	}
	if dagHost == "" {
		dagHost = "127.0.0.1"
	}

	syncChecker := &chainSyncChecker{bc: qm.GetBlockChain(), qm: qm}

	evmHost, evmInternalPort := cfgutil.ParseEVMRPC(qm.node.Config.EVMEnv)
	internalEvm := fmt.Sprintf("%s:%d", evmHost, evmInternalPort)
	evmListen := internalEvm
	if qm.node.Config.GatewayEvmAddr != "" && qm.node.Config.GatewayEvmPort != 0 {
		evmListen = fmt.Sprintf("%s:%d", qm.node.Config.GatewayEvmAddr, qm.node.Config.GatewayEvmPort)
	}

	_, evmPortStr, err := net.SplitHostPort(evmListen)
	if err != nil {
		return err
	}
	evmPort, err := strconv.Atoi(evmPortStr)
	if err != nil {
		return err
	}

	// Get In-Process EVM Server (http.Handler)
	evmChain := qm.GetBlockChain().EVMChain().ETHChain()

	// Create a new Geth RPC server and register all APIs from the backend
	// This avoids starting the internal Geth RPC server while still serving requests via Gateway
	// Parse http.api from config or EVMEnv to determine enabled modules
	var modules []string
	if len(qm.node.Config.GatewayEVMAPI) > 0 {
		modules = strings.Split(qm.node.Config.GatewayEVMAPI, ",")
	} else {
		modules = parseEVMModules(qm.node.Config.EVMEnv)
	}

	// Default to standard APIs if no modules specified
	if len(modules) == 0 {
		modules = []string{"eth", "net", "web3"}
	}
	moduleSet := make(map[string]bool)
	for _, m := range modules {
		moduleSet[m] = true
	}

	// Create a new Geth RPC server and register all APIs from the backend
	evmHandler := gethrpc.NewServer()

	// 1. Register Standard Backend APIs (eth, net, web3, etc.)
	for _, api := range evmChain.Ether().APIs() {
		// Register if in list OR if it's "eth", "net", "web3" (safe defaults)
		// Or just stick to the list strictly? The user wanted strictly flag control.
		// But "eth" is usually needed.
		if moduleSet[api.Namespace] {
			evmHandler.RegisterName(api.Namespace, api.Service)
		}
	}

	// 2. Register Debug API (Both Basic and Tracers)
	if moduleSet["debug"] {
		// Register basic debug API (DumpBlock, etc.)
		evmHandler.RegisterName("debug", getheth.NewDebugAPI(evmChain.Ether()))
		// Register tracers API (TraceTransaction, TraceCall)
		// Note: We register under "debug" namespace again. RPC server merges methods.
		evmHandler.RegisterName("debug", tracers.NewAPI(evmChain.Backend()))
	}

	// 3. Register TxPool API
	// if moduleSet["txpool"] {
	// 	evmHandler.RegisterName("txpool", getheth.NewTxPoolAPI(evmChain.Ether()))
	// }

	// 4. Register Blockdag API
	if moduleSet["bdag"] {
		for _, api := range qm.GetBlockChain().EVMChain().APIs() {
			if api.NameSpace == "bdag" || moduleSet[api.NameSpace] {
				evmHandler.RegisterName(api.NameSpace, api.Service)
			}
		}
	}

	// Register the Filter API to support eth_subscribe("logs", ...)
	// We use an empty ethfilters.Config{} as default.
	if backend := evmChain.Backend(); backend != nil {
		filterSystem := ethfilters.NewFilterSystem(backend, ethfilters.Config{})
		if err := evmHandler.RegisterName("eth", ethfilters.NewFilterAPI(filterSystem)); err != nil {
			log.Warn("Failed to register Filter API for eth_subscribe", "err", err)
		}
	} else {
		log.Warn("EVM Backend is nil, cannot register Filter API")
	}

	// Get In-Process DAG Handler
	dagHandler := qm.GetRpcServer().Handler()

	// Determine EVM WS Listen Address
	evmWSListen := ""
	if qm.node.Config.GatewayEvmWSPort != 0 {
		host := qm.node.Config.GatewayEvmWSAddr
		if host == "" {
			host = qm.node.Config.GatewayEvmAddr
		}
		if host == "" {
			host = "0.0.0.0"
		}
		evmWSListen = fmt.Sprintf("%s:%d", host, qm.node.Config.GatewayEvmWSPort)
	}

	var allowedOrigins []string
	if qm.node.Config.GatewayEvmCORSDomain != "" {
		for _, domain := range strings.Split(qm.node.Config.GatewayEvmCORSDomain, ",") {
			allowedOrigins = append(allowedOrigins, strings.TrimSpace(domain))
		}
	}

	evmGw, err := gateway.NewEVM(gateway.Config{
		ListenAddr:   evmListen,
		WSListenAddr: evmWSListen,
		PeerDagPort:  dagPort,
		PeerEvmPort:  evmPort,
		// The EVM gateway accepts connections without authentication
		// but still authenticates its own DAG RPC calls.
		InboundAuth:                false,
		InboundLimitUser:           qm.node.Config.RPCLimitUser,
		InboundLimitPass:           qm.node.Config.RPCLimitPass,
		UpUser:                     qm.node.Config.RPCUser,
		UpPass:                     qm.node.Config.RPCPass,
		UpLimitUser:                qm.node.Config.RPCLimitUser,
		UpLimitPass:                qm.node.Config.RPCLimitPass,
		AuthorizeLimitedRequest:    qm.GetRpcServer().AuthorizeLimitedRequest,
		MiningAddrs:                qm.node.Config.MiningAddrs,
		HTTPTimeout:                qm.node.Config.GatewayHTTPTimeout,
		RequestTimeout:             qm.node.Config.GatewayRequestTimeout,
		ReadTimeout:                qm.node.Config.GatewayReadTimeout,
		WriteTimeout:               qm.node.Config.GatewayWriteTimeout,
		IdleTimeout:                qm.node.Config.GatewayIdleTimeout,
		MaxRequestBytes:            qm.node.Config.GatewayMaxRequestBytes,
		RateLimit:                  qm.node.Config.GatewayRateLimit,
		RateBurst:                  qm.node.Config.GatewayRateBurst,
		MaxConcurrentRequests:      qm.node.Config.GatewayMaxConcurrent,
		CriticalConcurrentRequests: qm.node.Config.GatewayCriticalReserve,
		AllowedOrigins:             allowedOrigins,
	})
	if err != nil {
		return err
	}
	// Inject in-process components
	evmGw.EvmHandler = evmHandler
	evmGw.EvmWSHandler = evmHandler.WebsocketHandler([]string{"*"})
	evmGw.DagHandler = dagHandler
	evmGw.SyncChecker = syncChecker
	if pool := qm.GetBlockChain().EVMChain().BdagPool(); pool != nil {
		pool.SetPeerEVMTargetProvider(evmGw.MaxPeerHeight)
	}

	if err := qm.Services().RegisterService(evmGw); err != nil {
		return err
	}
	dagGw, err := gateway.NewDAG(gateway.Config{
		ListenAddr:                 dagListen,
		PeerDagPort:                dagPort,
		PeerEvmPort:                evmPort,
		InboundAuth:                true,
		InboundUser:                qm.node.Config.RPCUser,
		InboundPass:                qm.node.Config.RPCPass,
		InboundLimitUser:           qm.node.Config.RPCLimitUser,
		InboundLimitPass:           qm.node.Config.RPCLimitPass,
		UpUser:                     qm.node.Config.RPCUser,
		UpPass:                     qm.node.Config.RPCPass,
		UpLimitUser:                qm.node.Config.RPCLimitUser,
		UpLimitPass:                qm.node.Config.RPCLimitPass,
		AuthorizeLimitedRequest:    qm.GetRpcServer().AuthorizeLimitedRequest,
		MiningAddrs:                qm.node.Config.MiningAddrs,
		HTTPTimeout:                qm.node.Config.GatewayHTTPTimeout,
		RequestTimeout:             qm.node.Config.GatewayRequestTimeout,
		ReadTimeout:                qm.node.Config.GatewayReadTimeout,
		WriteTimeout:               qm.node.Config.GatewayWriteTimeout,
		IdleTimeout:                qm.node.Config.GatewayIdleTimeout,
		MaxRequestBytes:            qm.node.Config.GatewayMaxRequestBytes,
		RateLimit:                  qm.node.Config.GatewayRateLimit,
		RateBurst:                  qm.node.Config.GatewayRateBurst,
		MaxConcurrentRequests:      qm.node.Config.GatewayMaxConcurrent,
		CriticalConcurrentRequests: qm.node.Config.GatewayCriticalReserve,
	})
	if err != nil {
		return err
	}
	// Inject in-process components
	dagGw.EvmHandler = evmHandler
	dagGw.DagHandler = dagHandler
	dagGw.SyncChecker = syncChecker

	if err := qm.Services().RegisterService(dagGw); err != nil {
		return err
	}
	return nil
}

func (qm *BlockdagFull) RegisterNotifyMgr() error {
	nfManager := notifymgr.New(qm.GetPeerServer(), qm.node.consensus)
	qm.Services().RegisterService(nfManager)
	qm.nfManager = nfManager
	return nil
}

// return peer server
func (qm *BlockdagFull) GetPeerServer() *p2p.Service {
	var service *p2p.Service
	if err := qm.Services().FetchService(&service); err != nil {
		log.Error(err.Error())
		return nil
	}
	return service
}

func (qm *BlockdagFull) GetRpcServer() *rpc.RpcServer {
	var service *rpc.RpcServer
	if err := qm.Services().FetchService(&service); err != nil {
		log.Warn(err.Error())
		return nil
	}
	return service
}

func (qm *BlockdagFull) GetTxManager() *tx.TxManager {
	var service *tx.TxManager
	if err := qm.Services().FetchService(&service); err != nil {
		log.Error(err.Error())
		return nil
	}
	return service
}

func (qm *BlockdagFull) GetMiner() *miner.Miner {
	var service *miner.Miner
	if err := qm.Services().FetchService(&service); err != nil {
		log.Error(err.Error())
		return nil
	}
	return service
}

func (qm *BlockdagFull) GetBlockChain() *blockchain.BlockChain {
	var service *blockchain.BlockChain
	if err := qm.Services().FetchService(&service); err != nil {
		log.Error(err.Error())
		return nil
	}
	return service
}

func (qm *BlockdagFull) monitorFreeDiskSpace() error {
	freeDiskSpaceCritical := qm.node.Config.Minfreedisk * 1024 * 1024
	if freeDiskSpaceCritical == 0 {
		return nil
	}
	path, err := filepath.Abs(qm.node.Config.DataDir)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("monitor disk path is empty")
	}
	go func() {
		log.Info("Start monitor free disk space", "path", path, "critical", ethcommon.StorageSize(freeDiskSpaceCritical))
		for {
			freeSpace, err := disk.GetFreeDiskSpace(path)
			if err != nil {
				log.Warn("Failed to get free disk space", "path", path, "err", err)
				break
			}
			if freeSpace < freeDiskSpaceCritical {
				log.Error("Low disk space. Gracefully shutting down BDAG to prevent database corruption.", "available", ethcommon.StorageSize(freeSpace), "path", path)
				qm.node.consensus.Shutdown()
				break
			} else if freeSpace < 2*freeDiskSpaceCritical {
				log.Warn("Disk space is running low. BDAG will shutdown if disk space runs below critical level.", "available", ethcommon.StorageSize(freeSpace), "critical_level", ethcommon.StorageSize(freeDiskSpaceCritical), "path", path)
			}
			time.Sleep(30 * time.Second)
		}
	}()

	return nil
}

func newBlockdagFullNode(node *Node) (*BlockdagFull, error) {
	qm := BlockdagFull{
		node: node,
		db:   node.DB,
	}
	qm.Service.InitServices()

	if err := node.consensus.Init(); err != nil {
		return nil, err
	}
	bc := node.consensus.BlockChain().(*blockchain.BlockChain)
	if err := qm.Services().RegisterService(bc); err != nil {
		return nil, err
	}
	if err := qm.RegisterP2PService(); err != nil {
		return nil, err
	}
	if err := qm.RegisterNotifyMgr(); err != nil {
		return nil, err
	}
	if err := qm.RegisterTxManagerService(); err != nil {
		return nil, err
	}

	// health service
	hs := health.New(node.Config.HealthAddr)
	if err := qm.Services().RegisterService(hs); err != nil {
		return nil, err
	}
	node.health = hs

	txManager := qm.GetTxManager()
	// prepare peerServer
	qm.GetPeerServer().SetBlockChain(qm.GetBlockChain())
	qm.GetPeerServer().SetTimeSource(qm.node.consensus.MedianTimeSource())
	qm.GetPeerServer().SetTxMemPool(txManager.MemPool().GetTxPool())
	qm.GetPeerServer().SetNotify(qm.nfManager)

	//
	bc.EVMChain().BdagPool().SetTxPool(txManager.MemPool().GetTxPool())
	bc.EVMChain().BdagPool().SetNotify(qm.nfManager)
	if err := qm.RegisterMinerService(); err != nil {
		return nil, err
	}

	apis, err := qm.RegisterRpcService()
	if err != nil {
		return nil, err
	}

	if err := qm.RegisterGatewayService(); err != nil {
		return nil, err
	}
	// Disable standalone RPC server listeners to prevent port conflict with Gateway,
	// as Gateway will serve the RPC requests.
	qm.node.Config.RPCListeners = nil
	bc.EVMChain().RegisterAPIs(apis)

	if qm.GetRpcServer() != nil {
		qm.GetRpcServer().BC = qm.GetBlockChain()
		qm.GetRpcServer().ChainParams = qm.node.Params

		qm.nfManager.(*notifymgr.NotifyMgr).RpcServer = qm.GetRpcServer()
		qm.GetMiner().RpcSer = qm.GetRpcServer()
	}

	// BlockChain.Start may spend a long time repairing or replaying local EVM
	// state after an unclean shutdown or checkpoint restore. Keep DAG P2P in
	// its registered startup position so a recovering node can connect to peers
	// while local chain state is being prepared.
	qm.Services().LowestPriority(qm.GetBlockChain())
	qm.Services().LowestPriority(qm.GetTxManager())
	return &qm, qm.monitorFreeDiskSpace()
}
