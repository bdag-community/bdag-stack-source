// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ChainTallyReader} from "./ChainTallyReader.sol";
import {EpochSchedule} from "./EpochSchedule.sol";

/// @notice Candidate-only reader. Not installed by the historical bootstrap.
contract VersionedChainTallyReader is ChainTallyReader {
    // Slot 8 is an append-only dynamic array. Each entry occupies three full
    // uint256 words, matching the native storage reader. No initializer/setter:
    // only an independently gated consensus transition may write these slots.
    EpochSchedule.Segment[] private _epochSchedule;
    // Slot 9 is the native monotonic timestamp floor. It is not an epoch
    // counter, so slowing epochs cannot reopen an expired proposal window.
    uint256 private _governanceTimeFloor;
    uint256 private constant MAX_SEGMENTS = 64;

    constructor(address clock_) ChainTallyReader(clock_) {}

    function _schedule() private view returns (EpochSchedule.Segment[] memory segments) {
        if (_epochSchedule.length == 0 || _epochSchedule.length > MAX_SEGMENTS) {
            revert EpochSchedule.InvalidEpochSchedule();
        }
        segments = _epochSchedule;
        EpochSchedule.validate(segments);
    }

    function epochStatus() public view returns (EpochSchedule.Status memory) {
        return EpochSchedule.at(_schedule(), block.timestamp, rewardEpoch);
    }
    function governanceTime() external view returns (uint256 timestamp) {
        if (_governanceTimeFloor > type(uint64).max) revert EpochSchedule.EpochClockOverflow();
        timestamp = block.timestamp > _governanceTimeFloor ? block.timestamp : _governanceTimeFloor;
        uint256 epochStart = epochStatus().start;
        if (epochStart > timestamp) timestamp = epochStart;
    }
    function epochBounds(uint256 epoch) external view returns (EpochSchedule.Status memory) {
        return EpochSchedule.bounds(_schedule(), epoch);
    }
    /// @notice Historical timestamp lookup, without the live voting floor.
    /// Used to preserve the original default lock for positions without a
    /// stored deadline. It does not authorize voting in a historical epoch.
    function epochAtTime(uint256 timestamp) external view returns (EpochSchedule.Status memory) {
        return EpochSchedule.at(_schedule(), timestamp, 0);
    }
    function genesisTime() external view returns (uint256) { return _schedule()[0].startTime; }
    function currentEpoch() public view override returns (uint256) { return epochStatus().epoch; }
}
