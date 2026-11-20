package simulator

// import (
// 	"fmt"
// 	"os"
// 	"path"
// 	"runtime"

// 	_ "github.com/BlockdagNetworkLabs/bdag/bevm/common"
// 	"github.com/BlockdagNetworkLabs/bdag/common/system"
// 	"github.com/BlockdagNetworkLabs/bdag/config"
// 	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
// 	"github.com/BlockdagNetworkLabs/bdag/log"
// 	"github.com/BlockdagNetworkLabs/bdag/node"
// 	"github.com/BlockdagNetworkLabs/bdag/params"
// 	"github.com/Qitmeer/qitmeer/services/acct"

// 	// "github.com/BlockdagNetworkLabs/bdag/services/acct"
// 	// "github.com/BlockdagNetworkLabs/bdag/services/address"
// 	"github.com/BlockdagNetworkLabs/bdag/services/common"
// 	"github.com/BlockdagNetworkLabs/bdag/services/miner"
// 	"github.com/BlockdagNetworkLabs/bdag/services/tx"

// 	// "github.com/BlockdagNetworkLabs/bdag/services/wallet"
// 	"github.com/BlockdagNetworkLabs/bdag/testutils/simulator/testprivatekey"
// 	"github.com/BlockdagNetworkLabs/bdag/version"
// )

// func DefaultConfig() *config.Config {
// 	cfg := common.DefaultConfig(path.Join(os.TempDir(), "_test"))
// 	cfg.DataDir = ""
// 	cfg.NoFileLogging = true
// 	cfg.PrivNet = true
// 	cfg.DisableRPC = true
// 	cfg.DisableListen = true
// 	cfg.Miner = true
// 	cfg.AcctMode = true
// 	return cfg
// }

// var mockNodeGlobalID uint

// type MockNode struct {
// 	id          uint
// 	n           *node.Node
// 	pb          *testprivatekey.Builder
// 	overrideCfg func(cfg *config.Config) error
// 	//
// 	publicMinerAPI          *miner.PublicMinerAPI
// 	privateMinerAPI         *miner.PrivateMinerAPI
// 	publicBlockAPI          *blockchain.PublicBlockAPI
// 	publicBlockChainAPI     *node.PublicBlockChainAPI
// 	publicTxAPI             *tx.PublicTxAPI
// 	privateTxAPI            *tx.PrivateTxAPI
// 	publicAccountManagerAPI *acct.PublicAccountManagerAPI
// 	privateWalletManagerAPI *wallet.PrivateWalletManagerAPI
// }

// func (mn *MockNode) ID() uint {
// 	return mn.id
// }

// func (mn *MockNode) Start(cfg *config.Config) error {
// 	err := common.SetupConfig(cfg)
// 	if err != nil {
// 		return err
// 	}

// 	interrupt := system.InterruptListener()

// 	// Show version and home dir at startup.
// 	log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version(), "ID", mn.id)
// 	log.Info("System info", "Home dir", cfg.HomeDir)

// 	if cfg.NoFileLogging {
// 		log.Info("File logging disabled")
// 	}

// 	// Create node and start it.
// 	n, err := node.NewNode(cfg, params.ActiveNetParams.Params, interrupt)
// 	if err != nil {
// 		log.Error("Unable to start server", "listeners", cfg.Listener, "error", err)
// 		return err
// 	}
// 	mn.n = n
// 	err = n.RegisterService()
// 	if err != nil {
// 		return err
// 	}
// 	err = n.Start()
// 	if err != nil {
// 		log.Error("Uable to start server", "error", err)
// 		n.Stop()
// 		return err
// 	}

// 	return mn.setup()
// }

// func (mn *MockNode) Stop() {
// 	if log.LogWrite() != nil {
// 		log.LogWrite().Close()
// 	}
// 	if mn.n != nil {
// 		err := mn.n.Stop()
// 		if err != nil {
// 			log.Error(err.Error())
// 		}
// 	}
// 	// remove temp dir
// 	log.Info("Try remove home dir", "path", mn.n.Config.HomeDir)
// 	err := os.RemoveAll(mn.n.Config.HomeDir)
// 	if err != nil {
// 		log.Error(err.Error())
// 	}
// }

