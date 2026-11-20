// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {TimelockController} from "@openzeppelin/contracts/governance/TimelockController.sol";

/// @notice Immutable Governor-owned timelock. There is no bootstrap account,
///         Guardian, open executor, mutable delay, or role-administration path.
contract GovernanceTimelock is TimelockController {
    address public immutable governor;
    error AuthorityFrozen();

    constructor(uint256 delay_, address governor_)
        TimelockController(delay_, _one(governor_), _one(governor_), address(0)) {
        require(governor_ != address(0) && delay_ > 0, "invalid configuration");
        governor = governor_;
    }

    function _one(address value) private pure returns (address[] memory values) {
        values = new address[](1);
        values[0] = value;
    }

    function grantRole(bytes32, address) public pure override { revert AuthorityFrozen(); }
    function revokeRole(bytes32, address) public pure override { revert AuthorityFrozen(); }
    function renounceRole(bytes32, address) public pure override { revert AuthorityFrozen(); }
    function updateDelay(uint256) external pure override { revert AuthorityFrozen(); }
}
