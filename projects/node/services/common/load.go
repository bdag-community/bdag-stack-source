// Copyright (c) 2015-2016 The Decred developers
// Copyright (c) 2013-2016 The btcsuite developers

package common

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	evmconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/config"
	consensusupgrades "github.com/BlockdagNetworkLabs/bdag/consensus/upgrades"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/BlockdagNetworkLabs/bdag/version"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gp "github.com/howeyc/gopass"
	"github.com/jessevdk/go-flags"
	"github.com/urfave/cli/v2"
)

func registerTestnetRewardGovernanceUpgrade() error {
	if params.ActiveNetParams != &params.TestNetParam {
		return nil
	}
	manifest := evmparams.BdagTestnetGovernanceFork
	if manifest == nil {
		if upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name] != nil {
			return fmt.Errorf("persisted reward governance cannot run on an unscheduled preparation build")
		}
		return nil
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("invalid immutable testnet governance manifest: %w", err)
	}
	want := consensusupgrades.RewardGovernanceV3Definition(manifest.SignalWindowStartLayer)
	if want.WindowSize != manifest.SignalWindowLayers || want.Threshold != manifest.SignalThresholdLayers || want.ActivationDelayBlocks != manifest.ActivationDelayLayers {
		return fmt.Errorf("testnet native signalling rules differ from the immutable EVM manifest")
	}
	if got := upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name]; got != nil {
		if got.Name == want.Name &&
			got.BitMask == want.BitMask &&
			got.WindowSize == want.WindowSize &&
			got.Threshold == want.Threshold &&
			got.ActivationDelayBlocks == want.ActivationDelayBlocks &&
			got.StartLayer == want.StartLayer &&
			got.TimeoutLayer == want.TimeoutLayer {
			return nil
		}
		return fmt.Errorf("testnet reward-governance definition conflicts with the immutable release manifest")
	}
	if err := upgrades.RegisterUpgrade(want); err != nil {
		return fmt.Errorf("register testnet reward-governance upgrade: %w", err)
	}
	return nil
}

// loadConfig initializes and parses the config using a config file and command
// line options.
func LoadConfig(ctx *cli.Context, parsefile bool) (*config.Config, error) {
	cfg.Modules = Modules.Value()
	cfg.MiningAddrs = MiningAddrs.Value()
	cfg.BlockMinSize = uint32(BlockMinSize)
	cfg.BlockMaxSize = uint32(BlockMaxSize)
	cfg.BlockPrioritySize = uint32(BlockPrioritySize)
	cfg.AddPeers = AddPeers.Value()
	cfg.BootstrapNodes = BootstrapNodes.Value()
	cfg.Whitelist = Whitelist.Value()
	cfg.Blacklist = Blacklist.Value()
	cfg.GBTNotify = GBTNotify.Value()
	cfg.RPCListeners = RPCListeners.Value()
	cfg.GenesisAlloc = GenesisAlloc.Value()

	// Show the version and exit if the version flag was specified.
	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	if cfg.ShowVersion {
		fmt.Printf("%s version %s (Go version %s)\n", appName, version.String(), runtime.Version())
		os.Exit(0)
	}

	usageMessage := fmt.Sprintf("Use %s -h to show usage", appName)

	// TODO
	// Perform service command and exit if specified.  Invalid service
	// commands show an appropriate error.  Only runs on Windows since
	// the runServiceCommand function will be nil when not on Windows.
	// TODO

	// Update the home directory for Blockdagd if specified. Since the home
	// directory is updated, other variables need to be updated to
	// reflect the new changes.
	if cfg.HomeDir != defaultHomeDir {
		cfg.HomeDir, _ = filepath.Abs(cfg.HomeDir)

		if cfg.ConfigFile == defaultConfigFile {
			defaultConfigFile = filepath.Join(cfg.HomeDir,
				defaultConfigFilename)
			cfg.ConfigFile = defaultConfigFile
		}
		if cfg.DataDir == defaultDataDir {
			cfg.DataDir = filepath.Join(cfg.HomeDir, defaultDataDirname)
		}
		if cfg.RPCKey == defaultRPCKeyFile {
			cfg.RPCKey = filepath.Join(cfg.HomeDir, "rpc.key")
		}
		if cfg.RPCCert == defaultRPCCertFile {
			cfg.RPCCert = filepath.Join(cfg.HomeDir, "rpc.cert")
		}
		if cfg.LogDir == defaultLogDir {
			cfg.LogDir = filepath.Join(cfg.HomeDir, defaultLogDirname)
		}
	}

	// TODO
	// Create a default config file when one does not exist and the user did
	// not specify an override.
	// TODO

	if ctx.IsSet("configfile") && parsefile {
		// Load additional config from file.
		parser := newConfigParser(cfg, flags.Default)
		err := flags.NewIniParser(parser).ParseFile(cfg.ConfigFile)
		if err != nil {
			if _, ok := err.(*os.PathError); !ok {
				fmt.Fprintf(os.Stderr, "Error parsing config "+
					"file: %v\n", err)
				fmt.Fprintln(os.Stderr, usageMessage)
				return nil, err
			}
			log.Warn(fmt.Sprintf("missing config file error:%s", err))
		}

		// Parse command line options again to ensure they take precedence.
		_, err = parser.Parse()
		if err != nil {
			if e, ok := err.(*flags.Error); !ok || e.Type != flags.ErrHelp {
				fmt.Fprintln(os.Stderr, usageMessage)
			}
			return nil, err
		}
	}

	// Create the home directory if it doesn't already exist.
	funcName := "loadConfig"
	err := os.MkdirAll(cfg.HomeDir, 0700)
	if err != nil {
		// Show a nicer error message if it's because a symlink is
		// linked to a directory that does not exist (probably because
		// it's not mounted).
		if e, ok := err.(*os.PathError); ok && os.IsExist(err) {
			if link, lerr := os.Readlink(e.Path); lerr == nil {
				str := "is symlink %s -> %s mounted?"
				err = fmt.Errorf(str, e.Path, link)
			}
		}
		str := "%s: failed to create home directory: %v"
		err := fmt.Errorf(str, funcName, err)
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	err = SetupConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, err
	}
	return cfg, nil
}

