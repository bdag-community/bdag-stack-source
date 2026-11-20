package params

import (
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
)

var (
	// System Addresses
	// Can be overridden via STAKING_CONTRACT_ADDR env var (e.g. after redeploying on devnet).
	StakingContractAddr = func() common.Address {
		if v := os.Getenv("STAKING_CONTRACT_ADDR"); v != "" {
			return common.HexToAddress(v)
		}
		return common.HexToAddress("0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7")
	}()
	ChainTallyAddr = common.HexToAddress("0x1000000000000000000000000000000000000002")
	SystemAddr     = common.HexToAddress("0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE")

	// Devnet staking implementation predeploys. StakingContractAddr is an ERC-1967
	// proxy in the devnet genesis; its implementation slot points at StakingImplAddr.
	// StakingV2ImplAddr receives the V2 runtime at the devnet BdagStakingV2Block fork.
	StakingImplAddr   = common.HexToAddress("0x1000000000000000000000000000000000000011")
	StakingV2ImplAddr = common.HexToAddress("0x1000000000000000000000000000000000000012")
	// Fresh, separately gated Testnet V2. The retiring proxy remains at its
	// original address; these are not Mainnet production assignments.
	SeparateStakingV2Addr     = common.HexToAddress("0x1000000000000000000000000000000000000020")
	SeparateStakingV2ImplAddr = common.HexToAddress("0x1000000000000000000000000000000000000021")

	// ERC-1967 implementation slot: keccak256("eip1967.proxy.implementation") - 1.
	SlotERC1967Implementation = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")

	// DevnetStakingOwner owns the devnet staking proxy in genesis (slot 0). The key is
	// held in DigiMount devnet-ops/devnet.env and is never committed.
	DevnetStakingOwner = common.HexToAddress("0x6742768B4D8484352051c156aAb1e4c424761c5C")

	// Chain Tally Storage Slots
	SlotCurrentEpoch     = common.BigToHash(big.NewInt(0))
	SlotTotalLiquidity   = common.BigToHash(big.NewInt(1))
	SlotCurrentTallyBase = common.BigToHash(big.NewInt(2))
	SlotLastTallyBase    = common.BigToHash(big.NewInt(3))

	// Reward-voting ledger (append-only, separate from the historical full-
	// subsidy ChainTally). The first observed epoch is deliberately ineligible;
	// voting starts only after the first completely observed reward epoch.
	SlotRewardVotingEpoch              = common.BigToHash(big.NewInt(4))
	SlotRewardVotingBootstrapEpochPlus = common.BigToHash(big.NewInt(5))
	SlotRewardVotingFirstEpochPlus     = common.BigToHash(big.NewInt(6))
	SlotRewardVotingTotalBase          = common.BigToHash(big.NewInt(7))

	// Future versioned reward clock. No active release writes this slot yet.
	// Dynamic Segment[]: keccak256(slot 8) + 3*i stores uint256 firstEpoch,
	// startTime and duration. Historical slots 0..7 remain untouched.
	SlotRewardEpochScheduleLength = common.BigToHash(big.NewInt(8))
	SlotGovernanceTimeFloor       = common.BigToHash(big.NewInt(9))

	// Staking Contract Storage Slots.
	// Derived from ../Contract-Staking/contracts/staking/StakingPool.sol (StakingContract).
	// StakingContract state variables start at Slot 0.
	// Slot 0: owner
	// Slot 1: minerSplit
	SlotMinerSplit = common.BigToHash(big.NewInt(1))
	// Slot 2: stakerSplit
	SlotStakerSplit = common.BigToHash(big.NewInt(2))

	// Slot 3: stakerPoolShares (Double Mapping: staker => pool => shares)
	SlotStakerPoolSharesBase = common.BigToHash(big.NewInt(3))

	// Slot 4: __unused_slot_4 (preserved by StakingContract for layout stability)
	SlotUnusedStakerUnbondingSharesBase = common.BigToHash(big.NewInt(4))

	// Slot 5: poolTotalAssets (Mapping: pool => amount in BDAG)
	SlotPoolTotalAssetsBase = common.BigToHash(big.NewInt(5))

	// Slot 6: poolTotalShares (Mapping: pool => amount in Shares)
	SlotPoolTotalSharesBase = common.BigToHash(big.NewInt(6))

	// Slot 7: poolEpochAutoStake (Mapping: pool => epoch => shares)
	SlotPoolEpochAutoStakeBase = common.BigToHash(big.NewInt(7))

	// Slot 8: genesisTime (uint256)
	SlotGenesisTime = common.BigToHash(big.NewInt(8))

	// Slot 9: lastClaimedAutoStakeEpoch (Mapping: operator => epoch)
	SlotLastClaimedAutoStakeEpochBase = common.BigToHash(big.NewInt(9))

	// Slot 11: userFirstDepositTimestamp (Mapping: staker => timestamp)
	SlotUserFirstDepositTimestampBase = common.BigToHash(big.NewInt(11))

	// Slot 12: stakerTotalLockedShares (Mapping: staker => pool => shares)
	SlotStakerTotalLockedSharesBase = common.BigToHash(big.NewInt(12))

	// Slot 13: poolTotalStakers (Mapping: pool => uint256)
	SlotPoolTotalStakersBase = common.BigToHash(big.NewInt(13))

	// Slot 14: stakerPrincipalBDAG (Double Mapping: staker => pool => BDAG)
	SlotStakerPrincipalBDAGBase = common.BigToHash(big.NewInt(14))

	// Slot 15: poolTotalStake (Mapping: pool => BDAG)
	SlotPoolTotalStakeBase = common.BigToHash(big.NewInt(15))

	// Slot 16: isManager (Mapping: manager => bool)
	SlotIsManagerBase = common.BigToHash(big.NewInt(16))

	// Slot 17: stakerUnbondingBDAG (Double Mapping: staker => pool => BDAG)
	SlotStakerUnbondingBDAGBase = common.BigToHash(big.NewInt(17))

	// V3 reward-voting accounting. These append after the current proxy layout;
	// the consensus engine and Solidity implementation share these exact roots.
	SlotPoolEpochRewardPerShareBase = common.BigToHash(big.NewInt(34))
	SlotPoolRewardRemainderBase     = common.BigToHash(big.NewInt(35))
	SlotStakingEpochVotingTotalBase = common.BigToHash(big.NewInt(36))
	SlotOperatorEpochVotingBase     = common.BigToHash(big.NewInt(37))

	// OpenZeppelin ERC-7201 storage namespaces used by the upgradeable staking contract.
	SlotInitializableStorage = common.HexToHash("0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00")
	SlotReentrancyGuardState = common.HexToHash("0x9b779b17422d0df92223018b32b4d1fa46e071723d6817e2486d003becc55f00")

	// Reward Distribution Configuration
	DefaultMinerSplitPct   = uint64(70) // 70%
	DefaultStakersSplitPct = uint64(30) // 30%
	OperatorKickbackPct    = uint64(10) // 10% of Stakers Share
	AutoStakePct           = uint64(10) // 10% of Stakers Share (3% Total)

	// Staking Pot Wallet (Removed in V2)
)
