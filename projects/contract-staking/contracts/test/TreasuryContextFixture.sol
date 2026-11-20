// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
contract TreasuryContextFixture {
    uint256 public executingCoreProposal;
    uint256 public currentEpoch;
    function setContext(uint256 id, uint256 epoch) external { executingCoreProposal = id; currentEpoch = epoch; }
}
