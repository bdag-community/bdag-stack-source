// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {IVersionedEpochClock} from "../../credentials/IVersionedEpochClock.sol";

// Test-only electorate. No deployment manifest may include this fixture.
contract VersionedGovernanceCredentialFixture {
    IVersionedEpochClock public immutable source;
    uint256 public total;
    mapping(address => uint256) public weights;
    constructor(IVersionedEpochClock source_) { source = source_; }
    function genesisTime() external view returns (uint256) { return source.genesisTime(); }
    function epochDuration() external view returns (uint256) { return source.epochStatus().duration; }
    function currentEpoch() external view returns (uint256) { return source.epochStatus().epoch; }
    function votingReady() external pure returns (bool) { return true; }
    function totalSupplyAt(uint256) external view returns (uint256) { return total; }
    function getVotes(address who,uint256 epoch,bytes calldata) external view returns (uint256) {
        require(epoch == source.epochStatus().epoch, "expired fixture weight"); return weights[who];
    }
    function setWeight(address who,uint256 value) external { total=total-weights[who]+value; weights[who]=value; }
}
