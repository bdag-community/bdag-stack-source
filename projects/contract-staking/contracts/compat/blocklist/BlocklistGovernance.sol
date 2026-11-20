// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title BlocklistGovernance
/// @notice Minimal owner-governed wallet blocklist read by node consensus.
///
/// Storage layout is consensus-critical and MUST match bevm/params/blocklist.go:
///   slot 0: owner   (address)
///   slot 1: blocked (mapping(address => bool))  - READ BY NODE CONSENSUS
///
/// The node reads `blocked[addr]` directly from state at this contract's
/// address; it never calls the contract. Only governance writes go through the
/// EVM here. Mainnet deployment: deploy this contract as a regular
/// transaction (the deployer becomes the owner), then pin the resulting
/// address as BlocklistContractAddr in bevm/params/blocklist.go before
/// cutting the upgrade release. (Dev networks may instead predeploy via
/// BLOCKLIST_CONTRACT_CODE / BLOCKLIST_GENESIS_* env at genesis.)
contract BlocklistGovernance {
    address public owner;                        // slot 0
    mapping(address => bool) public blocked;     // slot 1 - consensus-critical

    event Blocked(address indexed account);
    event Unblocked(address indexed account);
    event OwnerChanged(address indexed previousOwner, address indexed newOwner);

    modifier onlyOwner() {
        require(msg.sender == owner, "not owner");
        _;
    }

    constructor() {
        owner = msg.sender;
    }

    function setBlocked(address account, bool isBlocked_) external onlyOwner {
        require(account != address(0), "zero account");
        blocked[account] = isBlocked_;
        if (isBlocked_) {
            emit Blocked(account);
        } else {
            emit Unblocked(account);
        }
    }

    function setBlockedBatch(address[] calldata accounts, bool isBlocked_) external onlyOwner {
        for (uint256 i = 0; i < accounts.length; i++) {
            require(accounts[i] != address(0), "zero account");
            blocked[accounts[i]] = isBlocked_;
            if (isBlocked_) {
                emit Blocked(accounts[i]);
            } else {
                emit Unblocked(accounts[i]);
            }
        }
    }

    function transferOwnership(address newOwner) external onlyOwner {
        require(newOwner != address(0), "zero owner");
        emit OwnerChanged(owner, newOwner);
        owner = newOwner;
    }

    function isBlocked(address account) external view returns (bool) {
        return blocked[account];
    }
}
