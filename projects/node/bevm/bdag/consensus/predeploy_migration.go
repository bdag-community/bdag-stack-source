package consensus

import (
	"bytes"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	ethconsensus "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

const predeployMigrationGas uint64 = 1_000_000_000

var (
	stakingProxyAddr    = common.HexToAddress("0x1000000000000000000000000000000000000001")
	governanceProxyAddr = common.HexToAddress("0x1000000000000000000000000000000000000005")
	stakingImplAddr     = common.HexToAddress("0x1000000000000000000000000000000000000011")
	governanceImplAddr  = common.HexToAddress("0x1000000000000000000000000000000000000015")
	defaultGenesisAdmin = common.HexToAddress("0x3655a92f2Db839a9eF30216c8843B310C65ce298")

	erc1967ImplementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	erc1967AdminSlot          = common.HexToHash("0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103")
	governanceAdminSlot       = common.Hash{}
	accessControlStorageSlot  = common.HexToHash("0x02dd7bc7dec4dceedda775e58dd541e08a116c6c53815c0bd028192f7b626800")
	governanceAdminRole       = common.HexToHash("0xa49807205ce4d355092ef5a8a18f56e8913cf4a201fbe287825b095693c21775")

	predeployMigrationAccounts = []common.Address{
		stakingProxyAddr,
		governanceProxyAddr,
		stakingImplAddr,
		governanceImplAddr,
	}
)

// ApplyPreBlockStateChanges applies the engine's pre-block state changes when the
// engine implements consensus.PreBlockStateChanger. The geth fork's block importer
// (core/state_processor.go) calls the same hook, so builders and importers agree.
func ApplyPreBlockStateChanges(engine ethconsensus.Engine, chain ethconsensus.ChainHeaderReader, header *types.Header, statedb *state.StateDB) {
	if hook, ok := engine.(ethconsensus.PreBlockStateChanger); ok {
		hook.ApplyPreBlockStateChange(chain, header, statedb)
	}
}

// ApplyPreBlockStateChange implements consensus.PreBlockStateChanger: the mainnet
// predeploy runtime migration, then any table-driven system-contract upgrades.
func (me *bdagEngine) ApplyPreBlockStateChange(chain ethconsensus.ChainHeaderReader, header *types.Header, statedb *state.StateDB) {
	if chain == nil {
		return
	}
	ApplyPredeployMigration(chain.Config(), header, statedb)
	ApplySystemContractUpgrades(chain.Config(), header, statedb)
	if err := ApplyMainnetDAOFork(chain, header, statedb); err != nil {
		me.log.Crit("Mainnet DAO migration validation failed", "err", err)
	}
	if err := ApplyGovernanceFork(chain.Config(), header, statedb); err != nil {
		me.log.Crit("governance fork validation failed", "err", err)
	}
	if err := ApplyTestnetTimingFork(chain.Config(), header, statedb); err != nil {
		me.log.Crit("testnet timing fork validation failed", "err", err)
	}
	// Resolve the epoch boundary from parent state, before advancing its clock.
	if err := ApplySeparateStakingFork(chain.Config(), header, statedb); err != nil {
		me.log.Crit("separate staking fork validation failed", "err", err)
	}
	if err := prepareVersionedRewardClock(chain.Config(), header, statedb); err != nil {
		me.log.Crit("versioned reward clock validation failed", "err", err)
	}
}

func ApplyPredeployMigration(config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB) {
	if !isPredeployMigrationBlock(config, header) || statedb == nil || !predeployMigrationNeeded(statedb) {
		return
	}

	migratePredeployCode(config, header, statedb, stakingProxyAddr, true)
	migratePredeployCode(config, header, statedb, governanceProxyAddr, true)
	migratePredeployCode(config, header, statedb, stakingImplAddr, false)
	migratePredeployCode(config, header, statedb, governanceImplAddr, false)

	setProxySlots(statedb, stakingProxyAddr, stakingImplAddr)
	setProxySlots(statedb, governanceProxyAddr, governanceImplAddr)
	bootstrapGovernanceAdmin(statedb)
}

func isPredeployMigrationBlock(config *ethparams.ChainConfig, header *types.Header) bool {
	return config != nil &&
		header != nil &&
		header.Number != nil &&
		config.BdagPredeployMigrationBlock != nil &&
		header.Number.Cmp(config.BdagPredeployMigrationBlock) == 0
}

func predeployMigrationNeeded(statedb *state.StateDB) bool {
	for _, addr := range predeployMigrationAccounts {
		if looksLikeCreationCode(statedb.GetCode(addr)) {
			return true
		}
	}
	return false
}

func migratePredeployCode(config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, addr common.Address, allowRuntimeSuffixFallback bool) {
	initCode := statedb.GetCode(addr)
	if len(initCode) == 0 || !looksLikeCreationCode(initCode) {
		return
	}

	runtime, err := runtimeFromCreationCode(config, header, statedb, addr, initCode)
	if err != nil || len(runtime) == 0 || bytes.Equal(runtime, initCode) {
		if !allowRuntimeSuffixFallback {
			return
		}
		runtime = runtimeSuffixFromSolidityCreation(initCode)
	}
	if len(runtime) == 0 || bytes.Equal(runtime, initCode) {
		return
	}
	statedb.SetCode(addr, runtime)
}

func looksLikeCreationCode(code []byte) bool {
	return len(runtimeSuffixFromSolidityCreation(code)) > 0
}

func runtimeSuffixFromSolidityCreation(code []byte) []byte {
	idx := bytes.Index(code, []byte{0xf3, 0xfe})
	if idx < 0 || idx+2 >= len(code) {
		return nil
	}
	runtime := make([]byte, len(code)-idx-2)
	copy(runtime, code[idx+2:])
	return runtime
}

func runtimeFromCreationCode(config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, addr common.Address, initCode []byte) ([]byte, error) {
	snapshot := statedb.Snapshot()
	statedb.SetCode(addr, nil)
	defer statedb.RevertToSnapshot(snapshot)

	blockNumber := big.NewInt(0)
	blockTime := uint64(0)
	difficulty := big.NewInt(0)
	baseFee := big.NewInt(0)
	gasLimit := predeployMigrationGas
	if header != nil {
		if header.Number != nil {
			blockNumber = new(big.Int).Set(header.Number)
		}
		blockTime = header.Time
		if header.Difficulty != nil {
			difficulty = new(big.Int).Set(header.Difficulty)
		}
		if header.BaseFee != nil {
			baseFee = new(big.Int).Set(header.BaseFee)
		}
		if header.GasLimit != 0 {
			gasLimit = header.GasLimit
		}
	}

	blockContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		BlockNumber: blockNumber,
		Time:        blockTime,
		Difficulty:  difficulty,
		BaseFee:     baseFee,
		GasLimit:    gasLimit,
	}
	evm := vm.NewEVM(blockContext, vm.TxContext{}, statedb, config, vm.Config{NoBaseFee: true})
	contract := vm.NewContract(vm.AccountRef(common.Address{}), vm.AccountRef(addr), uint256.NewInt(0), predeployMigrationGas)
	contract.IsDeployment = true
	contract.SetCallCode(&addr, common.Hash{}, initCode)

	runtime, err := evm.Interpreter().Run(contract, nil, false)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(runtime))
	copy(out, runtime)
	return out, nil
}

