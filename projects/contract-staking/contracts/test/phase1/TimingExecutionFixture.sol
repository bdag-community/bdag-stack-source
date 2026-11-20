// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
/// @dev Isolated test-only substitute, never part of a release allocation.
contract TimingExecutionFixture {
    address public clock = 0x1000000000000000000000000000000000000002;
    uint256 public legacyDuration = 60;
    uint256 public activeExecutionId = 1;
    bool public legacyExecution;
    function setLegacy(bool value) external { legacyExecution = value; }
    function setActive(uint256 value) external { activeExecutionId = value; }
}
