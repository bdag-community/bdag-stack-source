// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {EpochSchedule} from "./EpochSchedule.sol";

/// @notice Read-only native clock ABI. No configuration or authority setters.
interface IVersionedEpochClock {
    function clockSource() external view returns (address);
    function genesisTime() external view returns (uint256);
    function epochStatus() external view returns (EpochSchedule.Status memory);
    function epochBounds(uint256 epoch) external view returns (EpochSchedule.Status memory);
    function epochAtTime(uint256 timestamp) external view returns (EpochSchedule.Status memory);
    function governanceTime() external view returns (uint256);
}