func setProxySlots(statedb *state.StateDB, proxy common.Address, implementation common.Address) {
	statedb.SetState(proxy, erc1967ImplementationSlot, addressToSlotValue(implementation))
	if statedb.GetState(proxy, erc1967AdminSlot) == (common.Hash{}) {
		statedb.SetState(proxy, erc1967AdminSlot, addressToSlotValue(defaultGenesisAdmin))
	}
}

func bootstrapGovernanceAdmin(statedb *state.StateDB) {
	admin := statedb.GetState(governanceProxyAddr, governanceAdminSlot)
	if admin == (common.Hash{}) {
		admin = statedb.GetState(governanceProxyAddr, erc1967AdminSlot)
		if admin == (common.Hash{}) {
			admin = addressToSlotValue(defaultGenesisAdmin)
			statedb.SetState(governanceProxyAddr, erc1967AdminSlot, admin)
		}
		statedb.SetState(governanceProxyAddr, governanceAdminSlot, admin)
	}
	adminAddr := common.BytesToAddress(admin.Bytes())
	if adminAddr != (common.Address{}) {
		grantGovernanceRole(statedb, common.Hash{}, adminAddr)
		grantGovernanceRole(statedb, governanceAdminRole, adminAddr)
	}
}

func addressToSlotValue(addr common.Address) common.Hash {
	return common.BytesToHash(addr.Bytes())
}

func grantGovernanceRole(statedb *state.StateDB, role common.Hash, account common.Address) {
	roleSlot := crypto.Keccak256Hash(role.Bytes(), accessControlStorageSlot.Bytes())
	accountSlot := crypto.Keccak256Hash(common.LeftPadBytes(account.Bytes(), common.HashLength), roleSlot.Bytes())
	statedb.SetState(governanceProxyAddr, accountSlot, common.Hash{31: 1})
}
