// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title SelectiveBlocklist
/// @notice Owner-governed per-sender destination whitelist read by node consensus.
///
/// A "restricted" address is not fully blocked: it may send transactions, but
/// only to destinations that have been explicitly approved via setAllowedTo.
/// Full blocking (BlocklistGovernance) takes precedence and is checked first by
/// the node; this contract only governs the selective-block dimension.
///
/// Storage layout is consensus-critical and MUST match bevm/params/blocklist.go:
///   slot 0: owner      (address)
///   slot 1: restricted (mapping(address => bool))                     - READ BY NODE CONSENSUS
///   slot 2: allowedTo  (mapping(address => mapping(address => bool))) - READ BY NODE CONSENSUS
///
/// The node reads restricted[addr] and allowedTo[from][to] directly from state
/// at this contract's address; it never calls the contract via EVM.
///
/// Mainnet deployment: deploy this contract as a regular transaction (deployer
/// becomes owner), then pin the resulting address as SelectiveBlocklistContractAddr
/// in bevm/params/blocklist.go before cutting the SelectiveBlocklist upgrade release.
contract SelectiveBlocklist {
    address public owner;                                              // slot 0
    mapping(address => bool) public restricted;                        // slot 1 - consensus-critical
    mapping(address => mapping(address => bool)) public allowedTo;     // slot 2 - consensus-critical

    event Restricted(address indexed account);
    event Unrestricted(address indexed account);
    event AllowedToSet(address indexed from, address indexed to, bool allowed);
    event OwnerChanged(address indexed previousOwner, address indexed newOwner);

    modifier onlyOwner() {
        require(msg.sender == owner, "not owner");
        _;
    }

    constructor() {
        owner = msg.sender;
    }

    // -------------------------------------------------------------------------
    // Restriction management
    // -------------------------------------------------------------------------

    function setRestricted(address account, bool isRestricted_) external onlyOwner {
        require(account != address(0), "zero account");
        restricted[account] = isRestricted_;
        if (isRestricted_) {
            emit Restricted(account);
        } else {
            emit Unrestricted(account);
        }
    }

    function setRestrictedBatch(address[] calldata accounts, bool isRestricted_) external onlyOwner {
        for (uint256 i = 0; i < accounts.length; i++) {
            require(accounts[i] != address(0), "zero account");
            restricted[accounts[i]] = isRestricted_;
            if (isRestricted_) {
                emit Restricted(accounts[i]);
            } else {
                emit Unrestricted(accounts[i]);
            }
        }
    }

    // -------------------------------------------------------------------------
    // Whitelist management
    // -------------------------------------------------------------------------

    function setAllowedTo(address from, address to, bool allowed) external onlyOwner {
        require(from != address(0), "zero from");
        require(to != address(0), "zero to");
        allowedTo[from][to] = allowed;
        emit AllowedToSet(from, to, allowed);
    }

    function setAllowedToBatch(address from, address[] calldata tos, bool allowed) external onlyOwner {
        require(from != address(0), "zero from");
        for (uint256 i = 0; i < tos.length; i++) {
            require(tos[i] != address(0), "zero to");
            allowedTo[from][tos[i]] = allowed;
            emit AllowedToSet(from, tos[i], allowed);
        }
    }

    // -------------------------------------------------------------------------
    // Ownership
    // -------------------------------------------------------------------------

    function transferOwnership(address newOwner) external onlyOwner {
        require(newOwner != address(0), "zero owner");
        emit OwnerChanged(owner, newOwner);
        owner = newOwner;
    }

    // -------------------------------------------------------------------------
    // Views
    // -------------------------------------------------------------------------

    function isRestricted(address account) external view returns (bool) {
        return restricted[account];
    }

    function isAllowedTo(address from, address to) external view returns (bool) {
        return allowedTo[from][to];
    }
}
