// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {UUPSUpgradeable} from "@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol";
interface ICoreExecution {
    function executingCoreProposal() external view returns (uint256);
}
/// @notice Immutable authority also works when the engine installs this runtime behind
/// the legacy transparent proxy; no constructor storage or initializer is required.
contract GovernanceHubV2 is UUPSUpgradeable {
    address public immutable executingTimelock;
    ICoreExecution public immutable governor;
    error CoreApprovalRequired();
    error InvalidRelease();
    event ProposalExecuted(uint256 indexed id, string link, string sha256);
    constructor(address timelock_, ICoreExecution governor_) {
        require(timelock_ != address(0) && address(governor_) != address(0), "invalid authority");
        executingTimelock = timelock_;
        governor = governor_;
    }
    function _authorizeUpgrade(address) internal view override {
        if (msg.sender != executingTimelock || governor.executingCoreProposal() == 0) revert CoreApprovalRequired();
    }
    function announceRelease(uint256 id, string calldata link, string calldata releaseSha256) external {
        if (msg.sender != executingTimelock || governor.executingCoreProposal() == 0 || id == 0) revert CoreApprovalRequired();
        bytes memory digest = bytes(releaseSha256);
        if (bytes(link).length == 0 || bytes(link).length > 2048 || digest.length != 64) revert InvalidRelease();
        for (uint256 i; i < digest.length; ++i) {
            bytes1 c = digest[i];
            if (!((c >= 0x30 && c <= 0x39) || (c >= 0x61 && c <= 0x66))) revert InvalidRelease();
        }
        emit ProposalExecuted(id, link, releaseSha256);
    }
}
