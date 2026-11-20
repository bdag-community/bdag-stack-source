package common

import (
	"os"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

// Config for the p2p service.
// to initialize the p2p service.
type Config struct {
	EnableUPnP                 bool
	StaticPeers                []string
	StaticPeersOnly            bool
	BootstrapNodeAddr          []string
	DiscoveryTargetLivePeers   int
	DiscoveryTargetUsefulPeers int
	DataDir                    string
	MaxPeers                   uint
	RPCHandlerConcurrency      int
	MaxInbound                 int
	MetaDataDir                string
	ReadWritePermissions       os.FileMode
	AllowListCIDR              string
	DenyListCIDR               []string
	TCPPort                    uint
	WSOverHTTP                 bool
	EnableNoise                bool
	RelayNodeAddr              string
	LocalIP                    string
	HostAddress                string
	HostDNS                    string
	PrivateKey                 string
	Encoding                   string
	Services                   protocol.ServiceFlag
	UserAgent                  string
	// DisableRelayTx specifies if the remote peer should be informed to
	// not send inv messages for transactions.
	DisableRelayTx bool
	MaxOrphanTxs   int
	Params         *params.Params
	Banning        bool // Open or not ban module
	DisableListen  bool
	LANPeers       []string
	IsCircuit      bool
	Consistency    bool
	// EnableStreamReuse pools a persistent libp2p stream per peer for the
	// hot sync RPCs (syncdag, continuesyncdag, getblockdatas, getblocks)
	// instead of opening a new stream per request. Only takes effect
	// against peers advertising StreamReuseProtocolVersion (v47+); older
	// peers keep the one-shot half-close path. Disabled by default.
	EnableStreamReuse bool
}
