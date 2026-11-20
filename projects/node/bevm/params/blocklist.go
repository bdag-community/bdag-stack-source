package params

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

var (
	// BlocklistContractAddr is the address of the on-chain governance contract
	// (BlocklistGovernance.sol) that holds the wallet blocklist. Consensus
	// reads its storage directly, so the contract code is only required for
	// governance writes; reads against an undeployed address simply return
	// "not blocked".
	//
	// This is a consensus-critical constant: every node enforcing the
	// WalletBlocklist upgrade must agree on it. The contract was deployed on
	// mainnet (chain ID 1404) in tx
	// 0xf83a217f87a3ee556e1a4b44906f693f3e59c427f0f08da784289091d29cea5d.
	BlocklistContractAddr = common.HexToAddress("0xe628505d5cB6F5F4f9a25Cd5c80Caa198cc645a0")

	// BlocklistGovernance storage slots.
	// Layout (see ../Contract-Blocklist/src/BlocklistGovernance.sol):
	//   Slot 0: owner   (address)
	//   Slot 1: blocked (mapping(address => bool))  <- read by consensus
	SlotBlocklistOwner = common.BigToHash(big.NewInt(0))
	SlotBlockedBase    = common.BigToHash(big.NewInt(1))

	// SelectiveBlocklistContractAddr is the address of the SelectiveBlocklist
	// governance contract (SelectiveBlocklist.sol). Consensus reads its storage
	// directly to enforce per-sender to-address whitelisting.
	//
	// This is a consensus-critical constant for the SelectiveBlocklist upgrade.
	SelectiveBlocklistContractAddr = common.HexToAddress("0xc0a42A43A05e39C3a4165CA98440D516959f41D9")

	// NativeBlocklistEnforcementLayer is the emergency consensus patch layer for
	// EVM/native BDAG balance-movement enforcement. The original blocklist
	// upgrades may have activated before every native movement path was enforced,
	// so this future layer prevents patched nodes from retroactively rejecting
	// already-mined history.
	NativeBlocklistEnforcementLayer uint64 = 11_350_500

	// SelectiveBlocklist storage slots.
	// Layout (see ../Contract-Blocklist/src/SelectiveBlocklist.sol):
	//   Slot 0: owner     (address)
	//   Slot 1: restricted (mapping(address => bool))              <- read by consensus
	//   Slot 2: allowedTo  (mapping(address => mapping(address => bool))) <- read by consensus
	SlotRestrictedBase = common.BigToHash(big.NewInt(1))
	SlotAllowedToBase  = common.BigToHash(big.NewInt(2))
)
