package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

const (
	DefaultEVMRPCAddr = "127.0.0.1"
	DefaultEVMRPCPort = 28545
)

type Config struct {
	HomeDir                     string        `short:"A" long:"appdata" description:"Path to application home directory"`
	ShowVersion                 bool          `short:"V" long:"version" description:"Display version information and exit"`
	ConfigFile                  string        `short:"C" long:"configfile" description:"Path to configuration file"`
	DataDir                     string        `short:"b" long:"datadir" description:"Directory to store data"`
	LogDir                      string        `long:"logdir" description:"Directory to log output."`
	LogRotatorSize              int64         `long:"logrotatorsize" description:"unit KB."`
	NoFileLogging               bool          `long:"nofilelogging" description:"Disable file logging."`
	Listener                    string        `long:"listen" description:"Add an IP to listen for connections"`
	DefaultPort                 string        `long:"port" description:"Default p2p port."`
	RPCListeners                []string      `long:"rpclisten" json:"-" description:"Add an IP to listen for DAG JSON-RPC gateway"`
	GatewayDagAddr              string        `long:"dag.http.addr" description:"DAG HTTP listening interface"`
	GatewayDagPort              int           `long:"dag.http.port" description:"DAG HTTP listening port"`
	GatewayEvmAddr              string        `long:"evm.http.addr" description:"EVM HTTP listening interface"`
	GatewayEvmPort              int           `long:"evm.http.port" description:"EVM HTTP listening port"`
	GatewayEvmCORSDomain        string        `long:"evm.http.corsdomain" description:"Comma separated list of domains from which to accept cross origin requests (browser enforced)"`
	GatewayEVMAPI               string        `long:"evm.http.api" description:"EVM API modules to enable (e.g. eth,net,web3,debug)"`
	GatewayEvmWSPort            int           `long:"evm.ws.port" description:"EVM WebSocket listening port"`
	GatewayEvmWSAddr            string        `long:"evm.ws.addr" description:"EVM WebSocket listening interface"`
	GatewayMaxRequestBytes      int64         `long:"http.maxrequestbytes" description:"Maximum size of HTTP request body in bytes"`
	GatewayRateLimit            float64       `long:"http.ratelimit" description:"HTTP requests per second allowed per client IP"`
	GatewayRateBurst            int           `long:"http.rateburst" description:"Burst size for HTTP rate limiter"`
	GatewayMaxConcurrent        int           `long:"http.maxconcurrent" description:"Maximum number of concurrent non-critical gateway requests"`
	GatewayCriticalReserve      int           `long:"http.criticalreserve" description:"Reserved concurrent gateway slots for critical local RPC requests"`
	MaxPeers                    int           `long:"maxpeers" description:"Max number of inbound and outbound peers"`
	P2PRPCConcurrency           int           `long:"p2prpcconcurrency" description:"Max number of concurrent inbound P2P RPC stream handlers (inventory, getblockdatas, chainstate, etc.) processed at once across all connected peers; excess arrivals queue up to their own request timeout rather than piling onto blockdag.BDAG.stateLock in parallel"`
	EVMNativeCatchupBatchOrders uint64        `long:"evmnativecatchupbatchorders" description:"Max DAG orders replayed per EVM-native-state catch-up batch. This background task holds the same exclusive chain lock block import needs, for the duration of each batch's EVM replay -- lower this if import shows periodic multi-second/minute pauses under a large sync backlog, at the cost of the EVM-native view falling behind for more, smaller batches"`
	P2PStreamReuse              bool          `long:"p2pstreamreuse" description:"Reuse a pooled libp2p stream per peer for the hot sync RPCs (syncdag, continuesyncdag, getblockdatas, getblocks) instead of opening a new stream per request. Only skips the post-request half-close against peers advertising StreamReuseProtocolVersion (v47+); older peers keep the one-shot half-close path. Disabled by default; enable once validated on this network."`
	DisableListen               bool          `long:"nolisten" description:"Disable listening for incoming connections"`
	RPCUser                     string        `short:"u" long:"rpcuser" description:"Username for DAG RPC connections"`
	RPCPass                     string        `short:"P" long:"rpcpass" default-mask:"-" description:"Password for DAG RPC connections"`
	RPCLimitUser                string        `long:"rpclimituser" description:"Username for limited read-only DAG RPC connections"`
	RPCLimitPass                string        `long:"rpclimitpass" default-mask:"-" description:"Password for limited read-only DAG RPC connections"`
	RPCCert                     string        `long:"rpccert" description:"File containing the certificate file"`
	RPCKey                      string        `long:"rpckey" description:"File containing the certificate key"`
	RPCMaxClients               int           `long:"rpcmaxclients" description:"Max number of RPC clients for standard connections"`
	RPCMaxClientsPerHost        int           `long:"rpcmaxclientsperhost" description:"Max number of standard RPC clients from a single remote host"`
	RPCMaxConcurrent            int           `long:"rpcmaxconcurrent" description:"Max number of concurrent normal standard RPC requests"`
	RPCCriticalConcurrent       int           `long:"rpccriticalconcurrent" description:"Additional reserved concurrent standard RPC requests for block submission"`
	RPCRequestTimeout           time.Duration `long:"rpcrequesttimeout" description:"Timeout for standard RPC request processing"`
	DisableRPC                  bool          `long:"norpc" description:"Disable built-in RPC server -- NOTE: The RPC server is disabled by default if no rpcuser/rpcpass or rpclimituser/rpclimitpass is specified"`
	DisableTLS                  bool          `long:"notls" description:"Disable TLS for the RPC server -- NOTE: This is only allowed if the RPC server is bound to localhost"`
	Modules                     []string      `long:"modules" description:"Modules is a list of API modules(See GetNodeInfo) to expose via the HTTP RPC interface. If the module list is empty, all RPC API endpoints designated public will be exposed."`
	DisableCheckpoints          bool          `long:"nocheckpoints" description:"Disable built-in checkpoints.  Don't do this unless you know what you're doing."`
	LightNode                   bool          `long:"light" description:"start as a Blockdag light node"`
	SigCacheMaxSize             uint          `long:"sigcachemaxsize" description:"The maximum number of entries in the signature verification cache"`
	TestNet                     bool          `long:"testnet" description:"Use the test network"`
	MixNet                      bool          `long:"mixnet" description:"Use the test mix pow network"`
	PrivNet                     bool          `long:"privnet" description:"Use the private network"`
	DevNet                      bool          `long:"devnet" description:"Use the development network"`
	DbType                      string        `long:"dbtype" description:"Database backend to use for the Block Chain"`
	Profile                     string        `long:"profile" description:"Enable HTTP profiling on given [addr:]port -- NOTE port must be between 1024 and 65536"`
	CPUProfile                  string        `long:"cpuprofile" description:"Write CPU profile to the specified file"`
	TrackHeap                   bool          `long:"trackheap" description:"tracks the size of the heap and dumps a profile"`
	TrackHeapLimit              int           `long:"trackheaplimit" description:"track heap when limit in gigabytes (default:7G)"`
	HealthAddr                  string        `long:"health" description:"Address for HTTP health service (host:port)"`
	GatewayHTTPTimeout          time.Duration `long:"httptimeout" description:"HTTP client timeout for gateway"`
	GatewayRequestTimeout       time.Duration `long:"requesttimeout" description:"Timeout for gateway proxy requests"`
	GatewayReadTimeout          time.Duration `long:"http.readtimeout" description:"Read timeout for gateway HTTP servers"`
	GatewayWriteTimeout         time.Duration `long:"http.writetimeout" description:"Write timeout for gateway HTTP servers"`
	GatewayIdleTimeout          time.Duration `long:"http.idletimeout" description:"Idle timeout for gateway HTTP keep-alive connections"`
	GatewayTxCacheTTL           time.Duration `long:"txcachettl" description:"TTL for gateway transaction cache"`
	GatewayTxCacheMax           int           `long:"txcachemax" description:"Maximum entries for gateway transaction cache"`
	DebugLevel                  string        `short:"d" long:"debuglevel" description:"Logging level {trace, debug, info, warn, error, critical} "`
	DebugPrintOrigins           bool          `long:"printorigin" description:"Print log debug location (file:line) "`

	// MemPool Config
	NoRelayPriority  bool    `long:"norelaypriority" description:"Do not require free or low-fee transactions to have high priority for relaying"`
	FreeTxRelayLimit float64 `long:"limitfreerelay" description:"Limit relay of transactions with no transaction fee to the given amount in thousands of bytes per minute"`
	AcceptNonStd     bool    `long:"acceptnonstd" description:"Accept and relay non-standard transactions to the network regardless of the default settings for the active network."`
	MaxOrphanTxs     int     `long:"maxorphantx" description:"Max number of orphan transactions to keep in memory"`
	TxTimeScope      int64   `long:"txtimescope" description:"allow the mempool tx time scope(sec) with server time,default 0 will not check the time scope"`
	MinTxFee         int64   `long:"mintxfee" description:"The minimum transaction fee in AtomBDAG/kB."`
	MempoolExpiry    int64   `long:"mempoolexpiry" description:"Do not keep transactions in the mempool more than mempoolexpiry"`
	Persistmempool   bool    `long:"persistmempool" description:"Whether to save the mempool on shutdown and load on restart"`
	NoMempoolBar     bool    `long:"nomempoolbar" description:"Whether to show progress bar when load mempool from file"`
	// Miner
	Miner                       bool     `long:"miner" description:"Enable miner module"`
	MiningAddrs                 []string `long:"miningaddr" description:"Add the specified payment address to the list of addresses to use for generated blocks -- At least one address is required if the generate option is set"`
	MiningTimeOffset            int      `long:"miningtimeoffset" description:"Offset the mining timestamp of a block by this many seconds (positive values are in the past)"`
	BlockMinSize                uint32   `long:"blockminsize" description:"Mininum block size in bytes to be used when creating a block"`
	BlockMaxSize                uint32   `long:"blockmaxsize" description:"Maximum block size in bytes to be used when creating a block"`
	BlockPrioritySize           uint32   `long:"blockprioritysize" description:"Size in bytes for high-priority/low-fee transactions when creating a block"`
	miningAddrs                 []ethcommon.Address
	GBTNotify                   []string `long:"gbtnotify" description:"HTTP URL list to be notified of new block template"`
	ObsoleteHeight              int      `long:"obsoleteheight" description:"What is the maximum allowable height of block obsolescence for submission"`
	SubmitNoSynced              bool     `long:"allowsubmitwhennotsynced" description:"Allow the node to accept blocks from RPC while not synced (this flag is mainly used for testing)"`
	AllowMiningWhenNearlySynced bool     `long:"allowminingwhennearlysynced" description:"Allow mining RPC work when the node is chain-current and within stable confirmations of sync peers"`
	GBTTimeOut                  int      `long:"gbttimeout" description:"Build block template timeout by Millisecond.(Can limit the number of transactions included in the block)"`

	//WebSocket support
	RPCMaxWebsockets     int `long:"rpcmaxwebsockets" description:"Max number of RPC websocket connections"`
	RPCMaxConcurrentReqs int `long:"rpcmaxconcurrentreqs" description:"Max number of concurrent RPC requests that may be processed concurrently"`
	//P2P
	BlocksOnly      bool     `long:"blocksonly" description:"Do not accept transactions from remote peers."`
	MiningStateSync bool     `long:"miningstatesync" description:"Synchronizing the mining state with other nodes"`
	AddPeers        []string `short:"a" long:"addpeer" description:"Add a peer to connect with at startup"`
	Upnp            bool     `long:"upnp" description:"Use UPnP to map our listening port outside of NAT"`
	MaxInbound      int      `long:"maxinbound" description:"The max total of inbound peer for host"`
	//P2P - server ban
	Banning bool `long:"banning" description:"Enable banning of misbehaving peers"`

	DAGType     string `short:"G" long:"dagtype" description:"DAG type {phantom,conflux,spectre} "`
	NodeName    string `long:"name" description:"Custom name for this node advertised to peers"`
	Cleanup     bool   `short:"L" long:"cleanup" description:"Cleanup the block database "`
	CleanupTips bool   `long:"cleanuptips" description:"Cleanup the block database tips"`
	BuildLedger bool   `long:"buildledger" description:"Generate the genesis ledger for the next Blockdag version."`

	Zmqpubhashblock string `long:"zmqpubhashblock" description:"Enable publish hash block  in <address>"`
	Zmqpubrawblock  string `long:"zmqpubrawblock" description:"Enable publish raw block in <address>"`

	Zmqpubhashtx string `long:"zmqpubhashtx" description:"Enable publish hash transaction in <address>"`
	Zmqpubrawtx  string `long:"zmqpubrawtx" description:"Enable publish raw transaction in <address>"`

	// index
	AddrIndex      bool `long:"addrindex" description:"Maintain a full address-based transaction index which makes the getrawtransactions RPC available"`
	InvalidTxIndex bool `long:"invalidtxindex" description:"Cache invalid transactions."`
	TxHashIndex    bool `long:"txhashindex" description:"Cache transaction full hash."`
	DropAddrIndex  bool `long:"dropaddrindex" description:"Deletes the address-based transaction index from the database on start up and then exits."`

	NTP bool `long:"ntp" description:"Auto sync time."`

	//net2.0
	BootstrapNodes                []string `long:"bootstrapnode" description:"The address of bootstrap node."`
	P2PStaticPeersOnly            bool     `long:"p2pstaticpeersonly" description:"Only connect to peers explicitly configured with addpeer, bootstrapnode, or relaynode /p2p/<peer-id> multiaddresses"`
	P2PDiscoveryTargetLivePeers   int      `long:"p2pdiscoverytargetlivepeers" description:"Advisory live-consensus-peer target used by P2P discovery health"`
	P2PDiscoveryTargetUsefulPeers int      `long:"p2pdiscoverytargetusefulpeers" description:"Advisory fresh-compatible-peer target used by P2P discovery health"`
	P2PWSOverHTTP                 bool     `long:"p2p-ws-over-http" description:"Enable P2P over WebSocket instead of raw TCP"`
	MetaDataDir                   string   `long:"metadatadir" description:"meta data dir for p2p"`
	P2PTCPPort                    int      `long:"p2ptcpport" description:"The tcp port used by P2P."`
	HostIP                        string   `long:"externalip" description:"The IP address advertised by libp2p. This may be used to advertise an external IP."`
	HostDNS                       string   `long:"externaldns" description:"The DNS address advertised by libp2p. This may be used to advertise an external DNS."`
	RelayNode                     string   `long:"relaynode" description:"The address of relay node that routes traffic between two peers over a Blockdag “relay” peer."`
	Whitelist                     []string `long:"whitelist" description:"Add an IP network or IP,PeerID that will not be banned or ignore dual channel mode detection. (eg. 192.168.1.0/24 or ::1 or [peer id])"`
	Blacklist                     []string `long:"blacklist" description:"Add some IP network or IP that will be banned. (eg. 192.168.1.0/24 or ::1)"`
	MaxBadResp                    int      `long:"maxbadresp" description:"maxbadresp is the maximum number of bad responses from a peer before we stop talking to it."`
	Circuit                       bool     `long:"circuit" description:"All peers will ignore dual channel mode detection"`
	Consistency                   bool     `long:"consistency" description:"Detect data consistency through P2P"`
	// bevm environment
	EVMEnv       string   `long:"evmenv" description:"bdag EVM environment"`
	GenesisAlloc []string `long:"genesisalloc" description:"Pre-fund an EVM address at genesis. Format: 0xADDR:AMOUNT where AMOUNT is in BDAG (ether units). Repeatable. Only applies to a fresh chain."`

	Estimatefee bool `long:"estimatefee" description:"Enable estimate fee"`

	AcctMode      bool `long:"acctmode" description:"Enable support account system mode"`
	IsArchival    bool `long:"archival" description:"Archival tells the consensus if it should not prune old blocks"`
	IsArchiveNode bool
	// UpgradeActivationDelayBlocks specifies the number of layers between an upgrade
	// locking in and activating. When set to zero the network default is used.
	UpgradeActivationDelayBlocks uint64 `long:"upgradeactivationdelay" description:"Layers after lock-in before a consensus upgrade activates"`

	DAGCacheSize       uint64 `long:"dagcachesize" description:"DAG block cache size"`
	BlockDataCacheSize uint64 `long:"bdcachesize" description:"Block data cache size"`

	Metrics          bool `long:"metrics" description:"Enable metrics collection and reporting"`
	MetricsExpensive bool `long:"metrics.expensive" description:"Enable expensive metrics collection and reporting"`

	Minfreedisk uint64 `long:"minfreedisk" description:"Minimum free disk space in MB, once reached triggers auto shut down (default = 512M, 0 = disabled)"`

	Cache         int `long:"cache" description:"Megabytes of memory allocated to internal caching (default = 1024 mainnet full node)"`
	CacheDatabase int `long:"cache.database" description:"Percentage of cache memory allowance to use for database io"`
	CacheSnapshot int `long:"cache.snapshot" description:"Percentage of cache memory allowance to use for snapshot caching (default = 5% full mode)"`

	EVMTrieTimeout int    `long:"evmtrietimeout" description:"Set the interval time(seconds) for flush evm trie to disk"`
	StateScheme    string `long:"state.scheme" description:"Scheme to use for storing ethereum state ('hash' or 'path')"`
	// wallet
	WalletPass     string
	AutoCollectEvm bool `long:"autocollectevm" description:"auto collect utxo to evm"`
	No24hCheck     bool `long:"no24hcheck" description:"Disable the 24-hour rule for considering the chain current"`
}

