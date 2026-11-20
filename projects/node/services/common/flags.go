// Copyright (c) 2015-2016 The Decred developers
// Copyright (c) 2013-2016 The btcsuite developers

package common

import (
	"path/filepath"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/profiling"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/urfave/cli/v2"
)

const (
	defaultConfigFilename    = ".conf"
	defaultDataDirname       = "data"
	defaultLogLevel          = "info"
	defaultDebugPrintOrigins = false
	defaultLogDirname        = "logs"
	defaultLogFilename       = ".log"
	defaultBlockMinSize      = 0
	// 307200 = 300 KB (300*1024)
	defaultBlockMaxSize                = 307200
	defaultMaxRPCClients               = 10
	defaultMaxRPCClientsPerHost        = 0
	defaultMaxRPCWebsockets            = 25
	defaultMaxRPCConcurrentReqs        = 20
	defaultRPCMaxConcurrent            = 20
	defaultRPCCriticalConcurrent       = 4
	defaultRPCRequestTimeout           = 60 * time.Second
	defaultMaxPeers                    = 50
	defaultP2PRPCConcurrency           = 32
	defaultEVMNativeCatchupBatchOrders = uint64(512)
	defaultP2PStreamReuse              = false
	defaultMiningStateSync             = false
	defaultMaxInboundPeersPerHost      = 25 // The default max total of inbound peer for host
	defaultTrickleInterval             = 10 * time.Second
	defaultInvalidTxIndex              = false
	defaultTxhashIndex                 = false
	defaultMempoolExpiry               = int64(time.Hour)
	defaultRPCUser                     = "test"
	defaultRPCPass                     = "test"
	defaultMinBlockPruneSize           = 2000
	defaultMinBlockDataCache           = 2000
	defaultHealthAddr                  = "127.0.0.1:6061"
	defaultMinRelayTxFee               = int64(1e4)
	defaultObsoleteHeight              = 5
	defaultGBTTimeout                  = 800 // default gbt timeout 800 ms
	defaultGatewayHTTPTimeout          = 10 * time.Second
	defaultGatewayRequestTimeout       = 10 * time.Second
	defaultGatewayReadTimeout          = 30 * time.Second
	defaultGatewayWriteTimeout         = 30 * time.Second
	defaultGatewayIdleTimeout          = 30 * time.Second
	defaultGatewayTxCacheTTL           = 5 * time.Minute
	defaultGatewayTxCacheMax           = 10000
	defaultGatewayMaxRequestBytes      = int64(1 << 20) // 1 MiB
	defaultGatewayRateLimit            = 10.0           // requests per second
	defaultGatewayRateBurst            = 20
	defaultGatewayMaxConcurrent        = 1000
	defaultGatewayCriticalReserve      = 16
)
const (
	defaultSigCacheMaxSize = 100000
)
const (
	defaultMaxOrphanTxSize = 5000
)

var (
	defaultHomeDir        = util.AppDataDir("", false)
	defaultConfigFile     = filepath.Join(defaultHomeDir, defaultConfigFilename)
	defaultDataDir        = filepath.Join(defaultHomeDir, defaultDataDirname)
	defaultDbType         = "ffldb"
	defaultLogDir         = filepath.Join(defaultHomeDir, defaultLogDirname)
	defaultLogRotatorSize = int64(1024 * 10)
	defaultRPCKeyFile     = filepath.Join(defaultHomeDir, "rpc.key")
	defaultRPCCertFile    = filepath.Join(defaultHomeDir, "rpc.cert")
	defaultDAGType        = "phantom"
)

