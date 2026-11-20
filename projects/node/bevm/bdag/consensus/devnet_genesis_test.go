package consensus_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func devnetGenesisState(t *testing.T) *state.StateDB {
	t.Helper()
	return bdag.MakePreState(rawdb.NewMemoryDatabase(), bdag.DecodeAlloc(qparams.DevNetParam.Params))
}

func devnetChainConfig(stakingV2Block *uint64) *ethparams.ChainConfig {
	config := *mparams.BdagDevnetChainConfig
	config.BdagStakingV2Block = nil
	if stakingV2Block != nil {
		config.BdagStakingV2Block = new(big.Int).SetUint64(*stakingV2Block)
	}
	return &config
}

func devnetHeader(number uint64) *types.Header {
	return &types.Header{
		Number:     new(big.Int).SetUint64(number),
		Time:       1758551400 + number,
		Difficulty: big.NewInt(1),
		GasLimit:   100_000_000,
		BaseFee:    big.NewInt(0),
	}
}

func wordToUint64(t *testing.T, ret []byte) uint64 {
	t.Helper()
	require.Len(t, ret, common.HashLength)
	return new(big.Int).SetBytes(ret).Uint64()
}

// The devnet genesis must mirror mainnet: an ERC-1967 proxy at the consensus
// staking address with a UUPS implementation behind it, so that the owner can
// run upgradeToAndCall from block 1 (switchover path A). Before this shape was
// introduced the implementation runtime sat directly at the proxy address and
// every UUPS call reverted with UUPSUnauthorizedCallContext.
func TestDevnetGenesisProxyIsUUPSLive(t *testing.T) {
	statedb := devnetGenesisState(t)
	config := devnetChainConfig(nil)
	header := devnetHeader(1)
	proxy := mparams.StakingContractAddr
	impl := mparams.StakingImplAddr

	require.Equal(t, addressSlotValue(impl), statedb.GetState(proxy, mparams.SlotERC1967Implementation))
	require.NotEmpty(t, statedb.GetCode(impl))

	owner := common.BytesToAddress(staticCall(t, config, header, statedb, proxy, selector("owner()")))
	require.Equal(t, mparams.DevnetStakingOwner, owner)
	require.Equal(t, uint64(70), wordToUint64(t, staticCall(t, config, header, statedb, proxy, selector("minerSplit()"))))
	require.Equal(t, uint64(1758551400), wordToUint64(t, staticCall(t, config, header, statedb, proxy, selector("genesisTime()"))))

	// proxiableUUID: via the implementation returns the slot; via the proxy must revert (notDelegated).
	uuid := staticCall(t, config, header, statedb, impl, selector("proxiableUUID()"))
	require.Equal(t, mparams.SlotERC1967Implementation, common.BytesToHash(uuid))

	// Owner-driven UUPS upgrade to a second implementation must succeed and flip the
	// slot. A valid IERC1822 target must carry its own address as the UUPS __self
	// immutable (proxiableUUID is notDelegated), so re-home the V1 runtime by
	// rewriting the embedded address bytes. Real deployments get this for free by
	// executing the creation code at the target address.
	newImpl := common.HexToAddress("0x1000000000000000000000000000000000000099")
	rehomed := bytes.ReplaceAll(statedb.GetCode(impl), impl.Bytes(), newImpl.Bytes())
	require.NotEqual(t, statedb.GetCode(impl), rehomed, "V1 runtime should embed the impl address")
	statedb.SetCode(newImpl, rehomed)
	require.Equal(t, mparams.SlotERC1967Implementation, common.BytesToHash(staticCall(t, config, header, statedb, newImpl, selector("proxiableUUID()"))))
	snapshot := statedb.Snapshot()
	upgradeAndAssert(t, config, header, statedb, owner, proxy, newImpl)
	statedb.RevertToSnapshot(snapshot)
	require.Equal(t, addressSlotValue(impl), statedb.GetState(proxy, mparams.SlotERC1967Implementation))
}

// upgradeAndAssert performs upgradeToAndCall(newImpl, "") from `from` and checks the
// implementation slot moved and the proxy still answers.
func upgradeAndAssert(t *testing.T, config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB, from, proxy, newImpl common.Address) {
	t.Helper()
	callContractKeep(t, config, header, statedb, from, proxy, upgradeToAndCallInput(newImpl))
	require.Equal(t, addressSlotValue(newImpl), statedb.GetState(proxy, mparams.SlotERC1967Implementation))
	require.Equal(t, uint64(70), wordToUint64(t, staticCall(t, config, header, statedb, proxy, selector("minerSplit()"))))
}
