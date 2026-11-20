package bdag

import (
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	mconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	mcommon "github.com/BlockdagNetworkLabs/bdag/bevm/common"
	"github.com/BlockdagNetworkLabs/bdag/bevm/eth"
	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/params"
	"github.com/urfave/cli/v2"
)

var (
	// ClientIdentifier is a hard coded identifier to report into the network.
	ClientIdentifier = "bdageth"

	exclusionFlags = append([]cli.Flag{
		utils.TxPoolLocalsFlag,
		utils.TxPoolNoLocalsFlag,
		utils.SyncTargetFlag,
		utils.ListenPortFlag,
		utils.DiscoveryPortFlag,
		utils.MaxPeersFlag,
		utils.LightMaxPeersFlag,
		utils.MiningEnabledFlag,
		utils.MinerEtherbaseFlag,
		utils.NATFlag,
		utils.NoDiscoverFlag,
		utils.DiscoveryV5Flag,
		utils.NetrestrictFlag,
		utils.DNSDiscoveryFlag,
		// Exclude HTTP/WS/GraphQL flags to prevent internal EVM from starting conflicting listeners
		utils.HTTPEnabledFlag,
		utils.HTTPListenAddrFlag,
		utils.HTTPPortFlag,
		utils.HTTPApiFlag,
		utils.HTTPCORSDomainFlag,
		utils.HTTPVirtualHostsFlag,
		utils.WSEnabledFlag,
		utils.WSListenAddrFlag,
		utils.WSPortFlag,
		utils.WSApiFlag,
		utils.WSAllowedOriginsFlag,
		utils.GraphQLEnabledFlag,
		utils.GraphQLCORSDomainFlag,
		utils.GraphQLVirtualHostsFlag,
	}, utils.NetworkFlags...)
)

// parseGenesisAllocEntry parses "0xADDR:AMOUNT" where AMOUNT is in BDAG (ether units).
func parseGenesisAllocEntry(s string) (common.Address, *big.Int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return common.Address{}, nil, fmt.Errorf("expected format 0xADDR:AMOUNT, got %q", s)
	}
	addr := common.HexToAddress(strings.TrimSpace(parts[0]))
	f, ok := new(big.Float).SetPrec(256).SetString(strings.TrimSpace(parts[1]))
	if !ok {
		return common.Address{}, nil, fmt.Errorf("invalid amount %q", parts[1])
	}
	wei, _ := new(big.Float).Mul(f, new(big.Float).SetPrec(256).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))).Int(nil)
	return addr, wei, nil
}

// validateMainnetRehearsalOverrides rejects local fixture settings before
// constructing genesis or opening node state. Values are deliberately omitted
// from errors: configuration may contain private runtime material.
func validateMainnetRehearsalOverrides(cfg *config.Config) error {
	if qparams.ActiveNetParams.Net != protocol.MainNet {
		return nil
	}
	if len(cfg.GenesisAlloc) != 0 {
		return fmt.Errorf("Mainnet cannot use rehearsal genesis allocations")
	}
	for _, name := range []string{
		"STAKING_CONTRACT_ADDR", mparams.StakingV2ForkBlockEnv,
		mparams.GovernanceForkBlockEnv, mparams.GovernanceForkConfigEnv,
		"BLOCKLIST_GENESIS_BLOCKED", "BLOCKLIST_GENESIS_OWNER", "BLOCKLIST_CONTRACT_CODE",
	} {
		if os.Getenv(name) != "" {
			return fmt.Errorf("Mainnet cannot use rehearsal override %s; unset it", name)
		}
	}
	return nil
}