var (
	cfg = DefaultConfig("")

	Modules           cli.StringSlice
	MiningAddrs       cli.StringSlice
	BlockMinSize      uint
	BlockMaxSize      uint
	BlockPrioritySize uint
	AddPeers          cli.StringSlice
	BootstrapNodes    cli.StringSlice
	Whitelist         cli.StringSlice
	Blacklist         cli.StringSlice
	GBTNotify         cli.StringSlice
	RPCListeners      cli.StringSlice
	GenesisAlloc      cli.StringSlice

	Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "appdata",
			Aliases:     []string{"A"},
			Usage:       "Path to application home directory",
			Value:       defaultHomeDir,
			Destination: &cfg.HomeDir,
		},
		&cli.BoolFlag{
			Name:        "ShowVersion",
			Aliases:     []string{"V"},
			Usage:       "Display version information and exit",
			Destination: &cfg.ShowVersion,
		},
		&cli.StringFlag{
			Name:        "configfile",
			Aliases:     []string{"C"},
			Usage:       "Path to configuration file",
			Value:       defaultConfigFile,
			Destination: &cfg.ConfigFile,
		},
		&cli.StringFlag{
			Name:        "datadir",
			Aliases:     []string{"b"},
			Usage:       "Directory to store data",
			Value:       defaultDataDir,
			Destination: &cfg.DataDir,
		},
		&cli.StringFlag{
			Name:        "logdir",
			Usage:       "Directory to log output.",
			Value:       defaultLogDir,
			Destination: &cfg.LogDir,
		},
		&cli.Int64Flag{
			Name:        "logrotatorsize",
			Usage:       "Directory to log output.",
			Value:       defaultLogRotatorSize,
			Destination: &cfg.LogRotatorSize,
		},
		&cli.BoolFlag{
			Name:        "nofilelogging",
			Usage:       "Disable file logging.",
			Destination: &cfg.NoFileLogging,
		},
		&cli.StringFlag{
			Name:        "listen",
			Usage:       "Add an IP to listen for connections",
			Destination: &cfg.Listener,
		},
		&cli.StringFlag{
			Name:        "port",
			Usage:       "Default p2p port.",
			Destination: &cfg.DefaultPort,
		},

		&cli.Int64Flag{
			Name:        "http.maxrequestbytes",
			Usage:       "Maximum size of HTTP request body in bytes",
			Value:       defaultGatewayMaxRequestBytes,
			Destination: &cfg.GatewayMaxRequestBytes,
		},
		&cli.Float64Flag{
			Name:        "http.ratelimit",
			Usage:       "HTTP requests per second allowed per client IP",
			Value:       defaultGatewayRateLimit,
			Destination: &cfg.GatewayRateLimit,
		},
		&cli.IntFlag{
			Name:        "http.rateburst",
			Usage:       "Burst size for HTTP rate limiter",
			Value:       defaultGatewayRateBurst,
			Destination: &cfg.GatewayRateBurst,
		},
		&cli.IntFlag{
			Name:        "http.maxconcurrent",
			Usage:       "Maximum number of concurrent non-critical gateway requests",
			Value:       defaultGatewayMaxConcurrent,
			Destination: &cfg.GatewayMaxConcurrent,
		},
		&cli.IntFlag{
			Name:        "http.criticalreserve",
			Usage:       "Reserved concurrent gateway slots for critical local RPC requests",
			Value:       defaultGatewayCriticalReserve,
			Destination: &cfg.GatewayCriticalReserve,
		},
		&cli.StringFlag{
			Name:        "dag.http.addr",
			Usage:       "DAG HTTP listening interface",
			Destination: &cfg.GatewayDagAddr,
		},
		&cli.IntFlag{
			Name:        "dag.http.port",
			Usage:       "DAG HTTP listening port",
			Destination: &cfg.GatewayDagPort,
		},
		&cli.StringFlag{
			Name:        "evm.http.addr",
			Usage:       "EVM HTTP listening interface",
			Destination: &cfg.GatewayEvmAddr,
		},
		&cli.IntFlag{
			Name:        "evm.http.port",
			Usage:       "EVM HTTP listening port",
			Destination: &cfg.GatewayEvmPort,
		},
		&cli.StringFlag{
			Name:        "evm.http.corsdomain",
			Usage:       "Comma separated list of domains from which to accept cross origin requests (browser enforced)",
			Destination: &cfg.GatewayEvmCORSDomain,
		},
		&cli.StringFlag{
			Name:        "evm.http.api",
			Usage:       "EVM API modules to enable (e.g. eth,net,web3,debug)",
			Destination: &cfg.GatewayEVMAPI,
		},
		&cli.IntFlag{
			Name:        "evm.ws.port",
			Usage:       "EVM WebSocket listening port",
			Destination: &cfg.GatewayEvmWSPort,
		},
		&cli.StringFlag{
			Name:        "evm.ws.addr",
			Usage:       "EVM WebSocket listening interface",
			Destination: &cfg.GatewayEvmWSAddr,
		},
		&cli.StringFlag{
			Name:        "name",
			Usage:       "Custom name for this node advertised to peers",
			Value:       "BDAG",
			Destination: &cfg.NodeName,
		},
		&cli.IntFlag{
			Name:        "maxpeers",
			Usage:       "Max number of inbound and outbound peers",
			Value:       defaultMaxPeers,
			Destination: &cfg.MaxPeers,
		},
		&cli.IntFlag{
			Name:        "p2prpcconcurrency",
			Usage:       "Max number of concurrent inbound P2P RPC stream handlers processed at once across all connected peers",
			Value:       defaultP2PRPCConcurrency,
			Destination: &cfg.P2PRPCConcurrency,
		},
		&cli.Uint64Flag{
			Name:        "evmnativecatchupbatchorders",
			Usage:       "Max DAG orders replayed per EVM-native-state catch-up batch; lower under a large sync backlog if import shows periodic pauses",
			Value:       defaultEVMNativeCatchupBatchOrders,
			Destination: &cfg.EVMNativeCatchupBatchOrders,
		},
		&cli.BoolFlag{
			Name:        "p2pstreamreuse",
			Usage:       "Reuse a pooled libp2p stream per peer for the hot sync RPCs instead of opening a new stream per request; only against peers advertising StreamReuseProtocolVersion (v47+), older peers keep the one-shot half-close path",
			Value:       defaultP2PStreamReuse,
			Destination: &cfg.P2PStreamReuse,
		},
		&cli.BoolFlag{
			Name:        "nolisten",
			Usage:       "Disable listening for incoming connections",
			Destination: &cfg.DisableListen,
		},
		&cli.StringFlag{
			Name:        "rpcuser",
			Aliases:     []string{"u"},
			Usage:       "Username for RPC connections",
			Value:       defaultRPCUser,
			Destination: &cfg.RPCUser,
		},
		&cli.StringFlag{
			Name:        "rpcpass",
			Aliases:     []string{"P"},
			Usage:       "Password for RPC connections",
			Value:       defaultRPCPass,
			Destination: &cfg.RPCPass,
		},
		&cli.StringFlag{
			Name:        "rpccert",
			Usage:       "File containing the certificate file",
			Value:       defaultRPCCertFile,
			Destination: &cfg.RPCCert,
		},
		&cli.StringFlag{
			Name:        "rpckey",
			Usage:       "File containing the certificate key",
			Value:       defaultRPCKeyFile,
			Destination: &cfg.RPCKey,
		},
		&cli.StringSliceFlag{
			Name:        "rpclisten",
			Usage:       "Add an IP to listen for DAG JSON-RPC gateway",
			Destination: &RPCListeners,
		},
		&cli.IntFlag{
			Name:        "rpcmaxclients",
			Usage:       "Max number of RPC clients for standard connections",
			Value:       defaultMaxRPCClients,
			Destination: &cfg.RPCMaxClients,
		},
		&cli.IntFlag{
			Name:        "rpcmaxclientsperhost",
			Usage:       "Max number of standard RPC clients from a single remote host",
			Value:       defaultMaxRPCClientsPerHost,
			Destination: &cfg.RPCMaxClientsPerHost,
		},
		&cli.BoolFlag{
			Name:        "norpc",
			Usage:       "Disable built-in RPC server -- NOTE: The RPC server is disabled by default if no rpcuser/rpcpass or rpclimituser/rpclimitpass is specified",
			Destination: &cfg.DisableRPC,
		},
		&cli.BoolFlag{
			Name:        "notls",
			Usage:       "Disable TLS for the RPC server -- NOTE: This is only allowed if the RPC server is bound to localhost",
			Destination: &cfg.DisableTLS,
		},
		&cli.StringSliceFlag{
			Name:        "modules",
			Usage:       "Modules is a list of API modules(See GetNodeInfo) to expose via the HTTP RPC interface. If the module list is empty, all RPC API endpoints designated public will be exposed.",
			Destination: &Modules,
		},
		&cli.BoolFlag{
			Name:        "nocheckpoints",
			Usage:       "Disable built-in checkpoints.  Don't do this unless you know what you're doing.",
			Destination: &cfg.DisableCheckpoints,
		},
		&cli.BoolFlag{
			Name:        "addrindex",
			Usage:       "Maintain a full address-based transaction index which makes the getrawtransactions RPC available",
			Destination: &cfg.AddrIndex,
		},
		&cli.BoolFlag{
			Name:        "dropaddrindex",
			Usage:       "Deletes the address-based transaction index from the database on start up and then exits.",
			Destination: &cfg.DropAddrIndex,
		},
		&cli.BoolFlag{
			Name:        "light",
			Usage:       "start as a Blockdag light node",
			Destination: &cfg.LightNode,
		},
		&cli.UintFlag{
			Name:        "sigcachemaxsize",
			Usage:       "The maximum number of entries in the signature verification cache",
			Value:       defaultSigCacheMaxSize,
			Destination: &cfg.SigCacheMaxSize,
		},
		&cli.BoolFlag{
			Name:        "testnet",
			Usage:       "Use the test network",
			Destination: &cfg.TestNet,
		},
		&cli.BoolFlag{
			Name:        "mixnet",
			Usage:       "Use the test mix pow network",
			Destination: &cfg.MixNet,
		},
		&cli.BoolFlag{
			Name:        "privnet",
			Usage:       "Use the private network",
			Destination: &cfg.PrivNet,
		},
		&cli.BoolFlag{
			Name:        "devnet",
			Usage:       "Use the development network",
			Destination: &cfg.DevNet,
		},
		&cli.StringFlag{
			Name:        "dbtype",
			Usage:       "Database backend to use for the Block Chain",
			Value:       defaultDbType,
			Destination: &cfg.DbType,
		},
		&cli.StringFlag{
			Name:        "profile",
			Usage:       "Enable HTTP profiling on given [addr:]port -- NOTE port must be between 1024 and 65536",
			Destination: &cfg.Profile,
		},
		&cli.StringFlag{
			Name:        "health",
			Usage:       "Address for HTTP health service (host:port)",
			Value:       defaultHealthAddr,
			Destination: &cfg.HealthAddr,
		},
		&cli.StringFlag{
			Name:        "cpuprofile",
			Usage:       "Write CPU profile to the specified file",
			Destination: &cfg.CPUProfile,
		},
		&cli.BoolFlag{
			Name:        "trackheap",
			Usage:       "tracks the size of the heap and dumps a profile",
			Destination: &cfg.TrackHeap,
		},
		&cli.IntFlag{
			Name:        "trackheaplimit",
			Usage:       "track heap when limit in gigabytes (default:7G)",
			Destination: &cfg.TrackHeapLimit,
			Value:       profiling.DefaultTrackHeapLimit,
		},
		&cli.StringFlag{
			Name:        "debuglevel",
			Aliases:     []string{"d"},
			Usage:       "Logging level {trace, debug, info, warn, error, critical}",
			Value:       defaultLogLevel,
			Destination: &cfg.DebugLevel,
		},
		&cli.BoolFlag{
			Name:        "printorigin",
			Usage:       "Print log debug location (file:line)",
			Destination: &cfg.DebugPrintOrigins,
		},
		&cli.BoolFlag{
			Name:        "norelaypriority",
			Usage:       "Do not require free or low-fee transactions to have high priority for relaying",
			Destination: &cfg.NoRelayPriority,
		},
		&cli.Float64Flag{
			Name:        "limitfreerelay",
			Usage:       "Limit relay of transactions with no transaction fee to the given amount in thousands of bytes per minute",
			Destination: &cfg.FreeTxRelayLimit,
		},
		&cli.BoolFlag{
			Name:        "acceptnonstd",
			Usage:       "Accept and relay non-standard transactions to the network regardless of the default settings for the active network.",
			Destination: &cfg.AcceptNonStd,
			Value:       true,
		},
		&cli.IntFlag{
			Name:        "maxorphantx",
			Usage:       "Max number of orphan transactions to keep in memory",
			Destination: &cfg.MaxOrphanTxs,
		},
		&cli.Int64Flag{
			Name:        "mintxfee",
			Usage:       "The minimum transaction fee in AtomBDAG/kB.",
			Value:       defaultMinRelayTxFee,
			Destination: &cfg.MinTxFee,
		},
		&cli.Int64Flag{
			Name:        "mempoolexpiry",
			Usage:       "Do not keep transactions in the mempool more than mempoolexpiry",
			Value:       defaultMempoolExpiry,
			Destination: &cfg.MempoolExpiry,
		},
		&cli.BoolFlag{
			Name:        "persistmempool",
			Usage:       "Whether to save the mempool on shutdown and load on restart",
			Destination: &cfg.Persistmempool,
		},
		&cli.BoolFlag{
			Name:        "nomempoolbar",
			Usage:       "Whether to show progress bar when load mempool from file",
			Destination: &cfg.NoMempoolBar,
		},
		&cli.BoolFlag{
			Name:        "miner",
			Usage:       "Enable miner module",
			Destination: &cfg.Miner,
		},
		&cli.StringSliceFlag{
			Name:        "miningaddr",
			Usage:       "Add the specified payment address to the list of addresses to use for generated blocks",
			Destination: &MiningAddrs,
		},
		&cli.IntFlag{
			Name:        "miningtimeoffset",
			Usage:       "Offset the mining timestamp of a block by this many seconds (positive values are in the past)",
			Destination: &cfg.MiningTimeOffset,
		},
		&cli.UintFlag{
			Name:        "blockminsize",
			Usage:       "Mininum block size in bytes to be used when creating a block",
			Value:       defaultBlockMinSize,
			Destination: &BlockMinSize,
		},
		&cli.UintFlag{
			Name:        "blockmaxsize",
			Usage:       "Maximum block size in bytes to be used when creating a block",
			Value:       defaultBlockMaxSize,
			Destination: &BlockMaxSize,
		},
		&cli.UintFlag{
			Name:        "blockprioritysize",
			Usage:       "Size in bytes for high-priority/low-fee transactions when creating a block",
			Destination: &BlockPrioritySize,
		},
		&cli.IntFlag{
			Name:        "rpcmaxwebsockets",
			Usage:       "Max number of RPC websocket connections",
			Value:       defaultMaxRPCWebsockets,
			Destination: &cfg.RPCMaxWebsockets,
		},
		&cli.IntFlag{
			Name:        "rpcmaxconcurrent",
			Usage:       "Max number of concurrent normal RPC requests",
			Value:       defaultRPCMaxConcurrent,
			Destination: &cfg.RPCMaxConcurrent,
		},
		&cli.IntFlag{
			Name:        "rpccriticalconcurrent",
			Usage:       "Additional reserved concurrent RPC requests for block submission",
			Value:       defaultRPCCriticalConcurrent,
			Destination: &cfg.RPCCriticalConcurrent,
		},
		&cli.IntFlag{
			Name:        "rpcmaxconcurrentreqs",
			Usage:       "Max number of concurrent RPC requests that may be processed concurrently",
			Value:       defaultMaxRPCConcurrentReqs,
			Destination: &cfg.RPCMaxConcurrentReqs,
		},
		&cli.DurationFlag{
			Name:        "rpcrequesttimeout",
			Usage:       "Timeout for standard RPC request processing",
			Value:       defaultRPCRequestTimeout,
			Destination: &cfg.RPCRequestTimeout,
		},
		&cli.DurationFlag{
			Name:        "httptimeout",
			Usage:       "HTTP client timeout",
			Value:       defaultGatewayHTTPTimeout,
			Destination: &cfg.GatewayHTTPTimeout,
			EnvVars:     []string{"HTTP_TIMEOUT"},
		},
		&cli.DurationFlag{
			Name:        "requesttimeout",
			Usage:       "Timeout for proxy requests",
			Value:       defaultGatewayRequestTimeout,
			Destination: &cfg.GatewayRequestTimeout,
			EnvVars:     []string{"REQUEST_TIMEOUT"},
		},
		&cli.DurationFlag{
			Name:        "http.readtimeout",
			Usage:       "Read timeout for gateway HTTP servers",
			Value:       defaultGatewayReadTimeout,
			Destination: &cfg.GatewayReadTimeout,
		},
		&cli.DurationFlag{
			Name:        "http.writetimeout",
			Usage:       "Write timeout for gateway HTTP servers",
			Value:       defaultGatewayWriteTimeout,
			Destination: &cfg.GatewayWriteTimeout,
		},
		&cli.DurationFlag{
			Name:        "http.idletimeout",
			Usage:       "Idle timeout for gateway HTTP keep-alive connections",
			Value:       defaultGatewayIdleTimeout,
			Destination: &cfg.GatewayIdleTimeout,
		},
		&cli.DurationFlag{
			Name:        "txcachettl",
			Usage:       "TTL for transaction cache",
			Value:       defaultGatewayTxCacheTTL,
			Destination: &cfg.GatewayTxCacheTTL,
			EnvVars:     []string{"TXCACHE_TTL"},
		},
		&cli.IntFlag{
			Name:        "txcachemax",
			Usage:       "Maximum entries for transaction cache",
			Value:       defaultGatewayTxCacheMax,
			Destination: &cfg.GatewayTxCacheMax,
			EnvVars:     []string{"TXCACHE_MAX"},
		},
		&cli.BoolFlag{
			Name:        "blocksonly",
			Usage:       "Do not accept transactions from remote peers",
			Destination: &cfg.BlocksOnly,
		},
		&cli.BoolFlag{
			Name:        "miningstatesync",
			Usage:       "Synchronizing the mining state with other nodes",
			Destination: &cfg.MiningStateSync,
		},
		&cli.StringSliceFlag{
			Name:        "addpeer",
			Aliases:     []string{"a"},
			Usage:       "Add a peer to connect with at startup",
			Destination: &AddPeers,
		},
		&cli.BoolFlag{
			Name:        "upnp",
			Usage:       "Use UPnP to map our listening port outside of NAT",
			Destination: &cfg.Upnp,
		},
		&cli.IntFlag{
			Name:        "maxinbound",
			Usage:       "The max total of inbound peer for host",
			Value:       defaultMaxInboundPeersPerHost,
			Destination: &cfg.MaxInbound,
		},
		&cli.BoolFlag{
			Name:        "banning",
			Usage:       "Enable banning of misbehaving peers",
			Destination: &cfg.Banning,
			Value:       true,
		},
		&cli.StringFlag{
			Name:        "dagtype",
			Aliases:     []string{"G"},
			Usage:       "DAG type {phantom,spectre}",
			Value:       defaultDAGType,
			Destination: &cfg.DAGType,
		},
		&cli.BoolFlag{
			Name:        "cleanup",
			Aliases:     []string{"L"},
			Usage:       "Cleanup the block database",
			Destination: &cfg.Cleanup,
		},
		&cli.BoolFlag{
			Name:        "cleanuptips",
			Usage:       "Cleanup the tips of block database",
			Destination: &cfg.CleanupTips,
		},
		&cli.BoolFlag{
			Name:        "buildledger",
			Usage:       "Generate the genesis ledger for the next Blockdag version",
			Destination: &cfg.BuildLedger,
		},
		&cli.StringFlag{
			Name:        "zmqpubhashblock",
			Usage:       "Enable publish hash block  in <address>",
			Destination: &cfg.Zmqpubhashblock,
		},
		&cli.StringFlag{
			Name:        "zmqpubrawblock",
			Usage:       "Enable publish raw block in <address>",
			Destination: &cfg.Zmqpubrawblock,
		},
		&cli.StringFlag{
			Name:        "zmqpubhashtx",
			Usage:       "Enable publish hash transaction in <address>",
			Destination: &cfg.Zmqpubhashtx,
		},
		&cli.StringFlag{
			Name:        "zmqpubrawtx",
			Usage:       "Enable publish raw transaction in <address>",
			Destination: &cfg.Zmqpubrawtx,
		},
		&cli.BoolFlag{
			Name:        "invalidtxindex",
			Usage:       "invalid transaction index.",
			Destination: &cfg.InvalidTxIndex,
		},
		&cli.BoolFlag{
			Name:        "txhashindex",
			Usage:       "Cache transaction full hash.",
			Destination: &cfg.TxHashIndex,
		},
		&cli.BoolFlag{
			Name:        "ntp",
			Usage:       "Auto sync time.",
			Destination: &cfg.NTP,
		},
		&cli.StringSliceFlag{
			Name:        "bootstrapnode",
			Usage:       "The address of bootstrap node.",
			Destination: &BootstrapNodes,
		},
		&cli.BoolFlag{
			Name:        "p2pstaticpeersonly",
			Usage:       "Only connect to peers explicitly configured with addpeer, bootstrapnode, or relaynode /p2p/<peer-id> multiaddresses; disables default bootstrap, DHT discovery, recent peers, and peerstore dialing",
			Destination: &cfg.P2PStaticPeersOnly,
		},
		&cli.BoolFlag{
			Name:        "p2p-ws-over-http",
			Usage:       "Enable P2P over WebSocket instead of raw TCP.",
			Destination: &cfg.P2PWSOverHTTP,
		},
		&cli.StringFlag{
			Name:        "metadatadir",
			Usage:       "meta data dir for p2p",
			Destination: &cfg.MetaDataDir,
		},
		&cli.IntFlag{
			Name:        "p2ptcpport",
			Usage:       "The tcp port used by P2P.",
			Destination: &cfg.P2PTCPPort,
		},
		&cli.StringFlag{
			Name:        "externalip",
			Usage:       "The IP address advertised by libp2p. This may be used to advertise an external IP.",
			Destination: &cfg.HostIP,
		},
		&cli.StringFlag{
			Name:        "externaldns",
			Usage:       "The DNS address advertised by libp2p. This may be used to advertise an external DNS.",
			Destination: &cfg.HostDNS,
		},
		&cli.StringFlag{
			Name:        "relaynode",
			Usage:       "The address of relay node that routes traffic between two peers over a Blockdag “relay” peer.",
			Destination: &cfg.RelayNode,
		},
		&cli.StringSliceFlag{
			Name:        "whitelist",
			Usage:       "Add an IP network or IP,PeerID that will not be banned or ignore dual channel mode detection. (eg. 192.168.1.0/24 or ::1 or [peer id])",
			Destination: &Whitelist,
		},
		&cli.StringSliceFlag{
			Name:        "blacklist",
			Usage:       "Add some IP network or IP that will be banned. (eg. 192.168.1.0/24 or ::1)",
			Destination: &Blacklist,
		},
		&cli.IntFlag{
			Name:        "maxbadresp",
			Usage:       "maxbadresp is the maximum number of bad responses from a peer before we stop talking to it.",
			Destination: &cfg.MaxBadResp,
		},
		&cli.BoolFlag{
			Name:        "circuit",
			Usage:       "All peers will ignore dual channel mode detection",
			Destination: &cfg.Circuit,
			Value:       true,
		},
		&cli.StringFlag{
			Name:        "evmenv",
			Usage:       "bdag EVM environment",
			Destination: &cfg.EVMEnv,
		},
		&cli.StringSliceFlag{
			Name:        "genesisalloc",
			Usage:       "Pre-fund an EVM address at genesis. Format: 0xADDR:AMOUNT where AMOUNT is in BDAG (ether units). Repeatable. Only applies to a fresh chain.",
			Destination: &GenesisAlloc,
		},
		&cli.BoolFlag{
			Name:        "estimatefee",
			Usage:       "Enable estimate fee",
			Destination: &cfg.Estimatefee,
		},
		&cli.StringSliceFlag{
			Name:        "gbtnotify",
			Usage:       "HTTP URL list to be notified of new block template",
			Destination: &GBTNotify,
		},
		&cli.BoolFlag{
			Name:        "acctmode",
			Usage:       "Enable support account system mode",
			Destination: &cfg.AcctMode,
		},
		&cli.Uint64Flag{
			Name:        "dagcachesize",
			Usage:       "DAG block cache size",
			Value:       defaultMinBlockPruneSize,
			Destination: &cfg.DAGCacheSize,
		},
		&cli.Uint64Flag{
			Name:        "bdcachesize",
			Usage:       "Block data cache size",
			Value:       defaultMinBlockDataCache,
			Destination: &cfg.BlockDataCacheSize,
		},
		&cli.BoolFlag{
			Name:        "consistency",
			Usage:       "Detect data consistency through P2P",
			Destination: &cfg.Consistency,
			Value:       true,
		},
		&cli.BoolFlag{
			Name:        "metrics",
			Usage:       "Enable metrics collection and reporting",
			Destination: &cfg.Metrics,
		},
		&cli.BoolFlag{
			Name:        "metrics.expensive",
			Usage:       "Enable expensive metrics collection and reporting",
			Destination: &cfg.MetricsExpensive,
		},
		&cli.Uint64Flag{
			Name:        "minfreedisk",
			Usage:       "Minimum free disk space in MB, once reached triggers auto shut down (default = 512M, 0 = disabled)",
			Value:       512,
			Destination: &cfg.Minfreedisk,
		},
		&cli.IntFlag{
			Name:        "cache",
			Usage:       "Megabytes of memory allocated to internal caching (default = 1024 mainnet full node)",
			Value:       1024,
			Destination: &cfg.Cache,
		},
		&cli.IntFlag{
			Name:        "cache.database",
			Usage:       "Percentage of cache memory allowance to use for database io",
			Value:       50,
			Destination: &cfg.CacheDatabase,
		},
		&cli.IntFlag{
			Name:        "cache.snapshot",
			Usage:       "Percentage of cache memory allowance to use for snapshot caching (default = 5% full mode)",
			Value:       5,
			Destination: &cfg.CacheSnapshot,
		},
		&cli.BoolFlag{
			Name:        "autocollectevm",
			Usage:       "auto collect miner coinbase utxo to evm",
			Destination: &cfg.AutoCollectEvm,
		},
		&cli.BoolFlag{
			Name:        "no24hcheck",
			Aliases:     []string{"no24h-check", "no24h", "n"},
			Usage:       "Disable the 24-hour rule for considering the chain current",
			Destination: &cfg.No24hCheck,
		},
		&cli.StringFlag{
			Name:        "walletpass",
			Usage:       "wallet password",
			Destination: &cfg.WalletPass,
		},
		&cli.IntFlag{
			Name:        "evmtrietimeout",
			Usage:       "Set the interval time(seconds) for flush evm trie to disk",
			Destination: &cfg.EVMTrieTimeout,
		},
		&cli.StringFlag{
			Name:        "state.scheme",
			Usage:       "Scheme to use for storing ethereum state ('hash' or 'path')",
			Destination: &cfg.StateScheme,
		},
		&cli.IntFlag{
			Name:        "obsoleteheight",
			Usage:       "What is the maximum allowable height of block obsolescence for submission",
			Value:       defaultObsoleteHeight,
			Destination: &cfg.ObsoleteHeight,
		},
		&cli.BoolFlag{
			Name:        "allowsubmitwhennotsynced",
			Usage:       "Allow the node to accept blocks from RPC while not synced (this flag is mainly used for testing)",
			Value:       false,
			Destination: &cfg.SubmitNoSynced,
		},
		&cli.BoolFlag{
			Name:        "allowminingwhennearlysynced",
			Usage:       "Allow mining RPC work when the node is chain-current and within stable confirmations of sync peers",
			Value:       false,
			Destination: &cfg.AllowMiningWhenNearlySynced,
		},
		&cli.IntFlag{
			Name:        "gbttimeout",
			Usage:       "Build block template timeout by Millisecond.(Can limit the number of transactions included in the block)",
			Destination: &cfg.GBTTimeOut,
		},
		&cli.Uint64Flag{
			Name:        "upgradeactivationdelay",
			Usage:       "Layers after lock-in before a consensus upgrade activates",
			Destination: &cfg.UpgradeActivationDelayBlocks,
		},
		&cli.StringFlag{
			Name:        "health-addr",
			Usage:       "Address to listen for health checks",
			Value:       defaultHealthAddr,
			Destination: &cfg.HealthAddr,
		},
	}
)

