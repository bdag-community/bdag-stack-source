// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface INativeEpochReader {
    function rewardEpoch() external view returns (uint256);
}

/// @dev Native Finalize advances reward-ledger slot 4, never decreases it.
/// Historical financial epochs in slot 0 are a separate clock. Positive block
/// timestamps may decrease, so governance uses the reward epoch as a floor.
/// The fork installs the Reader; pre-fork deployments remain inert and can read
/// their cached timestamp clock while the canonical address has no runtime.
library NativeEpochClock {
    address internal constant READER = 0x1000000000000000000000000000000000000002;

    function currentEpoch(uint256 genesis, uint256 duration) internal view returns (uint256 epoch) {
        epoch = block.timestamp < genesis ? 0 : (block.timestamp - genesis) / duration;
        if (READER.code.length != 0) {
            uint256 floor = INativeEpochReader(READER).rewardEpoch();
            if (floor > epoch) epoch = floor;
        }
    }
}