// ValidateRPCAuth checks the two native HTTP authentication principals without
// exposing either credential in errors or diagnostics. An absent limited pair
// keeps standalone Core installations primary-only.
func (c *Config) ValidateRPCAuth() error {
	primaryUserSet := c.RPCUser != ""
	primaryPassSet := c.RPCPass != ""
	if primaryUserSet != primaryPassSet {
		return fmt.Errorf("rpc credentials must be provided as a complete pair")
	}
	limitedUserSet := c.RPCLimitUser != ""
	limitedPassSet := c.RPCLimitPass != ""
	if limitedUserSet != limitedPassSet {
		return fmt.Errorf("limited rpc credentials must be provided as a complete pair")
	}
	if !limitedUserSet {
		return nil
	}
	if !primaryUserSet {
		return fmt.Errorf("limited rpc credentials require a valid primary rpc credential pair")
	}
	primary := rpcAuthDigest(c.RPCUser, c.RPCPass)
	limited := rpcAuthDigest(c.RPCLimitUser, c.RPCLimitPass)
	if subtle.ConstantTimeCompare(primary[:], limited[:]) == 1 {
		return fmt.Errorf("limited rpc credentials must differ from primary rpc credentials")
	}
	return nil
}

func rpcAuthDigest(user, pass string) [sha256.Size]byte {
	return sha256.Sum256([]byte("Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))))
}

func (c *Config) GetMinningAddrs() []ethcommon.Address {
	return c.miningAddrs
}

func (c *Config) SetMiningAddrs(addr ethcommon.Address) {
	c.miningAddrs = append(c.miningAddrs, addr)
}

func (c *Config) ResolveDataPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.DataDir, path)
}

