// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Pure clock arithmetic only. Activation and schedule storage belong to
/// the native consensus transition, not to a wallet or an administrator.
library EpochSchedule {
    struct Segment { uint256 firstEpoch; uint256 startTime; uint256 duration; }
    struct Status { uint256 epoch; uint256 version; uint256 start; uint256 end; uint256 duration; }
    error InvalidEpochSchedule();
    error EpochClockOverflow();

    // Native timestamps and epoch keys use uint64. Reject values that the native
    // engine cannot represent, even though ABI/storage fields are uint256.
    uint256 private constant MAX = type(uint64).max;

    function validate(Segment[] memory segments) internal pure {
        if (segments.length == 0 || segments[0].firstEpoch != 0) revert InvalidEpochSchedule();
        for (uint256 i; i < segments.length; ++i) {
            Segment memory segment = segments[i];
            if (segment.duration == 0) revert InvalidEpochSchedule();
            if (segment.firstEpoch > MAX || segment.startTime > MAX || segment.duration > MAX) {
                revert EpochClockOverflow();
            }
            if (segment.startTime > MAX - segment.duration) revert EpochClockOverflow();
            if (i == 0) continue;
            Segment memory previous = segments[i - 1];
            if (segment.firstEpoch <= previous.firstEpoch) revert InvalidEpochSchedule();
            if (_boundary(previous, segment.firstEpoch) != segment.startTime) revert InvalidEpochSchedule();
        }
    }

    /// @dev Call validate once before using an untrusted schedule. Activation
    /// must append boundary-aligned segments, never rewrite an existing segment.
    function bounds(Segment[] memory segments, uint256 epoch) internal pure returns (Status memory) {
        if (segments.length == 0) revert InvalidEpochSchedule();
        if (epoch > MAX) revert EpochClockOverflow();
        uint256 lo;
        uint256 hi = segments.length;
        while (lo + 1 < hi) {
            uint256 mid = lo + (hi - lo) / 2;
            if (segments[mid].firstEpoch <= epoch) lo = mid;
            else hi = mid;
        }
        Segment memory segment = segments[lo];
        uint256 start = _boundary(segment, epoch);
        if (start > MAX - segment.duration) revert EpochClockOverflow();
        return Status(epoch, lo + 1, start, start + segment.duration, segment.duration);
    }

    /// @dev The persisted native floor prevents backwards timestamps reopening
    /// an epoch. Before genesis, epoch zero retains its future start/end bounds.
    function at(Segment[] memory segments, uint256 timestamp, uint256 floor) internal pure returns (Status memory) {
        if (segments.length == 0) revert InvalidEpochSchedule();
        if (timestamp > MAX || floor > MAX) revert EpochClockOverflow();
        uint256 epoch;
        if (timestamp >= segments[0].startTime) {
            uint256 lo;
            uint256 hi = segments.length;
            while (lo + 1 < hi) {
                uint256 mid = lo + (hi - lo) / 2;
                if (segments[mid].startTime <= timestamp) lo = mid;
                else hi = mid;
            }
            Segment memory segment = segments[lo];
            uint256 delta = (timestamp - segment.startTime) / segment.duration;
            if (delta > MAX - segment.firstEpoch) revert EpochClockOverflow();
            epoch = segment.firstEpoch + delta;
        }
        return bounds(segments, epoch < floor ? floor : epoch);
    }

    function _boundary(Segment memory segment, uint256 epoch) private pure returns (uint256) {
        uint256 delta = epoch - segment.firstEpoch;
        if (delta > (MAX - segment.startTime) / segment.duration) revert EpochClockOverflow();
        return segment.startTime + delta * segment.duration;
    }
}