// LocationFlags returns the subset of bdag flags that select *which*
// on-disk node a subcommand operates on (data dir, home dir, network
// selectors).
//
// They are duplicated here so subcommands can declare them locally — by
// default urfave/cli accepts App-level flags only BEFORE the subcommand
// name, which is unintuitive for one-shot tools like `bdag snap export`.
// Adding the same flags on the subcommand makes `bdag snap export
// --datadir /foo` work in addition to `bdag --datadir /foo snap export`.
//
// IMPORTANT: these flags deliberately do NOT bind a Destination. If they
// did, urfave/cli would overwrite the package-level `cfg.*` fields with
// each flag's zero/default value during the subcommand's FlagSet setup,
// clobbering whatever the App-level flags already wrote there. Instead,
// callers should run [ApplyLocationOverrides] from a `Before` hook so
// subcommand-level values are folded back into `cfg` only when the user
// actually set them.
func LocationFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "datadir",
			Aliases: []string{"b"},
			Usage:   "Directory to store data",
		},
		&cli.StringFlag{
			Name:    "appdata",
			Aliases: []string{"A"},
			Usage:   "Path to application home directory",
		},
		&cli.BoolFlag{
			Name:  "testnet",
			Usage: "Use the test network",
		},
		&cli.BoolFlag{
			Name:  "mixnet",
			Usage: "Use the test mix pow network",
		},
		&cli.BoolFlag{
			Name:  "privnet",
			Usage: "Use the private network",
		},
		&cli.BoolFlag{
			Name:  "devnet",
			Usage: "Use the development network",
		},
	}
}