func MakeConfig(cfg *config.Config) (*eth.Config, error) {
	if err := validateMainnetRehearsalOverrides(cfg); err != nil {
		return nil, err
	}
	datadir := cfg.DataDir
	genesis := CurrentGenesis()

	for _, entry := range cfg.GenesisAlloc {
		addr, amount, err := parseGenesisAllocEntry(entry)
		if err != nil {
			log.Warn("Skipping invalid --genesisalloc entry", "entry", entry, "err", err)
			continue
		}
		AddGenesisAlloc(genesis, addr, amount)
		bdagAmt := new(big.Float).Quo(new(big.Float).SetInt(amount), new(big.Float).SetPrec(256).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
		log.Info("Genesis alloc applied", "address", addr.Hex(), "wei", amount.String(), "bdag", bdagAmt.Text('f', 4))
	}

	// Dev/test only: optional blocklist governance predeploy/seed. No-op (genesis
	// hash unchanged) unless the BLOCKLIST_GENESIS_* variables are set.
	ApplyBlocklistGenesisSeed(genesis)

	econfig := ethconfig.Defaults

	chainID := genesis.Config.ChainID.Uint64()
	econfig.NetworkId = chainID
	econfig.Genesis = genesis
	log.Info("EVM genesis", "network", qparams.ActiveNetParams.Name, "chainId", chainID)
	econfig.NoPruning = false
	econfig.SkipBcVersionCheck = false
	econfig.ConsensusEngine = createConsensusEngine
	if len(cfg.GetMinningAddrs()) > 0 {
		econfig.Miner.Etherbase = cfg.GetMinningAddrs()[0]
	}
	econfig.Miner.ExtraData = mconsensus.EncodeRewardExtra(0)
	econfig.Miner.External = &BdagPool{}

	econfig.TxPool.NoLocals = false

	if cfg.EVMTrieTimeout > 0 {
		econfig.TrieTimeout = time.Second * time.Duration(cfg.EVMTrieTimeout)
	}
	if len(cfg.StateScheme) > 0 {
		econfig.StateScheme = cfg.StateScheme
	}

	nodeConf := node.DefaultConfig

	nodeConf.DataDir = datadir
	nodeConf.Name = ClientIdentifier
	nodeConf.Version = params.VersionWithMeta
	nodeConf.HTTPModules = append(nodeConf.HTTPModules, "eth")
	nodeConf.WSModules = append(nodeConf.WSModules, "eth")
	nodeConf.IPCPath = ""
	if len(datadir) > 0 {
		nodeConf.KeyStoreDir = filepath.Join(datadir, "keystore")
	}
	//nodeConf.HTTPHost = node.DefaultHTTPHost
	//nodeConf.WSHost = node.DefaultWSHost
	nodeConf.HTTPPort, nodeConf.WSPort, nodeConf.AuthPort = getDefaultRPCPort()

	nodeConf.P2P.MaxPeers = 0
	nodeConf.P2P.DiscoveryV5 = false
	nodeConf.P2P.NoDiscovery = true
	nodeConf.P2P.NoDial = true
	nodeConf.P2P.ListenAddr = ""
	nodeConf.P2P.NAT = nil

	db, _ := enode.OpenDB("")
	key, _ := crypto.GenerateKey()
	ln := enode.NewLocalNode(db, key)
	ln.SetFallbackIP(net.IP{127, 0, 0, 1})
	ln.SetFallbackUDP(8538)
	nodeConf.P2P.BootstrapNodes = []*enode.Node{ln.Node()}
	//
	return &eth.Config{
		Eth:     econfig,
		Node:    nodeConf,
		Metrics: metrics.DefaultConfig,
	}, nil
}

func MakeParams(cfg *config.Config) (*eth.Config, []string, error) {
	ecfg, err := MakeConfig(cfg)
	if err != nil {
		return ecfg, nil, err
	}
	args, err := mcommon.ProcessEnv(cfg.EVMEnv, ecfg.Node.Name, exclusionFlags)
	return ecfg, args, err
}

func getDefaultRPCPort() (int, int, int) {
	// Disable internal EVM RPC listeners as they are now served via the in-process Gateway.
	return 0, 0, 0
}

func createConsensusEngine(config *params.ChainConfig, db ethdb.Database) (consensus.Engine, error) {
	return mconsensus.New(db), nil
}

func ChainConfig() *params.ChainConfig {
	switch qparams.ActiveNetParams.Net {
	case protocol.MainNet:
		return mparams.BdagMainnetChainConfig
	case protocol.TestNet:
		return mparams.BdagTestnetChainConfig
	case protocol.MixNet:
		return mparams.BdagMixnetChainConfig
	case protocol.PrivNet:
		return mparams.BdagPrivnetChainConfig
	case protocol.DevNet:
		return mparams.BdagDevnetChainConfig
	}
	return nil
}

func Genesis(net protocol.Network, alloc types.GenesisAlloc) *core.Genesis {
	switch net {
	case protocol.MainNet:
		return BdagGenesis(alloc)
	case protocol.TestNet:
		return BdagTestnetGenesis(alloc)
	case protocol.MixNet:
		return BdagMixnetGenesis(alloc)
	case protocol.PrivNet:
		return BdagPrivnetGenesis(alloc)
	case protocol.DevNet:
		return BdagDevnetGenesis(alloc)
	}
	return nil
}

func CurrentGenesis() *core.Genesis {
	return Genesis(qparams.ActiveNetParams.Net, nil)
}
