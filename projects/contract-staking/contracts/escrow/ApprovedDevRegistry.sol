// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Public review attestations. Reviewers cannot also become approved bidders.
contract ApprovedDevRegistry {
    address public immutable owner;
    mapping(address => bool) public reviewers;
    mapping(address => bool) public isApproved;
    mapping(address => bytes32) public reviewHash;
    event DeveloperReviewed(address indexed developer, bool approved, bytes32 reviewHash);
    constructor(address governance, address[3] memory council) {
        require(governance != address(0), "owner"); owner = governance;
        for (uint256 i; i < 3; ++i) {
            require(council[i] != address(0) && !reviewers[council[i]], "council");
            reviewers[council[i]] = true;
        }
    }
    function setApproval(address developer, bool approved, bytes32 evidence) external {
        require(msg.sender == owner, "governance");
        require(developer != address(0) && evidence != bytes32(0), "review");
        require(!approved || !reviewers[developer], "reviewer cannot bid");
        isApproved[developer] = approved; reviewHash[developer] = evidence;
        emit DeveloperReviewed(developer, approved, evidence);
    }
}
