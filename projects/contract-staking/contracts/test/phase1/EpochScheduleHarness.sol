// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {EpochSchedule} from "../../credentials/EpochSchedule.sol";

contract EpochScheduleHarness {
    function validate(EpochSchedule.Segment[] memory segments) external pure {
        EpochSchedule.validate(segments);
    }
    function bounds(EpochSchedule.Segment[] memory segments, uint256 epoch) external pure returns (EpochSchedule.Status memory) {
        EpochSchedule.validate(segments);
        return EpochSchedule.bounds(segments, epoch);
    }
    function at(EpochSchedule.Segment[] memory segments, uint256 timestamp, uint256 floor) external pure returns (EpochSchedule.Status memory) {
        EpochSchedule.validate(segments);
        return EpochSchedule.at(segments, timestamp, floor);
    }
}