// ApplyLocationOverrides copies subcommand-level location flag values
// (those declared via [LocationFlags]) into the package-level config
// when — and ONLY when — the user actually passed them on the
// subcommand. Flags that weren't passed are left alone so the values
// already written by the App-level flag parser stay intact.
//
// Call this from each subcommand's `Before` hook BEFORE [LoadConfig]
// runs, so downstream config sanitisation (network selection, datadir
// expansion, …) sees the final, user-intended values.
func ApplyLocationOverrides(c *cli.Context) {
	if c.IsSet("datadir") {
		cfg.DataDir = c.String("datadir")
	}
	if c.IsSet("appdata") {
		cfg.HomeDir = c.String("appdata")
	}
	if c.IsSet("testnet") {
		cfg.TestNet = c.Bool("testnet")
	}
	if c.IsSet("mixnet") {
		cfg.MixNet = c.Bool("mixnet")
	}
	if c.IsSet("privnet") {
		cfg.PrivNet = c.Bool("privnet")
	}
	if c.IsSet("devnet") {
		cfg.DevNet = c.Bool("devnet")
	}
}

func DefaultConfig(homeDir string) *config.Config {
	cfg := &config.Config{
		HomeDir:                      defaultHomeDir,
		ConfigFile:                   defaultConfigFile,
		DebugLevel:                   defaultLogLevel,
		DebugPrintOrigins:            defaultDebugPrintOrigins,
		DataDir:                      defaultDataDir,
		LogDir:                       defaultLogDir,
		DbType:                       defaultDbType,
		RPCKey:                       defaultRPCKeyFile,
		RPCCert:                      defaultRPCCertFile,
		RPCMaxClients:                defaultMaxRPCClients,
		RPCMaxClientsPerHost:         defaultMaxRPCClientsPerHost,
		RPCMaxConcurrent:             defaultRPCMaxConcurrent,
		RPCCriticalConcurrent:        defaultRPCCriticalConcurrent,
		RPCMaxWebsockets:             defaultMaxRPCWebsockets,
		RPCMaxConcurrentReqs:         defaultMaxRPCConcurrentReqs,
		RPCRequestTimeout:            defaultRPCRequestTimeout,
		MaxPeers:                     defaultMaxPeers,
		P2PRPCConcurrency:            defaultP2PRPCConcurrency,
		EVMNativeCatchupBatchOrders:  defaultEVMNativeCatchupBatchOrders,
		P2PStreamReuse:               defaultP2PStreamReuse,
		MinTxFee:                     defaultMinRelayTxFee,
		BlockMinSize:                 defaultBlockMinSize,
		BlockMaxSize:                 defaultBlockMaxSize,
		SigCacheMaxSize:              defaultSigCacheMaxSize,
		MiningStateSync:              defaultMiningStateSync,
		DAGType:                      defaultDAGType,
		NodeName:                     "BDAG",
		Banning:                      true,
		MaxInbound:                   defaultMaxInboundPeersPerHost,
		InvalidTxIndex:               defaultInvalidTxIndex,
		TxHashIndex:                  defaultTxhashIndex,
		NTP:                          false,
		MempoolExpiry:                defaultMempoolExpiry,
		AcceptNonStd:                 true,
		RPCUser:                      defaultRPCUser,
		RPCPass:                      defaultRPCPass,
		ObsoleteHeight:               defaultObsoleteHeight,
		SubmitNoSynced:               false,
		AllowMiningWhenNearlySynced:  false,
		GBTTimeOut:                   defaultGBTTimeout,
		HealthAddr:                   defaultHealthAddr,
		GatewayHTTPTimeout:           defaultGatewayHTTPTimeout,
		GatewayRequestTimeout:        defaultGatewayRequestTimeout,
		GatewayTxCacheTTL:            defaultGatewayTxCacheTTL,
		GatewayTxCacheMax:            defaultGatewayTxCacheMax,
		GatewayReadTimeout:           defaultGatewayReadTimeout,
		GatewayWriteTimeout:          defaultGatewayWriteTimeout,
		GatewayIdleTimeout:           defaultGatewayIdleTimeout,
		GatewayMaxRequestBytes:       defaultGatewayMaxRequestBytes,
		GatewayRateLimit:             defaultGatewayRateLimit,
		GatewayRateBurst:             defaultGatewayRateBurst,
		GatewayMaxConcurrent:         defaultGatewayMaxConcurrent,
		GatewayCriticalReserve:       defaultGatewayCriticalReserve,
		UpgradeActivationDelayBlocks: 0,
	}
	if len(homeDir) > 0 {
		hd, err := filepath.Abs(homeDir)
		if err != nil {
			panic(err)
		}
		cfg.HomeDir = hd
		cfg.ConfigFile = filepath.Join(cfg.HomeDir, defaultConfigFilename)
		cfg.DataDir = filepath.Join(cfg.HomeDir, defaultDataDirname)
		cfg.LogDir = filepath.Join(cfg.HomeDir, defaultLogDirname)
		cfg.RPCKey = filepath.Join(cfg.HomeDir, "rpc.key")
		cfg.RPCCert = filepath.Join(cfg.HomeDir, "rpc.cert")
	}
	return cfg
}
