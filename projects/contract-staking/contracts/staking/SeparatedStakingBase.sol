// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts/utils/math/Math.sol";
import {NativeEpochClock} from "../credentials/NativeEpochClock.sol";

interface IRewardEpochReader {
    function rewardEpoch() external view returns (uint256);
    function firstCompleteRewardEpochPlusOne() external view returns (uint256);
    function isFinalized(uint256 epoch) external view returns (bool);
}

interface ILegacyFinancialEpochReader {
    function engineEpoch() external view returns (uint256);
}

/**
 * @title SeparatedStakingBase
 * @dev Versioned copy: historical StakingPoolV2.sol and its deployed bytecode remain frozen.
 * @dev Local DAO candidate. Preserve the engine's storage ABI at slots 0-17.
 *      Governance and principal histories append at slots 18-33. A fork installs
 *      the implementation and replaces the compromised owner with a timelock.
 *      No former manager remains authorized. There is no treasury withdrawal or
 *      stranded-principal repair in this version.
 *      Staking contract for BDAG mining pools using Vault (Shares) Math.
 *      Reward distribution is handled by the Consensus Engine directly updating
 *      poolTotalStaked.
 *      Supports Multiple Pools per User.
 */
contract SeparatedStakingBase is
    Initializable,
    ReentrancyGuardUpgradeable,
    PausableUpgradeable,
    UUPSUpgradeable
{
    /* ========== CUSTOM ERRORS ========== */
    error NotOwner();
    error NotAuthorized();
    error InvalidAddress();
    error AmountMismatch();
    error BelowMinStake();
    error InvalidPool();
    error CannotUnstakeZero();
    error InsufficientLiquidStake();
    error TooManyRequests();
    error InsufficientUnlockedStake();
    error NothingToClaim();
    error TransferFailed();
    error TooShort();
    error SumMustBe100();
    error CannotBeContract();
    error InvalidGenesisTime();
    error TooManyPools();
    error DisabledLegacyFunction();
    error InvalidParameter();
    error StakingBlocked();
    error VotingNotSeeded();
    error InvalidVotingSeed();
    error DuplicatePool();

    /* ========== STATE VARIABLES ========== */

    // NOTE: Storage layout is crucial here as the Go Consensus Engine reads/writes
    // these values directly by slot index. Do not reorder existing variables.

    // Governance
    // Governance
    address public owner;

    // Subsidy Split Configuration (read by Go Engine)
    // Default: 70% to Miner, 30% to Staking Contract
    uint256 public minerSplit;
    uint256 public stakerSplit;

    // Constants
    uint256 public constant MIN_STAKE = 0.001 ether;
    uint256 public constant MAX_UNBONDING_REQUESTS = 100;
    uint256 public constant MAX_MULTI_UNSTAKE_LIMIT = 200;

    // Time-based Locks
    // Preserve these getter selectors and their view mutability in both builds.
    function epochDuration() public view virtual returns (uint256) { return 7 days; }
    function EPOCH_DURATION() public view returns (uint256) { return epochDuration(); }
    function INITIAL_BONDING_PERIOD() public view returns (uint256) { return 4 * epochDuration(); }
    function UNBONDING_PERIOD() public view returns (uint256) { return 3 * epochDuration(); }
    function operatorEpoch() public view virtual returns (uint256) {
        return (block.timestamp - genesisTime) / epochDuration();
    }
    uint256 public constant AUTO_STAKE_LOCK_EPOCHS = 52; // 52 weeks

    /* ========== MAPPINGS (Critical Slots) ========== */

    // Slot 3: Staker Pool Shares (staker => pool => shares)
    // Replaces old 'stakingTallies'
    mapping(address => mapping(address => uint256)) public stakerPoolShares;

    // Slot 4: DEPRECATED (Previously stakerUnbondingShares)
    // This slot is preserved to maintain storage layout stability for Go Engine.
    mapping(address => mapping(address => uint256)) private __unused_slot_4;

    // Slot 5: Pool Total Assets (pool => total amount in BDAG)
    // Params: SlotPoolTotalAssetsBase (Shifted to 5)
    mapping(address => uint256) public poolTotalAssets;

    // Slot 6: Pool Total Shares (pool => total shares)
    // Params: SlotPoolTotalSharesBase (Shifted to 6 - Repurposed from AccTokenPerShare)
    // UPDATED DIRECTLY BY CONSENSUS ENGINE
    mapping(address => uint256) public poolTotalShares;

    // Slot 7: Auto Stake Mappings (pool => epoch => SHARES)
    mapping(address => mapping(uint256 => uint256)) public poolEpochAutoStake;

    // Slot 8: Genensis Time for Epoch Calculation
    uint256 public genesisTime;

    // Slot 9: Operator's Last Claimed Auto-Stake Epoch
    mapping(address => uint256) public lastClaimedAutoStakeEpoch;

    struct UnbondingRequest {
        uint256 shares;
        uint256 releaseTime;
        address pool;
        bool isClaimed;
        uint256 amount; // Fixed BDAG amount locked at unstake time
    }
    // Slot 10: Requests for principal withdrawal
    mapping(address => UnbondingRequest[]) public unbondingRequests;

    // Slot 11: Global 4-Week Lock Registration
    mapping(address => uint256) public userFirstDepositTimestamp;

    // Slot 12: stakerTotalLockedShares (Mapping: staker => pool => shares)
    // Tracks the sum of all locked auto-stakes for an operator
    mapping(address => mapping(address => uint256))
        public stakerTotalLockedShares;

    // Slot 13: poolTotalStakers (Mapping: pool => uint256)
    // O(1) counter of unique stakers in a pool for frontend querying
    mapping(address => uint256) public poolTotalStakers;

    /* ========== EVENTS ========== */

    event Staked(address indexed staker, uint256 amount, address indexed pool);
    event Unstaked(
        address indexed staker,
        uint256 shares,
        uint256 amount,
        address indexed pool
    );
    event StakeClaimed(
        address indexed staker,
        uint256 totalPayout,
        uint256 unbondingPayout
    );
    event Redelegated(
        address indexed staker,
        uint256 amount,
        address indexed fromPool,
        address indexed toPool
    );
    event OwnershipTransferred(
        address indexed previous,
        address indexed newOwner
    );
    event SplitRatiosUpdated(uint256 miner, uint256 staker);

    // Slot 14: Track Principal Deposits (staker => pool => BDAG Amount)
    // This allows the frontend to show "Staked" vs "Earned Yield" separately
    mapping(address => mapping(address => uint256)) public stakerPrincipalBDAG;

    // Slot 15: Track Pool Level Stake Deposits (pool => BDAG Amount)
    // This allows the frontend to show "Total Pool Staked" (Principal) vs "Total Pool Assets"
    mapping(address => uint256) public poolTotalStake;

    // Slot 16: Managers (address => bool)
    // Managers are allowed to rescue stuck users from dead pools
    mapping(address => bool) public isManager;

    // Slot 17: Track Fixed BDAG in unbonding (staker => pool => BDAG Amount)
    // REPLACES Slot 4 (stakerUnbondingShares) for reward calculation
    mapping(address => mapping(address => uint256)) public stakerUnbondingBDAG;

    /* ========== V2 STATE (append-only, slot 18+) ========== */

    // Slot 18: block.timestamp when initializeV2() ran through the proxy.
    // Stays 0 when V2 was installed by a node fork-height code swap until the
    // owner calls initializeV2() explicitly. Never read by the Go Engine.
    uint256 public v2ActivatedAt;

    struct VoteCheckpoint { uint48 epoch; int208 weight; }
    // A user's global first-deposit bonding gate applies to every pool/top-up.
    // Source epoch E records the balance at the END of E, usable during E+1.
    mapping(address => mapping(address => VoteCheckpoint[])) private _votingCheckpoints; // 19
    VoteCheckpoint[] private _totalVotingCheckpoints; // 20; signed deltas until seeded
    mapping(address => uint256) public stakingBlockedUntil; // 21; max = permanent DAO block
    address private _retiredGuardian; // 22; storage preserved, no authority
    uint256 private _retiredGuardianExpiry; // 23; storage preserved, no authority
    uint256 private _minStakeOverride; // 24
    uint256 internal _bondingOverridePlusOne; // 25; 0 fallback, 1 means no bonding delay
    uint256 internal _unbondingOverride; // 26
    uint256 public pausedUntil; // 27; timestamp, auto-expires
    address public pendingOwner; // 28
    bytes32 private _reservedRepairRoot; // 29; V2.1 only, no repair entry point
    mapping(bytes32 => bool) private _reservedRepaired; // 30
    uint256 private _retiredVotingSeedEpoch; // 31; historical storage only
    bool private _retiredVotingSeeded; // 32 offset 0; historical storage only
    bool public daoInitialized; // 32 offset 1
    mapping(address => uint256) internal _bondingEndsAt; // 33; new-user frozen unlock

    // V3 native reward accounting, shared with the consensus engine (slots34+).
    mapping(address => mapping(uint256 => uint256)) public poolEpochRewardPerShare; // 34
    mapping(address => uint256) public poolRewardRemainder; // 35
    mapping(uint256 => uint256) public stakingEpochVotingTotal; // 36
    mapping(address => mapping(uint256 => uint256)) public operatorEpochVoting; // 37
    mapping(address => mapping(address => mapping(uint256 => uint256))) private _rewardSettled; // 38
    mapping(address => mapping(address => mapping(uint256 => uint256))) private _rewardAccCheckpoint; // 39
    struct RewardShareCheckpoint { uint48 epoch; uint208 shares; }
    mapping(address => mapping(address => RewardShareCheckpoint[])) private _rewardShares; // 40

    address private constant REWARD_READER = 0x1000000000000000000000000000000000000002;

    event OwnershipTransferStarted(address indexed previousOwner, address indexed pendingOwner);
    event DaoInitialized(address indexed owner, uint256 activationEpoch);
    event VotingTotalSeeded(uint256 indexed epoch, uint256 bondedTotal, bytes32 indexed snapshotHash);
    event StakingBlockChanged(address indexed staker, uint256 until);
    event StakingPauseChanged(uint256 until);
    event ParameterChanged(bytes32 indexed parameter, uint256 value);

    /// @notice Emitted once per reinitializer run; proves the V2 initializer executed.
    event StakingVersion(uint256 indexed version);

    /* ========== MODIFIERS ========== */

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    modifier onlyManager() {
        // The mapping is retained for the engine/storage ABI, never authority.
        if (msg.sender != owner) revert NotAuthorized();
        _;
    }

    modifier onlyStaker(address stakerAddress) {
        if (msg.sender != stakerAddress) revert NotAuthorized();
        _;
    }

    // The retiring DAO block remains effective until governance lifts it there.
    function _inheritedStakingBlock(address) internal view virtual returns (uint256) { return 0; }

    function initialize() public initializer {
        __ReentrancyGuard_init();
        __Pausable_init();
        owner = msg.sender;
        // Default splits
        minerSplit = 70;
        stakerSplit = 30;
        genesisTime = block.timestamp;
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal override onlyOwner {}

    /* ========== V2 ========== */

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        // Locks the bare implementation's own Initializable storage. The proxy's
        // storage (where _initialized == 1) is untouched, so reinitializer(2) still runs.
        _disableInitializers();
    }

    /// @notice One-shot V2 initializer, carried in upgradeToAndCall(data) on the UUPS
    ///         path, or called directly by the owner after a fork-height code swap.
    function initializeV2() external reinitializer(2) onlyOwner {
        v2ActivatedAt = block.timestamp;
        emit StakingVersion(2);
    }

    /// @notice Semantic version of the implementation. Pure so it is correct even when
    ///         the code was installed without running any initializer.
    function version() external pure returns (string memory) {
        return "2.0.0";
    }

    function minStake() public view returns (uint256) {
        return _minStakeOverride == 0 ? MIN_STAKE : _minStakeOverride;
    }

    function bondingPeriod() public view virtual returns (uint256) {
        return _bondingOverridePlusOne == 0 ? INITIAL_BONDING_PERIOD() : _bondingOverridePlusOne - 1;
    }

    function unbondingPeriod() public view virtual returns (uint256) {
        return _unbondingOverride == 0 ? UNBONDING_PERIOD() : _unbondingOverride;
    }

    function bondingEndsAt(address staker) public view returns (uint256) {
        uint256 first = userFirstDepositTimestamp[staker];
        if (first == 0) return 0;
        uint256 fixedEnd = _bondingEndsAt[staker];
        return fixedEnd == 0 ? first + _fallbackBondingPeriod(staker) : fixedEnd;
    }

    // A versioned candidate can preserve the original default for old positions
    // without iterating accounts or rewriting already-frozen deadlines.
    function _fallbackBondingPeriod(address) internal view virtual returns (uint256) {
        return INITIAL_BONDING_PERIOD();
    }

    function _maturityEpoch(address staker) private view returns (uint256) {
        uint256 end = bondingEndsAt(staker);
        return end <= genesisTime ? 0 : (end - genesisTime) / epochDuration();
    }

    function rewardEpochFinalized(uint256 epoch) public view returns (bool) {
        if (REWARD_READER.code.length == 0) return false;
        try IRewardEpochReader(REWARD_READER).isFinalized(epoch) returns (bool result) { return result; }
        catch { return false; }
    }

    function votingWeightAt(address staker, address[] calldata pools, uint256 epoch) external view returns (uint256 weight) {
        if (!rewardEpochFinalized(epoch)) return 0;
        if (pools.length > MAX_MULTI_UNSTAKE_LIMIT) revert TooManyPools();
        for (uint256 i; i < pools.length; ++i) {
            for (uint256 j; j < i; ++j) if (pools[i] == pools[j]) revert DuplicatePool();
            address pool = pools[i];
            if (pool == address(0)) revert InvalidPool();
            if (staker == pool) {
                weight += operatorEpochVoting[staker][epoch];
                continue;
            }
            uint256 finalAccumulator = poolEpochRewardPerShare[pool][epoch];
            uint256 checkpoint = _rewardAccCheckpoint[staker][pool][epoch];
            uint256 settled = _rewardSettled[staker][pool][epoch];
            if (finalAccumulator < checkpoint) revert InvalidParameter();
            weight += settled + _rewardSharesAt(staker, pool, epoch) * (finalAccumulator - checkpoint);
        }
    }

    function totalVotingWeightAt(uint256 epoch) external view returns (uint256) {
        if (!rewardEpochFinalized(epoch)) revert VotingNotSeeded();
        return stakingEpochVotingTotal[epoch];
    }

    function _rewardEpoch() private view returns (uint256 epoch) {
        if (REWARD_READER.code.length == 0) return currentEpoch();
        try IRewardEpochReader(REWARD_READER).rewardEpoch() returns (uint256 result) { return result; }
        catch { return currentEpoch(); }
    }

    function _firstRewardEpoch() private view returns (uint256 epoch, bool available) {
        if (REWARD_READER.code.length == 0) return (0, false);
        try IRewardEpochReader(REWARD_READER).firstCompleteRewardEpochPlusOne() returns (uint256 plusOne) {
            return plusOne == 0 ? (0, false) : (plusOne - 1, true);
        } catch { return (0, false); }
    }

    function _rewardSharesAt(address staker, address pool, uint256 epoch) private view returns (uint256) {
        RewardShareCheckpoint[] storage history = _rewardShares[staker][pool];
        if (history.length == 0) return stakerPoolShares[staker][pool] + stakerTotalLockedShares[staker][pool];
        uint256 low; uint256 high = history.length;
        while (low < high) {
            uint256 mid = (low + high) / 2;
            if (history[mid].epoch <= epoch) low = mid + 1;
            else high = mid;
        }
        return low == 0 ? 0 : history[low - 1].shares;
    }

    function _writeRewardShares(address staker, address pool, uint256 epoch, uint256 shares) private {
        if (epoch > type(uint48).max || shares > type(uint208).max) revert InvalidParameter();
        RewardShareCheckpoint[] storage history = _rewardShares[staker][pool];
        if (history.length != 0 && history[history.length - 1].epoch == epoch) {
            history[history.length - 1].shares = uint208(shares);
        } else {
            history.push(RewardShareCheckpoint(uint48(epoch), uint208(shares)));
        }
    }

    function _beforePrincipalChange(address staker, address pool) private {
        if (staker == pool || REWARD_READER.code.length == 0) return;
        uint256 epoch = _rewardEpoch();
        uint256 shares = stakerPoolShares[staker][pool] + stakerTotalLockedShares[staker][pool];
        RewardShareCheckpoint[] storage history = _rewardShares[staker][pool];
        if (history.length == 0) {
            (uint256 first, bool available) = _firstRewardEpoch();
            if (available && first < epoch) _writeRewardShares(staker, pool, first, shares);
        }
        uint256 accumulator = poolEpochRewardPerShare[pool][epoch];
        uint256 checkpoint = _rewardAccCheckpoint[staker][pool][epoch];
        if (accumulator < checkpoint) revert InvalidParameter();
        _rewardSettled[staker][pool][epoch] += shares * (accumulator - checkpoint);
    }

    function _principalChanged(address staker, address pool, uint256) private {
        if (staker == pool || REWARD_READER.code.length == 0) return;
        uint256 epoch = _rewardEpoch();
        _rewardAccCheckpoint[staker][pool][epoch] = poolEpochRewardPerShare[pool][epoch];
        _writeRewardShares(staker, pool, epoch, stakerPoolShares[staker][pool] + stakerTotalLockedShares[staker][pool]);
    }

    /* ========== VIEWS ========== */

    function poolStakerAmounts(
        address _pool,
        address _staker
    ) public view returns (uint256) {
        uint256 totalUserShares = stakerPoolShares[_staker][_pool] +
            stakerTotalLockedShares[_staker][_pool];

        if (totalUserShares == 0) return 0;

        return Math.mulDiv(totalUserShares, _getSharePrice(_pool), 1e18);
    }

    function poolStakerShares(
        address _pool,
        address _staker
    ) public view returns (uint256) {
        return
            stakerPoolShares[_staker][_pool] +
            stakerTotalLockedShares[_staker][_pool];
    }

    function poolStakerYield(
        address _pool,
        address _staker
    ) public view returns (uint256) {
        uint256 totalValue = poolStakerAmounts(_pool, _staker);
        uint256 principal = stakerPrincipalBDAG[_staker][_pool];

        if (totalValue <= principal) return 0;
        return totalValue - principal;
    }

    /* ========== ACTIONS ========== */

    function stake(
        address stakerAddress,
        uint256 amount,
        address poolAddress
    ) external payable nonReentrant whenNotPaused onlyStaker(stakerAddress) {
        if (msg.value != amount) revert AmountMismatch();
        if (amount < minStake()) revert BelowMinStake();
        if (poolAddress == address(0)) revert InvalidPool();
        if (stakingBlockedUntil[stakerAddress] > block.timestamp || _inheritedStakingBlock(stakerAddress) > block.timestamp) revert StakingBlocked();
        _beforePrincipalChange(stakerAddress, poolAddress);
        uint256 oldPrincipal = stakerPrincipalBDAG[stakerAddress][poolAddress];

        // 1. Initial 4-Week Lock Check
        if (userFirstDepositTimestamp[stakerAddress] == 0) {
            userFirstDepositTimestamp[stakerAddress] = block.timestamp;
            _bondingEndsAt[stakerAddress] = block.timestamp + bondingPeriod();
        }

        // 2. Anti-Whale Unbonding Reset
        _resetUnbondingTimers(stakerAddress);

        // 3. State Updates
        if (
            stakerPoolShares[stakerAddress][poolAddress] == 0 &&
            stakerTotalLockedShares[stakerAddress][poolAddress] == 0
        ) {
            poolTotalStakers[poolAddress]++;
        }

        uint256 totalAssets = poolTotalAssets[poolAddress];
        uint256 totalShares = poolTotalShares[poolAddress];
        uint256 sharesToMint;

        if (totalShares == 0 || totalAssets == 0) {
            sharesToMint = amount;
        } else {
            sharesToMint = (amount * totalShares) / totalAssets;
        }

        unchecked {
            stakerPoolShares[stakerAddress][poolAddress] += sharesToMint;
            poolTotalShares[poolAddress] += sharesToMint;
            poolTotalAssets[poolAddress] += amount;
            stakerPrincipalBDAG[stakerAddress][poolAddress] += amount;
            poolTotalStake[poolAddress] += amount;
        }

        _principalChanged(stakerAddress, poolAddress, oldPrincipal);

        // Note: We DO NOT forward funds to adminVault. The BDAG stays here to back the Shares.

        emit Staked(stakerAddress, amount, poolAddress);
    }

    function adminRescueDelegation(
        address[] calldata stakers,
        address[] calldata inactivePools,
        address[] calldata activePools
    ) external nonReentrant whenNotPaused onlyManager {
        if (activePools.length == 0 || inactivePools.length == 0)
            revert InvalidPool();
        if (activePools.length > 20 || inactivePools.length > 20 || stakers.length > 200) revert TooManyPools();
        for (uint256 i; i < activePools.length; ++i) {
            if (activePools[i] == address(0)) revert InvalidPool();
            for (uint256 j; j < i; ++j) if (activePools[i] == activePools[j]) revert DuplicatePool();
            for (uint256 j; j < inactivePools.length; ++j) if (activePools[i] == inactivePools[j]) revert DuplicatePool();
        }

        for (uint256 i = 0; i < stakers.length; i++) {
            if (stakingBlockedUntil[stakers[i]] > block.timestamp) revert StakingBlocked();
            for (uint256 d = 0; d < inactivePools.length; d++) {
                address inactivePool = inactivePools[d];
                uint256 totalAssetsFrom = poolTotalAssets[inactivePool];
                uint256 totalSharesFrom = poolTotalShares[inactivePool];

                if (totalAssetsFrom > 0 && totalSharesFrom > 0) {
                    _rescueUserFromInactivePool(
                        stakers[i],
                        inactivePool,
                        activePools
                    );
                }
            }
        }
    }

    function _rescueUserFromInactivePool(
        address stakerAddress,
        address inactivePool,
        address[] calldata activePools
    ) internal virtual {
        uint256 liquidShares = stakerPoolShares[stakerAddress][inactivePool];
        if (liquidShares == 0) return;
        _beforePrincipalChange(stakerAddress, inactivePool);
        uint256 oldPrincipal = stakerPrincipalBDAG[stakerAddress][inactivePool];

        uint256 bdagValue = Math.mulDiv(liquidShares, _getSharePrice(inactivePool), 1e18);
        uint256 splitBdagValue = bdagValue / activePools.length;
        if (splitBdagValue == 0) return;

        uint256 splitPrincipal;
        {
            uint256 totalUserShares = liquidShares +
                stakerTotalLockedShares[stakerAddress][inactivePool];
            uint256 principalToMove = Math.mulDiv(
                stakerPrincipalBDAG[stakerAddress][inactivePool],
                liquidShares,
                totalUserShares
            );
            splitPrincipal = principalToMove / activePools.length;

            if (userFirstDepositTimestamp[stakerAddress] == 0) {
                userFirstDepositTimestamp[stakerAddress] = block.timestamp;
            }
            _resetUnbondingTimers(stakerAddress);

            // 1. Burn shares from inactive pool
            unchecked {
                stakerPoolShares[stakerAddress][inactivePool] = 0;

                if (poolTotalShares[inactivePool] >= liquidShares) {
                    poolTotalShares[inactivePool] -= liquidShares;
                } else {
                    poolTotalShares[inactivePool] = 0;
                }

                if (poolTotalAssets[inactivePool] >= bdagValue) {
                    poolTotalAssets[inactivePool] -= bdagValue;
                } else {
                    poolTotalAssets[inactivePool] = 0;
                }

                if (
                    stakerPrincipalBDAG[stakerAddress][inactivePool] >=
                    principalToMove
                ) {
                    stakerPrincipalBDAG[stakerAddress][
                        inactivePool
                    ] -= principalToMove;
                } else {
                    stakerPrincipalBDAG[stakerAddress][inactivePool] = 0;
                }

                if (poolTotalStake[inactivePool] >= principalToMove) {
                    poolTotalStake[inactivePool] -= principalToMove;
                } else {
                    poolTotalStake[inactivePool] = 0;
                }

                if (
                    stakerTotalLockedShares[stakerAddress][inactivePool] == 0 &&
                    poolTotalStakers[inactivePool] > 0
                ) {
                    poolTotalStakers[inactivePool]--;
                }
            }
        }

        _principalChanged(stakerAddress, inactivePool, oldPrincipal);

        // 2. Mint in new pools
        for (uint256 j = 0; j < activePools.length; j++) {
            _rescueMintToPool(
                stakerAddress,
                inactivePool,
                activePools[j],
                splitBdagValue,
                splitPrincipal
            );
        }
    }

    function _rescueMintToPool(
        address stakerAddress,
        address inactivePool,
        address toPool,
        uint256 splitBdagValue,
        uint256 splitPrincipal
    ) internal {
        if (toPool == address(0)) return;
        _beforePrincipalChange(stakerAddress, toPool);
        uint256 oldPrincipal = stakerPrincipalBDAG[stakerAddress][toPool];

        if (
            stakerPoolShares[stakerAddress][toPool] == 0 &&
            stakerTotalLockedShares[stakerAddress][toPool] == 0
        ) {
            poolTotalStakers[toPool]++;
        }

        uint256 totalAssetsTo = poolTotalAssets[toPool];
        uint256 totalSharesTo = poolTotalShares[toPool];
        uint256 sharesToMint;

        if (totalSharesTo == 0 || totalAssetsTo == 0) {
            sharesToMint = splitBdagValue;
        } else {
            sharesToMint = Math.mulDiv(splitBdagValue, totalSharesTo, totalAssetsTo);
        }

        unchecked {
            stakerPoolShares[stakerAddress][toPool] += sharesToMint;
            poolTotalShares[toPool] += sharesToMint;
            poolTotalAssets[toPool] += splitBdagValue;
            stakerPrincipalBDAG[stakerAddress][toPool] += splitPrincipal;
            poolTotalStake[toPool] += splitPrincipal;
        }

        _principalChanged(stakerAddress, toPool, oldPrincipal);

        emit Redelegated(stakerAddress, splitBdagValue, inactivePool, toPool);
    }

    function unstake(
        address stakerAddress,
        uint256 totalAmount,
        address[] calldata pools
    ) external nonReentrant onlyStaker(stakerAddress) {
        if (totalAmount == 0) revert CannotUnstakeZero();
        uint256 numPools = pools.length;
        if (numPools == 0) revert InvalidPool();
        if (numPools > MAX_MULTI_UNSTAKE_LIMIT) revert TooManyPools();

        uint256 remainingToUnstake = totalAmount;

        for (uint256 i = 0; i < numPools && remainingToUnstake > 0; i++) {
            uint256 actuallyUnstaked = _unstake(stakerAddress, remainingToUnstake, pools[i]);
            if (actuallyUnstaked >= remainingToUnstake) {
                remainingToUnstake = 0;
            } else {
                remainingToUnstake -= actuallyUnstaked;
            }
        }
    }

    function _unstake(
        address stakerAddress,
        uint256 amount,
        address poolAddress
    ) internal returns (uint256) {
        _beforePrincipalChange(stakerAddress, poolAddress);
        uint256 totalAssets = poolTotalAssets[poolAddress];
        uint256 totalShares = poolTotalShares[poolAddress];
        if (totalAssets == 0 || totalShares == 0) revert CannotUnstakeZero();

        uint256 liquidShares = stakerPoolShares[stakerAddress][poolAddress];
        uint256 sharesToUnstake;

        // If amount == MAX, unstake ALL liquid shares (zero dust)
        if (amount == type(uint256).max) {
            sharesToUnstake = liquidShares;
            if (sharesToUnstake == 0) revert CannotUnstakeZero();
        } else {
            // Ceiling division: Math.mulDiv(a,b,c) rounds down, so we add 1 if there's a remainder
            sharesToUnstake = Math.mulDiv(amount, totalShares, totalAssets);
            // Round up (ceiling) so we never give back slightly less than requested
            if (Math.mulDiv(sharesToUnstake, totalAssets, totalShares) < amount) {
                sharesToUnstake += 1;
            }

            if (sharesToUnstake == 0) revert CannotUnstakeZero();

            // Handle division rounding edge cases
            if (sharesToUnstake > liquidShares) {
                sharesToUnstake = liquidShares;
            }
        }

        if (unbondingRequests[stakerAddress].length >= MAX_UNBONDING_REQUESTS)
            revert TooManyRequests();

        uint256 actualAmount = Math.mulDiv(sharesToUnstake, totalAssets, totalShares);

        // 1. Move active shares to the cooling room (STATIC BUCKET)
        unchecked {
            stakerPoolShares[stakerAddress][poolAddress] -= sharesToUnstake;
            stakerUnbondingBDAG[stakerAddress][poolAddress] += actualAmount;

            // CRITICAL: Burn from pool TOTALS immediately to stop reward accrual
            poolTotalShares[poolAddress] -= sharesToUnstake;
            poolTotalAssets[poolAddress] -= actualAmount;

            if (poolTotalStake[poolAddress] > actualAmount) {
                poolTotalStake[poolAddress] -= actualAmount;
            } else {
                poolTotalStake[poolAddress] = 0;
            }
        }

        _deductUnstakedPrincipal(stakerAddress, poolAddress, actualAmount);

        // 2. Epoch-Aligned Unbonding
        uint256 finalReleaseTime = _nextUnbondingReleaseTime();

        unbondingRequests[stakerAddress].push(
            UnbondingRequest({
                shares: sharesToUnstake,
                amount: actualAmount,
                releaseTime: finalReleaseTime,
                pool: poolAddress,
                isClaimed: false
            })
        );

        // 3. Staker participation check (Immediate Exit)
        if (
            stakerPoolShares[stakerAddress][poolAddress] == 0 &&
            stakerTotalLockedShares[stakerAddress][poolAddress] == 0
        ) {
            if (poolTotalStakers[poolAddress] > 0) {
                poolTotalStakers[poolAddress]--;
            }
        }

        emit Unstaked(
            stakerAddress,
            sharesToUnstake,
            actualAmount,
            poolAddress
        );

        return actualAmount;
    }

    function _deductUnstakedPrincipal(address staker, address pool, uint256 actualAmount) private {
        // Preserve the existing profit-first policy after active shares burn.
        uint256 principal = stakerPrincipalBDAG[staker][pool];
        uint256 totalValue = Math.mulDiv(stakerPoolShares[staker][pool], _getSharePrice(pool), 1e18) + actualAmount;
        uint256 profit = totalValue > principal ? totalValue - principal : 0;
        if (actualAmount > profit) {
            uint256 reduction = actualAmount - profit;
            stakerPrincipalBDAG[staker][pool] = principal > reduction ? principal - reduction : 0;
        }
        _principalChanged(staker, pool, principal);
    }

    function claimStake(
        address stakerAddress,
        address poolAddress
    ) external nonReentrant onlyStaker(stakerAddress) {
        // RULE 1: The 4-Week Global Claim Gate
        require(
            userFirstDepositTimestamp[stakerAddress] != 0,
            "No deposit found"
        );
        require(
            block.timestamp >= bondingEndsAt(stakerAddress),
            "4-Week Gate Active"
        );

        // RULE 2: Total Lockdown Check
        UnbondingRequest[] storage requests = unbondingRequests[stakerAddress];
        uint256 len = requests.length;
        bool hasActiveUnbonding = false;

        for (uint256 i = 0; i < len; i++) {
            if (!requests[i].isClaimed) {
                if (block.timestamp < requests[i].releaseTime) {
                    require(false, "Total Lockdown: 3-Week Cooldown Active");
                } else {
                    hasActiveUnbonding = true;
                }
            }
        }

        uint256 totalPayout = 0;
        uint256 unbondingPayout = 0;
        uint256 operatorPayout = 0;

        // Process Principal Withdrawals (Shares -> BDAG at current price)
        if (hasActiveUnbonding) {
            unbondingPayout = _processUnbonding(stakerAddress);
            if (unbondingPayout > 0) {
                totalPayout += unbondingPayout;
            }
        }

        // Process 52-Week Auto-Stakes (Operator Shares -> BDAG at current price)
        operatorPayout = _processOperatorPayout(stakerAddress, poolAddress);
        if (operatorPayout > 0) {
            totalPayout += operatorPayout;
        }

        if (totalPayout == 0) revert NothingToClaim();

        // Dispatch single transaction
        (bool success, ) = payable(stakerAddress).call{value: totalPayout}("");
        if (!success) revert TransferFailed();

        emit StakeClaimed(stakerAddress, totalPayout, unbondingPayout);
    }

    function _processOperatorPayout(
        address stakerAddress,
        address poolAddress
    ) internal returns (uint256) {
        uint256 currentEpochIndex = operatorEpoch();
        uint256 operatorSharePayout = 0;

        // V2 Explicit Check: Only the operator (staker == pool) can claim the pool's 52-week auto-stakes
        if (
            stakerAddress == poolAddress &&
            poolAddress != address(0) &&
            currentEpochIndex >= AUTO_STAKE_LOCK_EPOCHS
        ) {
            uint256 lastClaimed = lastClaimedAutoStakeEpoch[stakerAddress];
            uint256 maxClaimableEpoch = currentEpochIndex -
                AUTO_STAKE_LOCK_EPOCHS;

            if (lastClaimed < maxClaimableEpoch) {
                for (uint256 i = lastClaimed + 1; i <= maxClaimableEpoch; i++) {
                    uint256 autoStakeShares = poolEpochAutoStake[poolAddress][
                        i
                    ];
                    if (autoStakeShares > 0) {
                        operatorSharePayout += autoStakeShares;
                        stakerTotalLockedShares[stakerAddress][
                            poolAddress
                        ] -= autoStakeShares;
                    }
                }
                lastClaimedAutoStakeEpoch[stakerAddress] = maxClaimableEpoch;
            }
        }

        if (operatorSharePayout > 0) {
            uint256 bdagValue = Math.mulDiv(operatorSharePayout, _getSharePrice(poolAddress), 1e18);

            // Burn shares
            unchecked {
                poolTotalShares[poolAddress] -= operatorSharePayout;
                poolTotalAssets[poolAddress] -= bdagValue;
            }

            // Decrement stakers count if fully exited
            if (
                stakerPoolShares[stakerAddress][poolAddress] == 0 &&
                stakerTotalLockedShares[stakerAddress][poolAddress] == 0 &&
                poolTotalStakers[poolAddress] > 0
            ) {
                poolTotalStakers[poolAddress]--;
            }

            return bdagValue;
        }

        return 0;
    }

    /* ========== INTERNAL ========== */

    /**
     * @notice Returns the conversion rate from shares to BDAG.
     *         1 Share = _getSharePrice() / 1e18 BDAG.
     */
    function _getSharePrice(address pool) internal view returns (uint256) {
        uint256 totalShares = poolTotalShares[pool];
        uint256 totalAssets = poolTotalAssets[pool];

        if (totalShares == 0 || totalAssets == 0) {
            return 1e18; // 1:1 ratio
        }
        return Math.mulDiv(totalAssets, 1e18, totalShares);
    }

    function _processUnbonding(address _staker) internal returns (uint256) {
        UnbondingRequest[] storage requests = unbondingRequests[_staker];
        uint256 payout = 0;
        uint256 writeIndex = 0;

        uint256 len = requests.length;
        for (uint256 i = 0; i < len; ) {
            if (requests[i].shares > 0 && !requests[i].isClaimed) {
                if (block.timestamp >= requests[i].releaseTime) {
                    address pool = requests[i].pool;

                    requests[i].isClaimed = true; // Mark as claimed

                    uint256 bdagValue = requests[i].amount;
                    payout += bdagValue;

                    // Clear from the static bucket
                    if (stakerUnbondingBDAG[_staker][pool] >= bdagValue) {
                        unchecked {
                            stakerUnbondingBDAG[_staker][pool] -= bdagValue;
                        }
                    } else {
                        stakerUnbondingBDAG[_staker][pool] = 0;
                    }

                    // Participation check already happened at Unstake (immediate exit).
                    // No need to decrement poolTotalStakers here anymore.
                } else {
                    if (writeIndex != i) {
                        requests[writeIndex] = requests[i];
                    }
                    unchecked {
                        writeIndex++;
                    }
                }
            }
            unchecked {
                ++i;
            }
        }
        while (requests.length > writeIndex) {
            requests.pop();
        }
        return payout;
    }

    function _nextUnbondingReleaseTime() internal view virtual returns (uint256) {
        uint256 rawReleaseTime = block.timestamp + unbondingPeriod();
        uint256 timeSinceGenesis = block.timestamp - genesisTime;
        uint256 currentEpochProgress = timeSinceGenesis % epochDuration();
        return rawReleaseTime + (epochDuration() - currentEpochProgress);
    }

    function _resetUnbondingTimers(address _staker) internal virtual {
        UnbondingRequest[] storage requests = unbondingRequests[_staker];
        uint256 len = requests.length;

        if (len == 0) return;

        // V2 Anti-Whale: Recalculate full unbonding penalty logic based on NOW + new epoch boundary
        uint256 newReleaseTime = _nextUnbondingReleaseTime();

        for (uint256 i = 0; i < len; i++) {
            if (
                requests[i].shares > 0 &&
                block.timestamp < requests[i].releaseTime
            ) {
                requests[i].releaseTime = newReleaseTime;
            }
        }
    }

    /* ========== GOVERNANCE ========== */

    /**
     * @notice Transfer ownership of the contract.
     * @param _newOwner The address of the new owner.
     */
    function transferOwnership(address _newOwner) external onlyOwner {
        if (_newOwner == address(0)) revert InvalidAddress();
        pendingOwner = _newOwner;
        emit OwnershipTransferStarted(owner, _newOwner);
    }

    function acceptOwnership() external {
        if (msg.sender != pendingOwner) revert NotAuthorized();
        emit OwnershipTransferred(owner, msg.sender);
        owner = msg.sender;
        pendingOwner = address(0);
    }

    /**
     * @notice Pause the contract.
     */
    function pause() external onlyOwner {
        pausedUntil = block.timestamp + 2 * epochDuration();
        emit StakingPauseChanged(pausedUntil);
    }

    /**
     * @notice Unpause the contract.
     */
    function unpause() external onlyOwner {
        pausedUntil = 0;
        emit StakingPauseChanged(0);
    }

    function paused() public view override returns (bool) {
        return block.timestamp < pausedUntil;
    }

    function setStakingBlockedUntil(address staker, uint256 until) external onlyOwner {
        if (staker == address(0)) revert InvalidAddress();
        stakingBlockedUntil[staker] = until;
        emit StakingBlockChanged(staker, until);
    }

    function setMinStake(uint256 amount) external onlyOwner {
        if (amount < MIN_STAKE || amount > 1000 ether) revert InvalidParameter();
        _minStakeOverride = amount;
        emit ParameterChanged("minStake", amount);
    }

    function setBondingPeriod(uint256 period) public virtual onlyOwner {
        if (period > 8 * epochDuration()) revert InvalidParameter();
        _bondingOverridePlusOne = period + 1;
        emit ParameterChanged("bondingPeriod", period);
    }

    function setUnbondingPeriod(uint256 period) public virtual onlyOwner {
        if (period < epochDuration() || period > 6 * epochDuration()) revert InvalidParameter();
        _unbondingOverride = period;
        emit ParameterChanged("unbondingPeriod", period);
    }

    /* ========== VIEWS (Frontend Helpers) ========== */

    /**
     * @notice Get all unbonding requests for a staker.
     * @param _staker The address to query.
     */
    function getUnbondingRequests(
        address _staker
    ) external view returns (UnbondingRequest[] memory) {
        return unbondingRequests[_staker];
    }

    /**
     * @notice Returns the current epoch index based on genesis time.
     */
    function currentEpoch() public view virtual returns (uint256) {
        return NativeEpochClock.currentEpoch(genesisTime, epochDuration());
    }

    /**
     * @notice Returns the time remaining until the next epoch starts (in seconds).
     */
    function timeToNextEpoch() external view virtual returns (uint256) {
        if (block.timestamp < genesisTime) return genesisTime - block.timestamp;
        uint256 timeSinceGenesis = block.timestamp - genesisTime;
        return epochDuration() - (timeSinceGenesis % epochDuration());
    }

    /* ========== GOVERNANCE ========== */

    function setSplitRatios(
        uint256 _miner,
        uint256 _staker
    ) external onlyOwner {
        // ABI retained, behavior deliberately retired: these are consensus slots.
        _miner; _staker;
        revert DisabledLegacyFunction();
    }

    function addManagers(address[] calldata _managers) external onlyOwner {
        _managers;
        revert DisabledLegacyFunction();
    }

    function removeManagers(address[] calldata _managers) external onlyOwner {
        for (uint256 i = 0; i < _managers.length; i++) {
            isManager[_managers[i]] = false;
        }
    }

    function setGenesisTime(uint256 _genesisTime) external onlyOwner {
        _genesisTime;
        revert DisabledLegacyFunction();
    }



    // Receive funds for unbonding payouts from owner manually or via direct transfer
    receive() external payable {}
}