func (c *Config) DatabaseCache() int {
	return c.Cache * c.CacheDatabase / 100
}

func (c *Config) SnapshotCache() int {
	return c.Cache * c.CacheSnapshot / 100
}

var Cfg *Config

func (c *Config) GatewayEVMAdvertisePort() int {
	if c.GatewayEvmPort != 0 {
		return c.GatewayEvmPort
	}
	_, port := ParseEVMRPC(c.EVMEnv)
	return port
}

func ParseEVMRPC(env string) (string, int) {
	host := DefaultEVMRPCAddr
	port := DefaultEVMRPCPort
	if unquoted, err := strconv.Unquote(env); err == nil {
		env = unquoted
	}
	fields := strings.Fields(env)
	for i, f := range fields {
		switch {
		case strings.HasPrefix(f, "--http.addr="):
			host = strings.TrimPrefix(f, "--http.addr=")
		case f == "--http.addr" && i+1 < len(fields):
			host = fields[i+1]
		case strings.HasPrefix(f, "--http.port="):
			if p, err := strconv.Atoi(strings.TrimPrefix(f, "--http.port=")); err == nil {
				port = p
			}
		case f == "--http.port" && i+1 < len(fields):
			if p, err := strconv.Atoi(fields[i+1]); err == nil {
				port = p
			}
		}
	}
	return host, port
}

// UpdateArchiveModeFromEVMEnv inspects the configured EVM environment flags and
// updates the archive mode indicator accordingly. The method detects both
// "--gcmode=archive" and "--gcmode archive" forms, regardless of flag casing.
func (c *Config) UpdateArchiveModeFromEVMEnv() {
	c.IsArchiveNode = hasArchiveGCMode(c.EVMEnv)
}

func hasArchiveGCMode(env string) bool {
	if env == "" {
		return false
	}
	if unquoted, err := strconv.Unquote(env); err == nil {
		env = unquoted
	}
	fields := strings.Fields(env)
	for i, field := range fields {
		if strings.EqualFold(field, "--gcmode=archive") {
			return true
		}
		if strings.EqualFold(field, "--gcmode") && i+1 < len(fields) && strings.EqualFold(fields[i+1], "archive") {
			return true
		}
	}
	return false
}
