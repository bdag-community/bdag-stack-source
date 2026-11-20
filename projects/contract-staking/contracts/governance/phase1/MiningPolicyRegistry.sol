// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Advisory mining-policy records. Recording a document changes no
/// consensus validation, rewards, miner eligibility or execution privileges.
contract MiningPolicyRegistry {
    struct Policy { string contentUri; bytes32 contentHash; }
    address public immutable owner;
    mapping(bytes32 => Policy) public policies;
    error OnlyMiningTimelock();
    error InvalidPolicy();
    event PolicyRecorded(bytes32 indexed topic, string contentUri, bytes32 contentHash);
    constructor(address miningTimelock) {
        require(miningTimelock != address(0), "invalid owner");
        owner = miningTimelock;
    }
    function setPolicy(bytes32 topic, string calldata contentUri, bytes32 contentHash) external {
        if (msg.sender != owner) revert OnlyMiningTimelock();
        if (topic == bytes32(0) || contentHash == bytes32(0) || bytes(contentUri).length == 0 || bytes(contentUri).length > 2048) revert InvalidPolicy();
        policies[topic] = Policy(contentUri, contentHash);
        emit PolicyRecorded(topic, contentUri, contentHash);
    }
}
