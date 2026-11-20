// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {EpochCredential} from "./EpochCredential.sol";
import {EpochSchedule} from "./EpochSchedule.sol";
import {IVersionedEpochClock} from "./IVersionedEpochClock.sol";

/// @notice Uninstalled candidate for the separately gated clock transition.
contract VersionedEpochCredential is EpochCredential {
    constructor(address staking_, address mining_, string memory name_, string memory symbol_)
        EpochCredential(staking_, mining_, name_, symbol_) {}

    function _clockStatus() private view returns (EpochSchedule.Status memory) {
        IVersionedEpochClock clock = IVersionedEpochClock(SYSTEM_READER);
        // Preserve the constructor-pinned source identity/genesis. A changed
        // duration is valid only through that source's native versioned clock.
        if (clock.clockSource() != address(stakingSource) || clock.genesisTime() != genesisTime()
            || stakingSource.genesisTime() != genesisTime()) revert InvalidSource();
        return clock.epochStatus();
    }

    function currentEpoch() public view override returns (uint256) {
        return _clockStatus().epoch;
    }

    function epochDuration() public view override returns (uint256) {
        return _clockStatus().duration;
    }
}