// newConfigParser returns a new command line flags parser.
func newConfigParser(cfg *config.Config, options flags.Options) *flags.Parser {
	parser := flags.NewParser(cfg, options)
	return parser
}

// parseAndSetDebugLevels attempts to parse the specified debug level and set
// the levels accordingly.  An appropriate error is returned if anything is
// invalid.
func ParseAndSetDebugLevels(debugLevel string) error {
	// When the specified string doesn't have any delimters, treat it as
	// the log level for all subsystems.
	if !strings.Contains(debugLevel, ",") && !strings.Contains(debugLevel, "=") {
		// Validate debug log level.
		lvl, err := log.LvlFromString(debugLevel)
		if err != nil {
			str := "the specified debug level [%v] is invalid"
			return fmt.Errorf(str, debugLevel)
		}
		// Change the logging level for all subsystems.
		log.Glogger().Verbosity(lvl)
		return nil
	}
	// TODO support log for subsystem
	return nil
}

// normalizeAddress returns addr with the passed default port appended if
// there is not already a port specified.
func normalizeAddress(addr, defaultPort string) string {
	_, _, err := net.SplitHostPort(addr)
	if err != nil {
		return net.JoinHostPort(addr, defaultPort)
	}
	return addr
}

// normalizeAddresses returns a new slice with all the passed peer addresses
// normalized with the given default port, and all duplicates removed.
func normalizeAddresses(addrs []string, defaultPort string) []string {
	for i, addr := range addrs {
		addrs[i] = normalizeAddress(addr, defaultPort)
	}

	return removeDuplicateAddresses(addrs)
}

// removeDuplicateAddresses returns a new slice with all duplicate entries in
// addrs removed.
func removeDuplicateAddresses(addrs []string) []string {
	result := make([]string, 0, len(addrs))
	seen := map[string]struct{}{}
	for _, val := range addrs {
		if _, ok := seen[val]; !ok {
			result = append(result, val)
			seen[val] = struct{}{}
		}
	}
	return result
}

