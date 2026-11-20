// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {SeparatedStakingBase, ILegacyFinancialEpochReader} from "./SeparatedStakingBase.sol";
import {EpochSchedule} from "../credentials/EpochSchedule.sol";
import {IVersionedEpochClock} from "../credentials/IVersionedEpochClock.sol";

interface IMainnetRewardDestination { function activeStakingSource() external view returns (address); }

/// @notice Review candidate for a fresh proxy beside original Mainnet V1.
/// No V1 voting/blocking/period getter is assumed: original V1 never had those
/// methods. Global native blocklists still apply; the new proxy owns its own
/// staking restrictions. Native installation/production manifests are separate
/// gates and this source is not installed by a network configuration.
contract MainnetStakingV2 is SeparatedStakingBase {
    error InvalidMainnetRewardSource();
    IVersionedEpochClock private constant CLOCK = IVersionedEpochClock(0x1000000000000000000000000000000000000002);
    address public immutable legacyClockSource;
    struct EpochPeriod { uint128 numerator; uint128 denominator; }
    EpochPeriod public bondingEpochPolicy;
    EpochPeriod public unbondingEpochPolicy;
    event EpochPeriodChanged(bytes32 indexed parameter, uint128 numerator, uint128 denominator);

    constructor(address legacy_) {
        if (legacy_ == address(0) || legacy_.code.length == 0) revert InvalidMainnetRewardSource();
        legacyClockSource = legacy_;
    }
    function _status() private view returns (EpochSchedule.Status memory) {
        if (CLOCK.clockSource() != legacyClockSource
            || IMainnetRewardDestination(address(CLOCK)).activeStakingSource() != address(this)) revert InvalidMainnetRewardSource();
        if (CLOCK.genesisTime() != genesisTime) revert InvalidGenesisTime();
        return CLOCK.epochStatus();
    }
    function epochDuration() public view override returns (uint256) { return _status().duration; }
    function _period(EpochPeriod memory policy) private view returns (uint256) {
        return (uint256(policy.numerator) * epochDuration() + policy.denominator - 1) / policy.denominator;
    }
    function bondingPeriod() public view override returns (uint256) {
        EpochPeriod memory policy = bondingEpochPolicy;
        return policy.denominator == 0 ? super.bondingPeriod() : _period(policy);
    }
    function unbondingPeriod() public view override returns (uint256) {
        EpochPeriod memory policy = unbondingEpochPolicy;
        return policy.denominator == 0 ? super.unbondingPeriod() : _period(policy);
    }
    function setBondingPeriod(uint256 period) public override onlyOwner {
        uint256 duration = epochDuration();
        if (period > 8 * duration) revert InvalidParameter();
        bondingEpochPolicy = EpochPeriod(uint128(period), uint128(duration));
        emit ParameterChanged("bondingPeriod", period);
        emit EpochPeriodChanged("bondingPeriod", uint128(period), uint128(duration));
    }
    function setUnbondingPeriod(uint256 period) public override onlyOwner {
        uint256 duration = epochDuration();
        if (period < duration || period > 6 * duration) revert InvalidParameter();
        unbondingEpochPolicy = EpochPeriod(uint128(period), uint128(duration));
        emit ParameterChanged("unbondingPeriod", period);
        emit EpochPeriodChanged("unbondingPeriod", uint128(period), uint128(duration));
    }

    function currentEpoch() public view override returns (uint256) { return _status().epoch; }
    function operatorEpoch() public view override returns (uint256) { return ILegacyFinancialEpochReader(address(CLOCK)).engineEpoch(); }
    function timeToNextEpoch() external view override returns (uint256) {
        EpochSchedule.Status memory status = _status();
        return block.timestamp < genesisTime ? genesisTime - block.timestamp : status.end - block.timestamp;
    }
    function _fallbackBondingPeriod(address staker) internal view override returns (uint256) {
        _status();
        return 4 * CLOCK.epochAtTime(userFirstDepositTimestamp[staker]).duration;
    }
    function _nextUnbondingReleaseTime() internal view override returns (uint256) { return _status().end + unbondingPeriod(); }
    function _resetUnbondingTimers(address staker) internal override {
        UnbondingRequest[] storage requests = unbondingRequests[staker];
        if (requests.length == 0) return;
        uint256 next = _nextUnbondingReleaseTime();
        for (uint256 i; i < requests.length; ++i) {
            if (requests[i].shares > 0 && block.timestamp < requests[i].releaseTime && next > requests[i].releaseTime) requests[i].releaseTime = next;
        }
    }
}
