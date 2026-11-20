// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ProjectEscrow} from "./ProjectEscrow.sol";
import {IVersionedEpochClock} from "../credentials/IVersionedEpochClock.sol";
import {EpochSchedule} from "../credentials/EpochSchedule.sol";
import {VersionedGovernanceTiming} from "../governance/phase1/VersionedGovernanceTiming.sol";

/// @notice Candidate for new campaigns only. Existing clones retain their
/// original implementation and financial terms. No mutable timing authority.
contract VersionedProjectEscrow is ProjectEscrow {
    IVersionedEpochClock public immutable clock;
    VersionedGovernanceTiming public immutable timing;
    uint256 public policyEpoch;
    uint256 public policyEpochSeconds;
    uint256 public maximumFundingSeconds;
    uint256 public inactivitySeconds;
    uint256 private _challengeMinimum;

    constructor(IVersionedEpochClock clock_, VersionedGovernanceTiming timing_) {
        require(address(clock_).code.length > 0 && address(timing_).code.length > 0
            && address(timing_.clock()) == address(clock_), "clock/timing");
        clock = clock_; timing = timing_;
    }

    function _initializeTiming() internal override {
        EpochSchedule.Status memory status = clock.epochStatus();
        require(status.duration > 0 && status.duration <= type(uint64).max, "epoch duration");
        (, uint256 critical,) = timing.delaysForDuration(status.duration);
        policyEpoch = status.epoch; policyEpochSeconds = status.duration;
        maximumFundingSeconds = (90 * status.duration + 6) / 7;
        inactivitySeconds = (180 * status.duration + 6) / 7;
        // One response epoch + up to one epoch waiting for the ballot + one
        // complete voting epoch + critical delay. Default: four epochs (28 days
        // on Mainnet). Stronger reviewed critical policy cannot shorten this.
        _challengeMinimum = 3 * status.duration + critical;
        require(_challengeMinimum <= type(uint64).max, "challenge overflow");
    }

    function _maximumFundingWindow() internal view override returns (uint256) { return maximumFundingSeconds; }
    function _minimumChallenge() internal view override returns (uint256) { return _challengeMinimum; }
    function _inactivityPeriod() internal view override returns (uint256) { return inactivitySeconds; }
}
