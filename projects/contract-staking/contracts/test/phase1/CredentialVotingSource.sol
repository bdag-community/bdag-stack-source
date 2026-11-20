// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @dev Test-only source; production weights come from V2 checkpoints / the engine tally.
contract CredentialVotingSource {
    uint256 public immutable genesisTime = block.timestamp;
    uint256 public constant epochDuration = 3600;
    bool public votingSeeded = true;
    uint256 public votingSeedEpoch;
    mapping(uint256 => mapping(address => mapping(address => uint256))) private weights;
    mapping(address => mapping(address => uint256)) public stakerPrincipalBDAG;
    mapping(uint256 => uint256) public totals;

    function currentEpoch() public view returns (uint256) { return (block.timestamp - genesisTime) / epochDuration; }
    function setReady(bool ready) external { votingSeeded = ready; }
    function setWeight(uint256 epoch, address account, address pool, uint256 weight) external {
        weights[epoch][account][pool] = weight;
        stakerPrincipalBDAG[account][pool] = weight;
    }
    function setTotal(uint256 epoch, uint256 total) external { totals[epoch] = total; }
    function totalVotingWeightAt(uint256 epoch) external view returns (uint256) { return totals[epoch]; }
    function votingWeightAt(address account, address[] memory pools, uint256 epoch) external view returns (uint256 amount) {
        for (uint256 i; i < pools.length; ++i) amount += weights[epoch][account][pools[i]];
    }
}
