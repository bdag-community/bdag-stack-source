package consensus

import (
	_ "embed"
	"math/big"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	ethparams "github.com/ethereum/go-ethereum/params"
)

// stakingV2CreationHex is the creation bytecode of StakingContractV2
// (Contract-Staking contracts/staking/StakingPoolV2.sol; solc 0.8.24, evmVersion
// london, optimizer 200 runs). It is executed at StakingV2ImplAddr at the fork
// block so the UUPS __self immutable binds to that address. Regenerate with
// `npm run export:artifacts` in Contract-Staking and copy
// out/devnet/StakingContractV2.creation.hex here. Storage slots 0-17 of the
// staking proxy are a consensus ABI; Contract-Staking test/StorageLayout.test.js
// gates every build that lands here.
//
//go:embed bytecode/StakingContractV2.creation.hex
var stakingV2CreationHex string

var stakingV2CreationCode = mustDecodeHex(stakingV2CreationHex)

// initializableDisabled mirrors OZ Initializable._disableInitializers() on the bare
// implementation (_initialized = type(uint64).max, low 8 bytes of the slot). The
// constructor's own write is discarded by the sandboxed derivation, hence explicit.
var initializableDisabled = common.HexToHash("0x000000000000000000000000000000000000000000000000ffffffffffffffff")

// stakingV2Upgrade installs StakingContractV2 at StakingV2ImplAddr and points the
// staking proxy's ERC-1967 implementation slot at it: exactly the state an owner
// upgradeToAndCall would produce, minus the Upgraded event and the reinitializer
// (the owner runs initializeV2() afterwards). The V1 implementation is left in
// place for forensics. Active only where BdagStakingV2Block is set (devnet).
var stakingV2Upgrade = SystemContractUpgrade{
	Name:         "staking-v2",
	ForkBlock:    func(c *ethparams.ChainConfig) *big.Int { return c.BdagStakingV2Block },
	Address:      mparams.StakingV2ImplAddr,
	CreationCode: stakingV2CreationCode,
	Storage: []StorageWrite{
		{Account: mparams.StakingContractAddr, Slot: mparams.SlotERC1967Implementation, Value: common.BytesToHash(mparams.StakingV2ImplAddr.Bytes())},
		{Account: mparams.StakingV2ImplAddr, Slot: mparams.SlotInitializableStorage, Value: initializableDisabled},
	},
}