func SetupConfig(cfg *config.Config) error {
	// BDAG_NETWORK env (e.g. set by k8s manifest) overrides flags so devnet works even with older images
	if n := strings.TrimSpace(os.Getenv("BDAG_NETWORK")); n != "" {
		switch strings.ToLower(n) {
		case "mainnet":
			params.ActiveNetParams = &params.MainNetParam
			log.Info("Active network: mainnet (from BDAG_NETWORK env)")
		case "mainnet-isolated":
			params.ActiveNetParams = &params.MainNetIsolatedParam
			log.Info("Active network: mainnet-isolated (from BDAG_NETWORK env)")
		case "devnet":
			params.ActiveNetParams = &params.DevNetParam
			log.Info("Active network: devnet (from BDAG_NETWORK env); EVM chain ID 8008")
		case "testnet":
			params.ActiveNetParams = &params.TestNetParam
			log.Info("Active network: testnet (from BDAG_NETWORK env); EVM chain ID 1043")
		case "privnet":
			params.ActiveNetParams = &params.PrivNetParam
			log.Info("Active network: privnet (from BDAG_NETWORK env)")
		case "mixnet":
			params.ActiveNetParams = &params.MixNetParam
			log.Info("Active network: mixnet (from BDAG_NETWORK env)")
		default:
			return fmt.Errorf("SetupConfig: invalid BDAG_NETWORK %q (use mainnet, mainnet-isolated, devnet, testnet, privnet, mixnet)", n)
		}
		// fall through to port/config setup below
	} else {
		// assign active network params from flags
		numNets := 0
		if cfg.TestNet {
			numNets++
			params.ActiveNetParams = &params.TestNetParam
		}
		if cfg.PrivNet {
			numNets++
			// Also disable dns seeding on the private test network.
			params.ActiveNetParams = &params.PrivNetParam
		}
		if cfg.MixNet {
			numNets++
			params.ActiveNetParams = &params.MixNetParam
		}
		if cfg.DevNet {
			numNets++
			params.ActiveNetParams = &params.DevNetParam
		}

		// Multiple networks can't be selected simultaneously.
		if numNets > 1 {
			return fmt.Errorf("SetupConfig: the testnet and simnet params can't be used together -- choose one of the three")
		}
		if params.ActiveNetParams == &params.DevNetParam {
			log.Info("Active network: devnet (from --devnet flag); EVM chain ID 8008")
		} else if params.ActiveNetParams == &params.TestNetParam {
			log.Info("Active network: testnet (from --testnet flag); EVM chain ID 1043")
		} else {
			log.Info("Active network: " + params.ActiveNetParams.Name + " (from flags)")
		}
	}

	// configure activation delay for consensus upgrades
	if cfg.UpgradeActivationDelayBlocks > 0 {
		upgrades.SetDefaultActivationDelayBlocks(cfg.UpgradeActivationDelayBlocks)
	} else {
		upgrades.SetDefaultActivationDelayBlocks(params.ActiveNetParams.Params.UpgradeActivationDelayBlocks)
	}
	if err := registerTestnetRewardGovernanceUpgrade(); err != nil {
		return err
	}
	if err := registerTestnetTimingUpgrade(); err != nil {
		return err
	}
	if err := registerSeparateStakingUpgrade(); err != nil {
		return err
	}
	if params.ActiveNetParams == &params.MainNetParam {
		if err := evmconsensus.ValidateCompiledMainnetDAOArtifacts(); err != nil {
			return err
		}
	}
	if err := registerMainnetDAOUpgrade(); err != nil {
		return err
	}

	// default p2p port
	if len(cfg.DefaultPort) > 0 {
		params.ActiveNetParams.Params.DefaultPort = cfg.DefaultPort
	}

	if cfg.P2PTCPPort <= 0 {
		P2PTCPPort, err := strconv.Atoi(params.ActiveNetParams.DefaultPort)
		if err != nil {
			return err
		}
		cfg.P2PTCPPort = P2PTCPPort
	}
	//
	if err := params.ActiveNetParams.PowConfig.Check(); err != nil {
		return err
	}

	// Add default port to all rpc listener addresses if needed and remove
	// duplicate addresses.
	cfg.RPCListeners = normalizeAddresses(cfg.RPCListeners, params.ActiveNetParams.RpcPort)
	// Only allow TLS to be disabled if the RPC is bound to localhost
	// addresses.
	if !cfg.DisableRPC && cfg.DisableTLS {
		allowedTLSListeners := map[string]struct{}{
			"localhost": {},
			"127.0.0.1": {},
			"0.0.0.0":   {},
			"::1":       {},
		}
		for _, addr := range cfg.RPCListeners {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				str := "SetupConfig: RPC listen interface '%s' is invalid: %v"
				return fmt.Errorf(str, addr, err)
			}
			if _, ok := allowedTLSListeners[host]; !ok {
				str := "SetupConfig: the --notls option may not be used " +
					"when binding RPC to non localhost " +
					"addresses: %s"
				return fmt.Errorf(str, addr)
			}
		}
	}
	// Default RPC to listen on localhost only.
	if !cfg.DisableRPC && len(cfg.RPCListeners) == 0 {
		addrs, err := net.LookupHost("localhost")
		if err != nil {
			return err
		}
		cfg.RPCListeners = make([]string, 0, len(addrs))
		for _, addr := range addrs {
			addr = net.JoinHostPort(addr, params.ActiveNetParams.RpcPort)
			cfg.RPCListeners = append(cfg.RPCListeners, addr)
		}
	}
	if cfg.RPCMaxConcurrentReqs < 0 {
		str := "SetupConfig: The rpcmaxwebsocketconcurrentrequests option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.RPCMaxConcurrentReqs)
	}

	if cfg.RPCMaxConcurrent < 0 {
		str := "SetupConfig: The rpcmaxconcurrent option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.RPCMaxConcurrent)
	}
	if cfg.RPCCriticalConcurrent < 0 {
		str := "SetupConfig: The rpccriticalconcurrent option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.RPCCriticalConcurrent)
	}
	if cfg.RPCMaxClients < 0 {
		str := "SetupConfig: The rpcmaxclients option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.RPCMaxClients)
	}
	if cfg.RPCMaxClientsPerHost < 0 {
		str := "SetupConfig: The rpcmaxclientsperhost option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.RPCMaxClientsPerHost)
	}
	if cfg.RPCRequestTimeout < 0 {
		str := "SetupConfig: The rpcrequesttimeout option may not be less than 0 -- parsed [%s]"
		return fmt.Errorf(str, cfg.RPCRequestTimeout)
	}
	if cfg.GatewayMaxConcurrent < 0 {
		str := "SetupConfig: The http.maxconcurrent option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.GatewayMaxConcurrent)
	}
	if cfg.P2PRPCConcurrency < 0 {
		str := "SetupConfig: The p2prpcconcurrency option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.P2PRPCConcurrency)
	}
	if cfg.GatewayCriticalReserve < 0 {
		str := "SetupConfig: The http.criticalreserve option may not be less than 0 -- parsed [%d]"
		return fmt.Errorf(str, cfg.GatewayCriticalReserve)
	}
	if cfg.GatewayRequestTimeout < 0 {
		str := "SetupConfig: The requesttimeout option may not be less than 0 -- parsed [%s]"
		return fmt.Errorf(str, cfg.GatewayRequestTimeout)
	}

	// Append the network type to the data directory so it is "namespaced"
	// per network.  In addition to the block database, there are other
	// pieces of data that are saved to disk such as address manager state.
	// All data is specific to a network, so namespacing the data directory
	// means each individual piece of serialized data does not have to
	// worry about changing names per network and such.
	if len(cfg.DataDir) > 0 {
		cfg.DataDir = util.CleanAndExpandPath(cfg.DataDir)
		cfg.DataDir = filepath.Join(cfg.DataDir, params.ActiveNetParams.Name)
	}
	// Set logging file if presented
	if !cfg.NoFileLogging {
		// Append the network type to the log directory so it is "namespaced"
		// per network in the same fashion as the data directory.
		cfg.LogDir = util.CleanAndExpandPath(cfg.LogDir)
		cfg.LogDir = filepath.Join(cfg.LogDir, params.ActiveNetParams.Name)

		// Initialize log rotation.  After log rotation has been initialized, the
		// logger variables may be used.
		log.InitLogRotator(filepath.Join(cfg.LogDir, defaultLogFilename), cfg.LogRotatorSize)
	}
	// Parse, validate, and set debug log level(s).
	if err := ParseAndSetDebugLevels(cfg.DebugLevel); err != nil {
		return err
	}
	// DebugPrintOrigins
	if cfg.DebugPrintOrigins {
		log.PrintOrigins(true)
	}
	// --addrindex and --dropaddrindex do not mix.
	if cfg.AddrIndex && cfg.DropAddrIndex {
		return fmt.Errorf("SetupConfig: the --addrindex and --dropaddrindex options may not be activated at the same time")
	}
	// Check mining addresses are valid and saved parsed versions.
	for _, strAddr := range cfg.MiningAddrs {
		if strAddr == "" {
			continue
		}
		if !ethcommon.IsHexAddress(strAddr) {
			return fmt.Errorf("SetupConfig: mining address '%s' is not a valid Ethereum address", strAddr)
		}
		addr := ethcommon.HexToAddress(strAddr)

		// TODO, check network by using IsForNetwork()

		// if !address.IsForNetwork(addr, params.ActiveNetParams.Params) {
		// 	str := "SetupConfig: mining address '%s' is on the wrong network"
		// 	return fmt.Errorf(str, strAddr)
		// }
		cfg.SetMiningAddrs(addr)
	}
	// Ensure there is at least one mining address when miner flag is set.
	if len(cfg.MiningAddrs) == 0 && cfg.Miner && !cfg.Cleanup {
		return fmt.Errorf("SetupConfig: the miner flag is set, but there are no mining addresses specified")
	}
	if cfg.NTP {
		roughtime.Init(context.Background())
	}
	if cfg.AutoCollectEvm {
		s3, _ := gp.GetPasswdPrompt("please input your pass unlock your wallet:", true, os.Stdin, os.Stdout)
		cfg.WalletPass = string(s3)
	}
	cfg.UpdateArchiveModeFromEVMEnv()
	config.Cfg = cfg
	return nil
}
