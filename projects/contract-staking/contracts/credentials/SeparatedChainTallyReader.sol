// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {VersionedChainTallyReader} from "./VersionedChainTallyReader.sol";

/// @notice Separate reward destination; the historical clock identity stays
/// bound to the retiring staking proxy so existing exits keep working.
/// Slots 10..11 are written only by the atomic native transition. No setter.
contract SeparatedChainTallyReader is VersionedChainTallyReader {
    address private _activeStakingSource; // 10
    uint256 private _stakingCutoverEpochPlusOne; // 11

    constructor(address legacy_) VersionedChainTallyReader(legacy_) {}

    function activeStakingSource() public view returns (address source) {
        source = _activeStakingSource;
        if (source == address(0) || source == address(clockSource) || source.code.length == 0
            || _stakingCutoverEpochPlusOne == 0) revert InvalidClock();
    }

    function stakingCutoverEpoch() public view returns (uint256) {
        activeStakingSource();
        return _stakingCutoverEpochPlusOne - 1;
    }

    function stakingSourceAt(uint256 sourceEpoch) external view returns (address) {
        return sourceEpoch < stakingCutoverEpoch() ? address(clockSource) : activeStakingSource();
    }
}
