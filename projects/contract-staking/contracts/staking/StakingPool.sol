// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts/utils/math/Math.sol";

/**
 * @title StakingContract
 * @dev Staking contract for BDAG mining pools using Vault (Shares) Math.
 *      Reward distribution is handled by the Consensus Engine directly updating
 *      poolTotalStaked.
 *      Supports Multiple Pools per User.
 */
contract StakingContract is
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
    uint256 public constant INITIAL_BONDING_PERIOD = 4 weeks;
    uint256 public constant UNBONDING_PERIOD = 3 weeks;
    uint256 public constant EPOCH_DURATION = 7 days;
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

    /* ========== MODIFIERS ========== */

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    modifier onlyManager() {
        if (!isManager[msg.sender] && msg.sender != owner)
            revert NotAuthorized();
        _;
    }

    modifier onlyStaker(address stakerAddress) {
        if (msg.sender != stakerAddress) revert NotAuthorized();
        _;
    }

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
        if (amount < MIN_STAKE) revert BelowMinStake();
        if (poolAddress == address(0)) revert InvalidPool();

        // 1. Initial 4-Week Lock Check
        if (userFirstDepositTimestamp[stakerAddress] == 0) {
            userFirstDepositTimestamp[stakerAddress] = block.timestamp;
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

        for (uint256 i = 0; i < stakers.length; i++) {
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
    ) internal {
        uint256 liquidShares = stakerPoolShares[stakerAddress][inactivePool];
        if (liquidShares == 0) return;

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

        emit Redelegated(stakerAddress, splitBdagValue, inactivePool, toPool);
    }

    function unstake(
        address stakerAddress,
        uint256 totalAmount,
        address[] calldata pools
    ) external nonReentrant whenNotPaused onlyStaker(stakerAddress) {
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

            // PROFIT-FIRST: Deduct from yield before touching principal
            uint256 currentPrincipal = stakerPrincipalBDAG[stakerAddress][poolAddress];
            uint256 currentTotalValue = Math.mulDiv(
                    stakerPoolShares[stakerAddress][poolAddress],
                    _getSharePrice(poolAddress),
                    1e18
                ) + actualAmount;
            uint256 currentProfit = currentTotalValue > currentPrincipal 
                ? currentTotalValue - currentPrincipal 
                : 0;

            if (actualAmount > currentProfit) {
                uint256 principalReduction = actualAmount - currentProfit;
                if (currentPrincipal > principalReduction) {
                    stakerPrincipalBDAG[stakerAddress][poolAddress] -= principalReduction;
                } else {
                    stakerPrincipalBDAG[stakerAddress][poolAddress] = 0;
                }
            }
            // else: amount <= profit, so Principal stays exactly the same!

            if (poolTotalStake[poolAddress] > actualAmount) {
                poolTotalStake[poolAddress] -= actualAmount;
            } else {
                poolTotalStake[poolAddress] = 0;
            }
        }

        // 2. Epoch-Aligned Unbonding
        uint256 rawReleaseTime = block.timestamp + UNBONDING_PERIOD;
        uint256 timeSinceGenesis = block.timestamp - genesisTime;
        uint256 currentEpochProgress = timeSinceGenesis % EPOCH_DURATION;
        uint256 remainingTimeToNextEpoch = EPOCH_DURATION -
            currentEpochProgress;

        uint256 finalReleaseTime = rawReleaseTime + remainingTimeToNextEpoch;

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
            block.timestamp >=
                userFirstDepositTimestamp[stakerAddress] +
                    INITIAL_BONDING_PERIOD,
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
        uint256 currentEpochIndex = (block.timestamp - genesisTime) /
            EPOCH_DURATION;
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

    function _resetUnbondingTimers(address _staker) internal {
        UnbondingRequest[] storage requests = unbondingRequests[_staker];
        uint256 len = requests.length;

        if (len == 0) return;

        // V2 Anti-Whale: Recalculate full unbonding penalty logic based on NOW + new epoch boundary
        uint256 rawReleaseTime = block.timestamp + UNBONDING_PERIOD;
        uint256 timeSinceGenesis = block.timestamp - genesisTime;
        uint256 currentEpochProgress = timeSinceGenesis % EPOCH_DURATION;
        uint256 remainingTimeToNextEpoch = EPOCH_DURATION -
            currentEpochProgress;
        uint256 newReleaseTime = rawReleaseTime + remainingTimeToNextEpoch;

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
        if (_newOwner.code.length > 0) revert CannotBeContract();

        emit OwnershipTransferred(owner, _newOwner);
        owner = _newOwner;
    }

    /**
     * @notice Pause the contract.
     */
    function pause() external onlyOwner {
        _pause();
    }

    /**
     * @notice Unpause the contract.
     */
    function unpause() external onlyOwner {
        _unpause();
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
    function currentEpoch() external view returns (uint256) {
        if (block.timestamp < genesisTime) return 0;
        return (block.timestamp - genesisTime) / EPOCH_DURATION;
    }

    /**
     * @notice Returns the time remaining until the next epoch starts (in seconds).
     */
    function timeToNextEpoch() external view returns (uint256) {
        if (block.timestamp < genesisTime) return genesisTime - block.timestamp;
        uint256 timeSinceGenesis = block.timestamp - genesisTime;
        return EPOCH_DURATION - (timeSinceGenesis % EPOCH_DURATION);
    }

    /* ========== GOVERNANCE ========== */

    function setSplitRatios(
        uint256 _miner,
        uint256 _staker
    ) external onlyOwner {
        if (_miner + _staker != 100) revert SumMustBe100();
        minerSplit = _miner;
        stakerSplit = _staker;
        emit SplitRatiosUpdated(_miner, _staker);
    }

    function addManagers(address[] calldata _managers) external onlyOwner {
        for (uint256 i = 0; i < _managers.length; i++) {
            isManager[_managers[i]] = true;
        }
    }

    function removeManagers(address[] calldata _managers) external onlyOwner {
        for (uint256 i = 0; i < _managers.length; i++) {
            isManager[_managers[i]] = false;
        }
    }

    function setGenesisTime(uint256 _genesisTime) external onlyOwner {
        if (_genesisTime > block.timestamp) revert InvalidGenesisTime();
        genesisTime = _genesisTime;
    }



    // Receive funds for unbonding payouts from owner manually or via direct transfer
    receive() external payable {}
}
