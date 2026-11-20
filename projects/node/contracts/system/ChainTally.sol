// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * @title ChainTally
 * @dev Read-only interface for Chain Tally data written by Consensus Engine.
 *      Address: 0x1000000000000000000000000000000000000002
 */
contract ChainTally {
    // Slot 0
    uint256 public currentEpoch;

    // Slot 1
    uint256 public totalLiquidity;

    // Slot 2: currentTally (address => amount?) assuming mapping
    mapping(address => uint256) public currentTally;

    // Slot 3: lastTally
    mapping(address => uint256) public lastTally;
}