// func (mn *MockNode) setup() error {
// 	// init
// 	coinbasePKHex := mn.pb.GetHex(testprivatekey.CoinbaseIdx)
// 	_, err := mn.GetPrivateWalletManagerAPI().ImportRawKey(coinbasePKHex, testprivatekey.Password)
// 	if err != nil {
// 		return err
// 	}
// 	accounts, err := mn.GetPrivateWalletManagerAPI().ListAccount()
// 	if err != nil {
// 		return err
// 	}
// 	log.Info(fmt.Sprintf("%v", accounts))
// 	if len(mn.n.Config.MiningAddrs) <= 0 {
// 		_, addr, _, _ := address.NewAddresses(coinbasePKHex)
// 		mn.n.Config.SetMiningAddrs(addr)
// 	}
// 	return nil
// }

// func (mn *MockNode) GetPublicMinerAPI() *miner.PublicMinerAPI {
// 	if mn.publicMinerAPI == nil {
// 		mn.publicMinerAPI = miner.NewPublicMinerAPI(mn.n.GetBlockdagFull().GetMiner())
// 	}
// 	return mn.publicMinerAPI
// }

// func (mn *MockNode) GetPrivateMinerAPI() *miner.PrivateMinerAPI {
// 	if mn.privateMinerAPI == nil {
// 		mn.privateMinerAPI = miner.NewPrivateMinerAPI(mn.n.GetBlockdagFull().GetMiner())
// 	}
// 	return mn.privateMinerAPI
// }

// func (mn *MockNode) GetPublicBlockAPI() *blockchain.PublicBlockAPI {
// 	if mn.publicBlockAPI == nil {
// 		mn.publicBlockAPI = blockchain.NewPublicBlockAPI(mn.n.GetBlockdagFull().GetBlockChain())
// 	}
// 	return mn.publicBlockAPI
// }

// func (mn *MockNode) GetPublicBlockChainAPI() *node.PublicBlockChainAPI {
// 	if mn.publicBlockChainAPI == nil {
// 		mn.publicBlockChainAPI = node.NewPublicBlockChainAPI(mn.n.GetBlockdagFull())
// 	}
// 	return mn.publicBlockChainAPI
// }

// func (mn *MockNode) GetPublicTxAPI() *tx.PublicTxAPI {
// 	if mn.publicTxAPI == nil {
// 		mn.publicTxAPI = tx.NewPublicTxAPI(mn.n.GetBlockdagFull().GetTxManager())
// 	}
// 	return mn.publicTxAPI
// }

// func (mn *MockNode) GetPrivateTxAPI() *tx.PrivateTxAPI {
// 	if mn.privateTxAPI == nil {
// 		mn.privateTxAPI = tx.NewPrivateTxAPI(mn.n.GetBlockdagFull().GetTxManager())
// 	}
// 	return mn.privateTxAPI
// }

// func (mn *MockNode) GetPublicAccountManagerAPI() *acct.PublicAccountManagerAPI {
// 	if mn.publicAccountManagerAPI == nil {
// 		mn.publicAccountManagerAPI = acct.NewPublicAccountManagerAPI(mn.n.GetBlockdagFull().GetAccountManager())
// 	}
// 	return mn.publicAccountManagerAPI
// }

// func (mn *MockNode) GetPrivateWalletManagerAPI() *wallet.PrivateWalletManagerAPI {
// 	if mn.privateWalletManagerAPI == nil {
// 		mn.privateWalletManagerAPI = wallet.NewPrivateWalletAPI(mn.n.GetBlockdagFull().GetWalletManager())
// 	}
// 	return mn.privateWalletManagerAPI
// }

// func StartMockNode(overrideCfg func(cfg *config.Config) error) (*MockNode, error) {
// 	pb, err := testprivatekey.NewBuilder(uint32(mockNodeGlobalID))
// 	if err != nil {
// 		return nil, err
// 	}
// 	mn := &MockNode{id: mockNodeGlobalID, pb: pb}
// 	cfg := DefaultConfig()
// 	if overrideCfg != nil {
// 		err := overrideCfg(cfg)
// 		if err != nil {
// 			return nil, err
// 		}
// 	}
// 	err = mn.Start(cfg)
// 	if err != nil {
// 		return nil, err
// 	}

// 	mockNodeGlobalID++
// 	return mn, nil
// }
