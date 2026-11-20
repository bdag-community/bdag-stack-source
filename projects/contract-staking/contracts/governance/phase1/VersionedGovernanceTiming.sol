// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {IVersionedEpochClock} from "../../credentials/IVersionedEpochClock.sol";
import {EpochSchedule} from "../../credentials/EpochSchedule.sol";

/// @notice Candidate-only timing module. Consensus must atomically install this
/// with its bound Governor and native clock. No administrator or policy setter.
contract VersionedGovernanceTiming {
    struct Terms { uint256 start; uint256 end; uint256 delay; uint256 grace; }
    struct Record { Terms terms; uint256 epoch; bool critical; }
    struct EpochPolicy {
        uint32 ordinaryNumerator;
        uint32 ordinaryDenominator;
        uint32 criticalEpochs;
        uint32 graceEpochs;
    }
    address public immutable governor;
    IVersionedEpochClock public immutable clock;
    uint256 public immutable legacyDuration;
    uint256 public immutable legacyOrdinaryDelay;
    uint256 public immutable legacyCriticalDelay;
    uint256 public immutable ordinaryDelayNumerator;
    uint256 public immutable ordinaryDelayDenominator;
    uint256 public immutable criticalDelayEpochs;
    uint256 public immutable actionGraceEpochs;
    mapping(uint256 => Record) private _records;
    // Transaction-scoped by the Governor's atomic queue call. Kept in the new
    // module so existing Governor and timelock storage layouts do not change.
    uint256 public activeQueueDelay;
    uint256 public activeExecutionId;
    bool public legacyExecution;
    error InvalidTiming();
    error Unauthorized();

    constructor(address governor_, IVersionedEpochClock clock_, uint256 duration_, uint256 ordinary_, uint256 critical_, EpochPolicy memory policy_) {
        if (governor_ == address(0) || address(clock_).code.length == 0 || duration_ == 0
            || ordinary_ == 0 || critical_ < ordinary_) revert InvalidTiming();
        EpochSchedule.Status memory initial = clock_.epochBounds(0);
        if (initial.duration != duration_) revert InvalidTiming();
        // Constitutional minima are fractions/counts of an epoch, never days.
        // The 3/7 ordinary floor preserves the agreed Mainnet 3-day minimum
        // when its epoch is 7 days. Round up when test epochs do not divide by 7.
        uint256 numerator = policy_.ordinaryNumerator;
        uint256 denominator = policy_.ordinaryDenominator;
        if (denominator == 0 || numerator * 7 < denominator * 3
            || policy_.criticalEpochs == 0 || policy_.graceEpochs < 2
            || numerator > uint256(policy_.criticalEpochs) * denominator) revert InvalidTiming();
        governor = governor_; clock = clock_;
        legacyDuration = duration_; legacyOrdinaryDelay = ordinary_; legacyCriticalDelay = critical_;
        ordinaryDelayNumerator = numerator;
        ordinaryDelayDenominator = denominator;
        criticalDelayEpochs = policy_.criticalEpochs;
        actionGraceEpochs = policy_.graceEpochs;
    }

    /// @notice Convert the immutable epoch policy to seconds for a specific
    /// epoch's duration. Callers must not substitute today's length for an old
    /// proposal's duration. uint64 clock lengths and uint32 policy inputs keep
    /// every multiplication below uint256; ceiling never shortens a minimum.
    function delaysForDuration(uint256 duration) public view returns (uint256 ordinary, uint256 critical, uint256 grace) {
        if (duration == 0 || duration > type(uint64).max) revert InvalidTiming();
        ordinary = (duration * ordinaryDelayNumerator + ordinaryDelayDenominator - 1) / ordinaryDelayDenominator;
        critical = duration * criticalDelayEpochs;
        grace = duration * actionGraceEpochs;
    }

    function record(uint256 id, uint256 epoch, bool critical) external {
        if (msg.sender != governor) revert Unauthorized();
        if (id == 0 || _records[id].terms.end != 0 || epoch != clock.epochStatus().epoch + 1) revert InvalidTiming();
        EpochSchedule.Status memory bounds = clock.epochBounds(epoch);
        (uint256 ordinaryDelay, uint256 criticalDelay, uint256 grace) = delaysForDuration(bounds.duration);
        _records[id] = Record(Terms(bounds.start, bounds.end, critical ? criticalDelay : ordinaryDelay, grace), epoch, critical);
    }

    function beginQueue(uint256 id, uint256 epoch, bool critical) external {
        if (msg.sender != governor) revert Unauthorized();
        if (id == 0 || activeQueueDelay != 0) revert InvalidTiming();
        activeQueueDelay = terms(id, epoch, critical).delay;
    }

    function endQueue() external {
        if (msg.sender != governor) revert Unauthorized();
        if (activeQueueDelay == 0) revert InvalidTiming();
        activeQueueDelay = 0;
    }

    /// @notice Exposes the provenance of the currently executing approved
    /// action without letting a target or wallet choose its own policy version.
    function beginExecution(uint256 id) external {
        if (msg.sender != governor) revert Unauthorized();
        if (id == 0 || activeExecutionId != 0) revert InvalidTiming();
        activeExecutionId = id;
        legacyExecution = _records[id].terms.end == 0;
    }

    function endExecution() external {
        if (msg.sender != governor) revert Unauthorized();
        if (activeExecutionId == 0) revert InvalidTiming();
        activeExecutionId = 0; legacyExecution = false;
    }

    function terms(uint256 id, uint256 epoch, bool critical) public view returns (Terms memory) {
        Record storage saved = _records[id];
        if (saved.terms.end != 0) {
            if (saved.epoch != epoch || saved.critical != critical) revert InvalidTiming();
            return saved.terms;
        }
        // No record means a proposal created by the original Governor, not a
        // default to the new policy. Preserve its exact original timing.
        uint256 start = clock.genesisTime() + epoch * legacyDuration;
        return Terms(start, start + legacyDuration, critical ? legacyCriticalDelay : legacyOrdinaryDelay, 2 * legacyDuration);
    }

    /// 0=pending, 1=active, 2=closed. Does not decide passage or execute anything.
    function phase(uint256 id, uint256 epoch) external view returns (uint8) {
        Record storage saved = _records[id];
        Terms memory timing = terms(id, epoch, saved.terms.end != 0 && saved.critical);
        uint256 now_ = clock.governanceTime();
        return now_ < timing.start ? 0 : now_ < timing.end ? 1 : 2;
    }
}
