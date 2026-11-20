package consensus_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

var (
	testStakingProxyAddr    = common.HexToAddress("0x1000000000000000000000000000000000000001")
	testGovernanceProxyAddr = common.HexToAddress("0x1000000000000000000000000000000000000005")
	testStakingImplAddr     = common.HexToAddress("0x1000000000000000000000000000000000000011")
	testGovernanceImplAddr  = common.HexToAddress("0x1000000000000000000000000000000000000015")
	testDefaultGenesisAdmin = common.HexToAddress("0x3655a92f2Db839a9eF30216c8843B310C65ce298")

	testERC1967ImplementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	testERC1967AdminSlot          = common.HexToHash("0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103")
	testGovernanceAdminSlot       = common.Hash{}
)

func TestPredeployMigrationIgnoresNonActivationBlocks(t *testing.T) {
	statedb := mainnetGenesisState(t)
	config := testChainConfig(7)
	header := testHeader(6)

	before := append([]byte(nil), statedb.GetCode(testGovernanceImplAddr)...)
	bdagconsensus.ApplyPredeployMigration(config, header, statedb)

	require.Equal(t, before, statedb.GetCode(testGovernanceImplAddr))
	require.True(t, testLooksLikeCreationCode(statedb.GetCode(testGovernanceImplAddr)))
}

func TestPredeployMigrationRepairsMainnetGenesisPredeploys(t *testing.T) {
	statedb := mainnetGenesisState(t)
	config := testChainConfig(7)
	header := testHeader(7)

	predeploys := []common.Address{
		testStakingProxyAddr,
		testGovernanceProxyAddr,
		testStakingImplAddr,
		testGovernanceImplAddr,
	}
	before := make(map[common.Address][]byte, len(predeploys))
	for _, addr := range predeploys {
		before[addr] = append([]byte(nil), statedb.GetCode(addr)...)
		require.True(t, testLooksLikeCreationCode(before[addr]), addr.Hex())
	}

	bdagconsensus.ApplyPredeployMigration(config, header, statedb)

	for _, addr := range predeploys {
		after := statedb.GetCode(addr)
		require.NotEmpty(t, after, addr.Hex())
		require.False(t, testLooksLikeCreationCode(after), addr.Hex())
		require.Less(t, len(after), len(before[addr]), addr.Hex())
	}

	require.Equal(t, addressSlotValue(testStakingImplAddr), statedb.GetState(testStakingProxyAddr, testERC1967ImplementationSlot))
	require.Equal(t, addressSlotValue(testGovernanceImplAddr), statedb.GetState(testGovernanceProxyAddr, testERC1967ImplementationSlot))
	require.Equal(t, addressSlotValue(testDefaultGenesisAdmin), statedb.GetState(testGovernanceProxyAddr, testERC1967AdminSlot))
	require.Equal(t, addressSlotValue(testDefaultGenesisAdmin), statedb.GetState(testGovernanceProxyAddr, testGovernanceAdminSlot))

	uuid := staticCall(t, config, header, statedb, testGovernanceImplAddr, selector("proxiableUUID()"))
	require.Len(t, uuid, common.HashLength)
	require.Equal(t, testERC1967ImplementationSlot, common.BytesToHash(uuid))

	callContract(
		t,
		config,
		header,
		statedb,
		testDefaultGenesisAdmin,
		testGovernanceProxyAddr,
		upgradeToAndCallInput(testGovernanceImplAddr),
	)
}

func mainnetGenesisState(t *testing.T) *state.StateDB {
	t.Helper()
	return bdag.MakePreState(rawdb.NewMemoryDatabase(), bdag.DecodeAlloc(qparams.MainNetParam.Params))
}

func testChainConfig(migrationBlock uint64) *ethparams.ChainConfig {
	config := *mparams.BdagMainnetChainConfig
	config.BdagPredeployMigrationBlock = new(big.Int).SetUint64(migrationBlock)
	return &config
}

func testHeader(number uint64) *types.Header {
	return &types.Header{
		Number:     new(big.Int).SetUint64(number),
		Time:       1,
		Difficulty: big.NewInt(1),
		GasLimit:   mparams.MainnetFixedGasLimit,
		BaseFee:    big.NewInt(0),
	}
}

func staticCall(t *testing.T, config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, to common.Address, input []byte) []byte {
	t.Helper()

	return callEVM(t, config, header, statedb, common.Address{}, to, input, true)
}

func callContract(t *testing.T, config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, from common.Address, to common.Address, input []byte) []byte {
	t.Helper()

	return callEVM(t, config, header, statedb, from, to, input, false)
}

// callContractKeep is callContract without the snapshot revert: state changes persist.
func callContractKeep(t *testing.T, config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, from common.Address, to common.Address, input []byte) []byte {
	t.Helper()

	return callEVMOpt(t, config, header, statedb, from, to, input, false, true)
}

func callEVM(t *testing.T, config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, from common.Address, to common.Address, input []byte, readOnly bool) []byte {
	t.Helper()

	return callEVMOpt(t, config, header, statedb, from, to, input, readOnly, false)
}

func callEVMOpt(t *testing.T, config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, from common.Address, to common.Address, input []byte, readOnly bool, keep bool) []byte {
	t.Helper()

	blockContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		BlockNumber: new(big.Int).Set(header.Number),
		Time:        header.Time,
		Difficulty:  new(big.Int).Set(header.Difficulty),
		BaseFee:     new(big.Int).Set(header.BaseFee),
		GasLimit:    header.GasLimit,
	}
	evm := vm.NewEVM(blockContext, vm.TxContext{}, statedb, config, vm.Config{NoBaseFee: true})
	snapshot := statedb.Snapshot()
	var (
		ret []byte
		err error
	)
	if readOnly {
		ret, _, err = evm.StaticCall(vm.AccountRef(from), to, input, 10_000_000)
	} else {
		ret, _, err = evm.Call(vm.AccountRef(from), to, input, 10_000_000, uint256.NewInt(0))
	}
	if !keep || err != nil {
		statedb.RevertToSnapshot(snapshot)
	}

	require.NoErrorf(t, err, "return data: 0x%x", ret)
	return ret
}

func selector(signature string) []byte {
	return crypto.Keccak256([]byte(signature))[:4]
}

func addressSlotValue(addr common.Address) common.Hash {
	return common.BytesToHash(addr.Bytes())
}

func upgradeToAndCallInput(implementation common.Address) []byte {
	input := append([]byte{}, selector("upgradeToAndCall(address,bytes)")...)
	input = append(input, common.LeftPadBytes(implementation.Bytes(), common.HashLength)...)
	input = append(input, common.LeftPadBytes(big.NewInt(64).Bytes(), common.HashLength)...)
	input = append(input, make([]byte, common.HashLength)...)
	return input
}

func testLooksLikeCreationCode(code []byte) bool {
	idx := bytes.Index(code, []byte{0xf3, 0xfe})
	return idx >= 0 && idx+2 < len(code)
}
