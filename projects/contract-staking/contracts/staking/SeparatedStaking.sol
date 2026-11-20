// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {SeparatedStakingBase, ILegacyFinancialEpochReader} from "./SeparatedStakingBase.sol";
import {EpochSchedule} from "../credentials/EpochSchedule.sol";
import {IVersionedEpochClock} from "../credentials/IVersionedEpochClock.sol";
import {VersionedGovernanceTiming} from "../governance/phase1/VersionedGovernanceTiming.sol";

interface ILegacyStakingRestriction {
    function stakingBlockedUntil(address account) external view returns (uint256);
}

interface ISeparatedRewardSource {
    function activeStakingSource() external view returns (address);
}

/// @notice Fresh V2 at a distinct proxy; legacy clock and positions stay put.
/// Only append-only epoch policy state, no new administrator. The reader and its schedule must be installed
/// atomically with this runtime. Missing/malformed clocks fail closed.
contract SeparatedStaking is SeparatedStakingBase {
    struct EpochPeriod { uint128 numerator; uint128 denominator; }
    // Appended after the native V3 reward ledger. A zero denominator means an
    // untouched pre-migration policy, not permission to reinterpret its seconds.
    EpochPeriod public bondingEpochPolicy; // 41
    EpochPeriod public unbondingEpochPolicy; // 42
    VersionedGovernanceTiming public immutable timingModule;
    error InvalidRewardClockSource();
    event EpochPeriodChanged(bytes32 indexed parameter, uint128 numerator, uint128 denominator);
    IVersionedEpochClock private constant CLOCK = IVersionedEpochClock(0x1000000000000000000000000000000000000002);

    address public immutable legacyClockSource;

    constructor(VersionedGovernanceTiming timing_, address legacy_) {
        if (address(timing_).code.length == 0 || address(timing_.clock()) != address(CLOCK)) revert InvalidRewardClockSource();
        if (legacy_ == address(0) || legacy_.code.length == 0) revert InvalidRewardClockSource();
        legacyClockSource = legacy_;
        timingModule = timing_;
    }

    function _inheritedStakingBlock(address account) internal view override returns (uint256) {
        return ILegacyStakingRestriction(legacyClockSource).stakingBlockedUntil(account);
    }

    function _legacyAction() private view returns (bool) {
        if (timingModule.activeExecutionId() == 0) revert InvalidParameter();
        return timingModule.legacyExecution();
    }

    function _checkClockGenesis() private view {
        if (CLOCK.clockSource() != legacyClockSource
            || ISeparatedRewardSource(address(CLOCK)).activeStakingSource() != address(this)) revert InvalidRewardClockSource();
        if (CLOCK.genesisTime() != genesisTime) revert InvalidGenesisTime();
    }

    function _clockStatus() private view returns (EpochSchedule.Status memory) {
        _checkClockGenesis();
        return CLOCK.epochStatus();
    }

    function epochDuration() public view override returns (uint256) {
        return _clockStatus().duration;
    }

    function bondingPeriod() public view override returns (uint256) {
        EpochPeriod memory policy = bondingEpochPolicy;
        return policy.denominator == 0 ? super.bondingPeriod() : _epochPeriodSeconds(policy);
    }

    function unbondingPeriod() public view override returns (uint256) {
        EpochPeriod memory policy = unbondingEpochPolicy;
        return policy.denominator == 0 ? super.unbondingPeriod() : _epochPeriodSeconds(policy);
    }

    function _epochPeriodSeconds(EpochPeriod memory policy) private view returns (uint256) {
        return (uint256(policy.numerator) * epochDuration() + policy.denominator - 1) / policy.denominator;
    }

    /// @notice Keep the existing seconds-input ABI for approved calldata, but
    /// snapshot its ratio to the execution epoch for every future policy read.
    /// Existing user lock/exit timestamps and the old override words are intact.
    function setBondingPeriod(uint256 period) public override onlyOwner {
        if (_legacyAction()) {
            if (period > 8 * timingModule.legacyDuration()) revert InvalidParameter();
            _bondingOverridePlusOne = period + 1;
            delete bondingEpochPolicy;
            emit ParameterChanged("bondingPeriod", period);
            return;
        }
        uint256 duration = epochDuration();
        if (period > 8 * duration) revert InvalidParameter();
        bondingEpochPolicy = EpochPeriod(uint128(period), uint128(duration));
        emit ParameterChanged("bondingPeriod", period);
        emit EpochPeriodChanged("bondingPeriod", uint128(period), uint128(duration));
    }

    function setUnbondingPeriod(uint256 period) public override onlyOwner {
        if (_legacyAction()) {
            uint256 previousDuration = timingModule.legacyDuration();
            if (period < previousDuration || period > 6 * previousDuration) revert InvalidParameter();
            _unbondingOverride = period;
            delete unbondingEpochPolicy;
            emit ParameterChanged("unbondingPeriod", period);
            return;
        }
        uint256 duration = epochDuration();
        if (period < duration || period > 6 * duration) revert InvalidParameter();
        unbondingEpochPolicy = EpochPeriod(uint128(period), uint128(duration));
        emit ParameterChanged("unbondingPeriod", period);
        emit EpochPeriodChanged("unbondingPeriod", uint128(period), uint128(duration));
    }

    function currentEpoch() public view override returns (uint256) {
        return _clockStatus().epoch;
    }

    function timeToNextEpoch() external view override returns (uint256) {
        EpochSchedule.Status memory status = _clockStatus();
        if (block.timestamp < genesisTime) return genesisTime - block.timestamp;
        return status.end - block.timestamp;
    }

    function operatorEpoch() public view override returns (uint256) {
        // Never reinterpret the native operator auto-stake maturity clock.
        return ILegacyFinancialEpochReader(address(CLOCK)).engineEpoch();
    }

    function _fallbackBondingPeriod(address staker) internal view override returns (uint256) {
        _checkClockGenesis();
        return 4 * CLOCK.epochAtTime(userFirstDepositTimestamp[staker]).duration;
    }

    function _nextUnbondingReleaseTime() internal view override returns (uint256) {
        // Use the real next boundary, not genesis modulo today's duration.
        return _clockStatus().end + unbondingPeriod();
    }

    function _rescueUserFromInactivePool(address staker, address inactivePool, address[] calldata activePools) internal override {
        bool needsFirstDeposit = userFirstDepositTimestamp[staker] == 0;
        super._rescueUserFromInactivePool(staker, inactivePool, activePools);
        if (needsFirstDeposit && userFirstDepositTimestamp[staker] != 0) {
            // The native floor, not timestamp lookup, identifies the active
            // clock for a newly initialized position on a backward-time block.
            _bondingEndsAt[staker] = userFirstDepositTimestamp[staker] + bondingPeriod();
        }
    }

    function _resetUnbondingTimers(address staker) internal override {
        UnbondingRequest[] storage requests = unbondingRequests[staker];
        if (requests.length == 0) return;
        uint256 next = _nextUnbondingReleaseTime();
        for (uint256 i; i < requests.length; ++i) {
            // Preserve existing entitlements even if an earlier policy produced
            // a longer deadline. A top-up may extend a live lock, never shorten it.
            if (requests[i].shares > 0 && block.timestamp < requests[i].releaseTime && next > requests[i].releaseTime) {
                requests[i].releaseTime = next;
            }
        }
    }
}
