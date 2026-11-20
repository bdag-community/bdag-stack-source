// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {GovernanceTimelock} from "./GovernanceTimelock.sol";
import {VersionedGovernanceTiming} from "./VersionedGovernanceTiming.sol";

/// @notice Candidate-only epoch-policy timelock. Native activation must replace
/// both owner timelocks and the Governor atomically. Stored roles, operation
/// identifiers and release timestamps are unchanged. No new administrator.
contract VersionedGovernanceTimelock is GovernanceTimelock {
    VersionedGovernanceTiming public immutable timingModule;
    error MissingGovernorQueue();

    constructor(VersionedGovernanceTiming timing_)
        GovernanceTimelock(_initialDelay(timing_), timing_.governor()) {
        timingModule = timing_;
    }

    function _initialDelay(VersionedGovernanceTiming timing_) private view returns (uint256 ordinary) {
        (ordinary,,) = timing_.delaysForDuration(timing_.clock().epochStatus().duration);
    }

    /// @notice Outside a queue call, reports today's ordinary epoch-policy
    /// floor. During queueing, enforces that proposal's frozen ordinary OR
    /// critical delay, including preserved pre-transition proposals.
    function getMinDelay() public view override returns (uint256) {
        uint256 delay = timingModule.activeQueueDelay();
        return delay == 0 ? _initialDelay(timingModule) : delay;
    }

    modifier onlyGovernorQueue() {
        if (msg.sender != governor || timingModule.activeQueueDelay() == 0) revert MissingGovernorQueue();
        _;
    }

    function schedule(address target, uint256 value, bytes calldata data, bytes32 predecessor, bytes32 salt, uint256 delay)
        public override onlyGovernorQueue {
        super.schedule(target, value, data, predecessor, salt, delay);
    }

    function scheduleBatch(address[] calldata targets, uint256[] calldata values, bytes[] calldata data, bytes32 predecessor, bytes32 salt, uint256 delay)
        public override onlyGovernorQueue {
        super.scheduleBatch(targets, values, data, predecessor, salt, delay);
    }
}
