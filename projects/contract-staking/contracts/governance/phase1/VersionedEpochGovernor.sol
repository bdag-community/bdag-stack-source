// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {EpochGovernor} from "./EpochGovernor.sol";
import {Governor} from "@openzeppelin/contracts/governance/Governor.sol";
import {TimelockController} from "@openzeppelin/contracts/governance/TimelockController.sol";
import {GovernancePolicyModule} from "./GovernancePolicyModule.sol";
import {IGovernanceCredential} from "./IGovernanceCredential.sol";
import {VersionedGovernanceTiming} from "./VersionedGovernanceTiming.sol";
import {VersionedGovernanceTimelock} from "./VersionedGovernanceTimelock.sol";

/// @notice Uninstalled candidate. Preserves Governor storage and authority;
/// only the separately reviewed native transition may replace the runtime.
contract VersionedEpochGovernor is EpochGovernor {
    VersionedGovernanceTiming public immutable timingModule;

    constructor(
        IGovernanceCredential mining_, IGovernanceCredential staking_,
        TimelockController miningLock_, TimelockController stakingLock_,
        GovernancePolicyModule policy_, Configuration memory config, Route[] memory allowed,
        VersionedGovernanceTiming timing_
    ) EpochGovernor(mining_, staking_, miningLock_, stakingLock_, policy_, config, allowed) {
        if (address(timing_).code.length == 0 || timing_.governor() != address(this)
            || timing_.clock().genesisTime() != mining_.genesisTime()) revert InvalidConfiguration();
        (uint256 ordinary, uint256 critical,) = timing_.delaysForDuration(config.epochSeconds);
        if (ordinary != config.ordinaryDelay || critical != config.criticalDelay) revert InvalidConfiguration();
        if (address(VersionedGovernanceTimelock(payable(address(miningLock_))).timingModule()) != address(timing_)
            || address(VersionedGovernanceTimelock(payable(address(stakingLock_))).timingModule()) != address(timing_)) revert InvalidConfiguration();
        timingModule = timing_;
    }
    // Scalar getters describe constructor configuration, not historical terms.
    // Clients must read each proposal's frozen terms, not epochSeconds math.
    function proposalTiming(uint256 id) public view returns (VersionedGovernanceTiming.Terms memory) {
        uint256 epoch = proposalSnapshot(id);
        if (epoch == 0) revert InvalidConfiguration();
        return timingModule.terms(id, epoch, proposalPolicy[id].tier == Tier.CRITICAL);
    }
    function _afterProposal(uint256 id) internal override {
        timingModule.record(id, proposalSnapshot(id), proposalPolicy[id].tier == Tier.CRITICAL);
    }
    function queue(address[] memory targets, uint256[] memory values, bytes[] memory data, bytes32 descriptionHash)
        public override returns (uint256 id) {
        id = getProposalId(targets, values, data, descriptionHash);
        timingModule.beginQueue(id, proposalSnapshot(id), proposalPolicy[id].tier == Tier.CRITICAL);
        super.queue(targets, values, data, descriptionHash);
        timingModule.endQueue();
    }
    function _proposalDelay(uint256 id) internal view override returns (uint256) { return proposalTiming(id).delay; }
    function execute(address[] memory targets, uint256[] memory values, bytes[] memory data, bytes32 descriptionHash)
        public payable override returns (uint256 id) {
        id = getProposalId(targets, values, data, descriptionHash);
        timingModule.beginExecution(id);
        super.execute(targets, values, data, descriptionHash);
        timingModule.endExecution();
    }
    function _proposalGrace(uint256 id) internal view override returns (uint256) { return proposalTiming(id).grace; }
    function _proposalVotingEnd(uint256 id) internal view override returns (uint256) { return proposalTiming(id).end; }
    function _actionClock() internal view override returns (uint256) { return timingModule.clock().governanceTime(); }
    function _proposalState(uint256 id) internal view override returns (ProposalState) {
        // Preserve OZ's existence check and irreversible cancellation/execution.
        ProposalState original = Governor.state(id);
        if (original == ProposalState.Executed || original == ProposalState.Canceled) return original;
        // A previously queued operation retains its exact stored ETA.
        if (proposalEta(id) != 0) return ProposalState.Queued;
        uint8 phase = timingModule.phase(id, proposalSnapshot(id));
        if (phase == 0) return ProposalState.Pending;
        if (phase == 1) return ProposalState.Active;
        return _quorumReached(id) && _voteSucceeded(id) ? ProposalState.Succeeded : ProposalState.Defeated;
    }
}
