// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {VersionedGovernanceTiming} from "../../governance/phase1/VersionedGovernanceTiming.sol";
contract GovernanceCredentialFixture {
    uint256 public immutable genesisTime;
    uint256 public immutable epochDuration;
    bool public votingReady = true;
    uint256 public total;
    mapping(address => uint256) public weights;
    mapping(address => bool) public delegates;
    constructor(uint256 start, uint256 seconds_) { genesisTime=start; epochDuration=seconds_; }
    function currentEpoch() external view returns(uint256) { return (block.timestamp-genesisTime)/epochDuration; }
    function totalSupplyAt(uint256) external view returns(uint256) { return total; }
    function getVotes(address who,uint256,bytes calldata) external view returns(uint256) { return weights[who]; }
    function isDelegate(address who,uint256) external view returns(bool) { return delegates[who]; }
    function setWeight(address who,uint256 value) external { total=total-weights[who]+value; weights[who]=value; }
    function setDelegate(address who,bool yes) external { delegates[who]=yes; }
    function setReady(bool ready) external { votingReady=ready; }
}
contract GovernanceTargetFixture {
    uint256 public value;
    bool public fail;
    address public owner;
    uint256 public observedExecution;
    bool public observedLegacy;
    constructor() { owner=msg.sender; }
    function setOwner(address who) external { require(msg.sender==owner);owner=who; }
    function mining(uint256 next) external { require(msg.sender==owner);value=next; }
    function staking(uint256 next) external { require(msg.sender==owner);require(!fail,"fixture failure");value=next; }
    function stakingCritical(uint256 next) external { require(msg.sender==owner);value=next; }
    function setFail(bool yes) external { fail=yes; }
    function core(uint256 next) external { require(msg.sender==owner);value=next; }
    function coreOrdinary(uint256 next) external { require(msg.sender==owner);value=next; }
    function inspectTiming(VersionedGovernanceTiming timing) external {
        require(msg.sender==owner);
        observedExecution=timing.activeExecutionId(); observedLegacy=timing.legacyExecution();
    }
    function setBlockedBatch(address[] calldata, bool) external { require(msg.sender==owner);value=777; }
    function upgradeToAndCall(address,bytes calldata) external { require(msg.sender==owner);value=999; }
}

import {GovernanceHubV2, ICoreExecution} from "../../governance/phase1/GovernanceHubV2.sol";
contract GovernanceHubUpgradeFixture is GovernanceHubV2 {
    constructor(address timelock_, ICoreExecution governor_) GovernanceHubV2(timelock_,governor_) {}
    function marker() external pure returns(uint256) { return 2; }
}
