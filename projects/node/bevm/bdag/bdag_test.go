package bdag

import (
	"bytes"
	"strings"
	"testing"

	bevmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	nodeconfig "github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
)

func TestGenesisHash(t *testing.T) {
	assert.Equal(t, MainNetGenesisHash, Genesis(params.MainNetParams.Net, nil).ToBlock().Hash().String(),
		params.MainNetParams.Name+" genesis hash not equal latest")
	assert.Equal(t, MixNetGenesisHash, Genesis(params.MixNetParams.Net, nil).ToBlock().Hash().String(),
		params.MixNetParams.Name+" genesis hash not equal latest")
	assert.Equal(t, TestNetGenesisHash, Genesis(params.TestNetParams.Net, nil).ToBlock().Hash().String(),
		params.TestNetParams.Name+" genesis hash not equal latest")
	assert.Equal(t, PrivNetGenesisHash, Genesis(params.PrivNetParams.Net, nil).ToBlock().Hash().String(),
		params.PrivNetParam.Name+" genesis hash not equal latest")
	assert.Equal(t, DevNetGenesisHash, Genesis(params.DevNetParams.Net, nil).ToBlock().Hash().String(),
		params.DevNetParam.Name+" genesis hash not equal latest")
}

func TestDevnetGenesisIncludesStakingContractAtConsensusAddress(t *testing.T) {
	alloc := DecodeAlloc(params.DevNetParam.Params)

	proxy, ok := alloc[bevmparams.StakingContractAddr]
	assert.True(t, ok, "devnet genesis must predeploy the staking proxy at the consensus staking address")
	if !ok {
		return
	}
	impl, ok := alloc[bevmparams.StakingImplAddr]
	assert.True(t, ok, "devnet genesis must predeploy the staking implementation")
	if !ok {
		return
	}

	// Mirror mainnet topology: a small ERC-1967 proxy at the consensus address whose
	// implementation slot points at the real StakingContract runtime.
	assert.NotEmpty(t, proxy.Code, "proxy bytecode must be present")
	assert.Less(t, len(proxy.Code), 512, "proxy code must be the thin ERC-1967 proxy, not the implementation")
	assert.NotEmpty(t, impl.Code, "implementation bytecode must be present")
	assert.False(t, looksLikeCreationCode(impl.Code), "implementation must be runtime code, not creation code")
	assert.Equal(t, bevmparams.StakingImplAddr, common.BytesToAddress(proxy.Storage[bevmparams.SlotERC1967Implementation].Bytes()[12:]))
	// UUPSUpgradeable bakes address(this) into the implementation as an immutable;
	// upgradeToAndCall through the proxy reverts unless it equals the impl address.
	assert.True(t, bytes.Contains(impl.Code, bevmparams.StakingImplAddr.Bytes()), "implementation runtime must embed its own address (UUPS __self)")

	legacyDevAddr := common.HexToAddress("0x1AAE170038D3541Da7Ef0FB4a3e38D0b2A36EAb1")
	legacyDevAccount, legacyDevExists := alloc[legacyDevAddr]
	assert.False(t, legacyDevExists && len(legacyDevAccount.Code) > 0, "devnet must not deploy staking to the old ad-hoc address")

	// State lives at the proxy.
	assert.Equal(t, bevmparams.DevnetStakingOwner, common.BytesToAddress(proxy.Storage[common.Hash{}].Bytes()[12:]))
	assert.Equal(t, uint64(70), proxy.Storage[bevmparams.SlotMinerSplit].Big().Uint64())
	assert.Equal(t, uint64(30), proxy.Storage[bevmparams.SlotStakerSplit].Big().Uint64())
	assert.Equal(t, uint64(params.DevNetParams.GenesisBlock.Block().Header.Timestamp.Unix()), proxy.Storage[bevmparams.SlotGenesisTime].Big().Uint64())

	managerSlot := storageMappingSlot(bevmparams.DevnetStakingOwner, bevmparams.SlotIsManagerBase)
	assert.Equal(t, uint64(1), proxy.Storage[managerSlot].Big().Uint64())
	assert.Equal(t, uint64(1), proxy.Storage[bevmparams.SlotInitializableStorage].Big().Uint64())
	assert.Equal(t, uint64(1), proxy.Storage[bevmparams.SlotReentrancyGuardState].Big().Uint64())

	// Funded operator accounts for the devnet.
	owner, ok := alloc[bevmparams.DevnetStakingOwner]
	assert.True(t, ok && owner.Balance.Sign() > 0, "devnet owner must be funded in genesis")
}

func looksLikeCreationCode(code []byte) bool {
	idx := bytes.Index(code, []byte{0xf3, 0xfe})
	return idx >= 0 && idx+2 < len(code)
}

func TestDevnetStakingEpochDurationIsOneMinute(t *testing.T) {
	assert.Equal(t, uint64(60), params.DevNetParams.StakingEpochSeconds())
	assert.Equal(t, int64(604800), params.DevNetParams.SubsidyReductionInterval)
}

func TestMakeParamsRejectsEVMP2PFlags(t *testing.T) {
	for _, evmEnv := range []string{
		`"--maxpeers 50"`,
		`"--maxpeers=50"`,
		`"--port 30303"`,
		`"--port=30303"`,
	} {
		_, _, err := MakeParams(&nodeconfig.Config{EVMEnv: evmEnv})
		if err == nil {
			t.Fatalf("MakeParams(%q) returned nil error, want rejection", evmEnv)
		}
		if !strings.Contains(err.Error(), "currently does not support") {
			t.Fatalf("MakeParams(%q) error = %q, want unsupported flag rejection", evmEnv, err.Error())
		}
	}
}

func TestMakeParamsAllowsEVMMetricsPortFlag(t *testing.T) {
	_, args, err := MakeParams(&nodeconfig.Config{EVMEnv: `"--metrics --metrics.port 6060 --cache 2048"`})
	if err != nil {
		t.Fatalf("MakeParams() error = %v, want nil", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--metrics.port 6060") {
		t.Fatalf("MakeParams() args = %q, want metrics port preserved", got)
	}
}

func storageMappingSlot(addr common.Address, baseSlot common.Hash) common.Hash {
	b := make([]byte, 64)
	copy(b[12:32], addr.Bytes())
	copy(b[32:], baseSlot.Bytes())
	return crypto.Keccak256Hash(b)
}
