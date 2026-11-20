// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
interface IGovernanceCredential {
    function currentEpoch() external view returns (uint256);
    function epochDuration() external view returns (uint256);
    function genesisTime() external view returns (uint256);
    function votingReady() external view returns (bool);
    function totalSupplyAt(uint256 credentialEpoch) external view returns (uint256);
    function getVotes(address voter, uint256 credentialEpoch, bytes calldata params) external view returns (uint256);
    function isDelegate(address voter, uint256 credentialEpoch) external view returns (bool);
}
