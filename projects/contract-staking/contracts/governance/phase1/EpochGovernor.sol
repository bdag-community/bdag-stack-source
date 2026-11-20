// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {Governor} from "@openzeppelin/contracts/governance/Governor.sol";
import {GovernorTimelockControl} from "@openzeppelin/contracts/governance/extensions/GovernorTimelockControl.sol";
import {TimelockController} from "@openzeppelin/contracts/governance/TimelockController.sol";
import {SafeCast} from "@openzeppelin/contracts/utils/math/SafeCast.sol";
import {GovernanceTimelock} from "./GovernanceTimelock.sol";
import {GovernancePolicyModule} from "./GovernancePolicyModule.sol";
import {IGovernanceCredential} from "./IGovernanceCredential.sol";

/// @notice Local release candidate. One ballot coordinator preserves two DAO owner
/// addresses and routes CORE to an exact normalized 70/30 tally. See README for
/// immutable policy restrictions and intentionally deferred production features.
contract EpochGovernor is Governor, GovernorTimelockControl {
    enum Domain { INVALID, MINING, STAKING, CORE }
    enum Tier { INVALID, PARAM, CRITICAL }
    struct Route { address target; bytes4 selector; Domain domain; Tier tier; Domain ownerDomain; }
    struct Policy { Domain domain; Tier tier; }
    struct Tally { uint256 againstVotes; uint256 forVotes; uint256 abstainVotes; uint256 voters; uint256 total; }
    struct Ballot { uint256 weight; uint8 support; bool cast; }
    struct Operations { bytes32 mining; bytes32 staking; }
    struct Batch { address[] targets; uint256[] values; bytes[] data; }
    struct Configuration {
        uint48 epochSeconds;
        uint48 ordinaryDelay;
        uint48 criticalDelay;
        uint256 proposalBond;
    }

    IGovernanceCredential public immutable miningCredential;
    IGovernanceCredential public immutable stakingCredential;
    TimelockController public immutable miningTimelock;
    TimelockController public immutable stakingTimelock;
    GovernancePolicyModule public immutable policyModule;
    uint48 public immutable epochSeconds;
    uint48 public immutable ordinaryDelay;
    uint48 public immutable criticalDelay;
    uint256 public immutable proposalBond;
    uint256 public executingCoreProposal;
    mapping(address => mapping(bytes4 => Policy)) public routes;
    mapping(address => Domain) public targetOwnerDomain;
    mapping(uint256 => Policy) public proposalPolicy;
    mapping(uint256 => uint32) public proposalPolicyVersion;
    mapping(uint256 => mapping(Domain => Tally)) private _tallies;
    mapping(uint256 => mapping(Domain => mapping(address => Ballot))) private _ballots;
    mapping(uint256 => Operations) public operationIds;
    mapping(uint256 => uint256) public refundableBond;

    error UnsupportedAction(address target, bytes4 selector);
    error InvalidConfiguration();
    error InvalidBallot();
    error VotingUnavailable();
    error DepositRequired();
    error RefundUnavailable();
    error RefundFailed();
    error AuthorityFrozen();
    error UseChamberQuorum();
    error SupplyOutOfBounds();
    event DenominatorsFrozen(uint256 indexed proposalId, uint256 credentialEpoch, uint256 mining, uint256 staking);
    event BondRefunded(uint256 indexed proposalId, address indexed proposer, uint256 amount);

    constructor(
        IGovernanceCredential mining_, IGovernanceCredential staking_,
        TimelockController miningLock_, TimelockController stakingLock_,
        GovernancePolicyModule policyModule_, Configuration memory config, Route[] memory allowed
    ) Governor("BlockDAG Epoch Governance") GovernorTimelockControl(miningLock_) {
        if (address(mining_) == address(0) || address(staking_) == address(0) || address(miningLock_) == address(stakingLock_)
            || address(miningLock_) == address(0) || address(stakingLock_) == address(0)
            || address(policyModule_) == address(0)
            || config.epochSeconds == 0
            || config.ordinaryDelay < miningLock_.getMinDelay() || config.ordinaryDelay < stakingLock_.getMinDelay()
            || config.criticalDelay < miningLock_.getMinDelay() || config.criticalDelay < stakingLock_.getMinDelay()
            || config.criticalDelay < config.ordinaryDelay || config.proposalBond == 0 || allowed.length == 0) {
            revert InvalidConfiguration();
        }
        if (GovernanceTimelock(payable(address(miningLock_))).governor() != address(this)
            || GovernanceTimelock(payable(address(stakingLock_))).governor() != address(this)
            || policyModule_.governor() != address(this) || policyModule_.controller() != address(miningLock_)) revert InvalidConfiguration();
        miningCredential = mining_; stakingCredential = staking_;
        miningTimelock = miningLock_; stakingTimelock = stakingLock_;
        policyModule = policyModule_;
        epochSeconds = config.epochSeconds;
        ordinaryDelay = config.ordinaryDelay;
        criticalDelay = config.criticalDelay;
        proposalBond = config.proposalBond;
        if (mining_.epochDuration() != config.epochSeconds || staking_.epochDuration() != config.epochSeconds
            || mining_.genesisTime() != staking_.genesisTime()) revert InvalidConfiguration();
        for (uint256 i; i < allowed.length; ++i) {
            Route memory rule = allowed[i];
            if (rule.target == address(0) || rule.selector == bytes4(0) || rule.domain == Domain.INVALID || rule.tier == Tier.INVALID
                || (rule.ownerDomain != Domain.MINING && rule.ownerDomain != Domain.STAKING)) revert InvalidConfiguration();
            if (routes[rule.target][rule.selector].domain != Domain.INVALID) revert InvalidConfiguration();
            Domain existing = targetOwnerDomain[rule.target];
            if (existing != Domain.INVALID && existing != rule.ownerDomain) revert InvalidConfiguration();
            if (_criticalCoreSelector(rule.selector)) { rule.domain = Domain.CORE; rule.tier = Tier.CRITICAL; }
            routes[rule.target][rule.selector] = Policy(rule.domain, rule.tier);
            targetOwnerDomain[rule.target] = rule.ownerDomain;
        }
    }

    /// OZ Pending uses snapshot>=clock; an offset avoids its zero sentinel and a
    /// delay of0 yields exactly the following full real epoch, not E+2.
    function clock() public view override returns (uint48) { return SafeCast.toUint48(miningCredential.currentEpoch() + 1); }
    function CLOCK_MODE() public pure override returns (string memory) { return "mode=epoch&offset=1"; }
    function votingDelay() public pure override returns (uint256) { return 0; }
    function votingPeriod() public pure override returns (uint256) { return 1; }
    function COUNTING_MODE() public pure override returns (string memory) { return "support=bravo&quorum=custom&params=chamber"; }
    /// A single scalar quorum would misrepresent a two-electorate proposal.
    function quorum(uint256) public pure override returns (uint256) { revert UseChamberQuorum(); }
    function hasVoted(uint256 id, address who) public view override returns (bool) {
        return _ballots[id][Domain.MINING][who].cast || _ballots[id][Domain.STAKING][who].cast;
    }
    function chamberVotes(uint256 id, Domain chamber) external view returns (Tally memory) { return _tallies[id][chamber]; }

    function propose(address[] memory, uint256[] memory, bytes[] memory, string memory) public pure override returns (uint256) {
        revert DepositRequired();
    }
    function proposeWithDeposit(address[] memory targets, uint256[] memory values, bytes[] memory data, string memory description)
        external payable returns (uint256 id) {
        if (msg.value != proposalBond) revert DepositRequired();
        Policy memory policy = _classify(targets, values, data);
        if ((policy.domain != Domain.STAKING && !miningCredential.votingReady())
            || (policy.domain != Domain.MINING && !stakingCredential.votingReady())
            || miningCredential.currentEpoch() != stakingCredential.currentEpoch()) revert VotingUnavailable();
        // The previous completed work epoch funds current voting credentials.
        uint256 epoch = miningCredential.currentEpoch();
        bool miner = policy.domain != Domain.STAKING && _eligibleProposer(miningCredential, epoch);
        bool staker = policy.domain != Domain.MINING && _eligibleProposer(stakingCredential, epoch);
        if (!miner && !staker) revert VotingUnavailable();
        id = super.propose(targets, values, data, description);
        proposalPolicy[id] = policy;
        proposalPolicyVersion[id] = policyModule.versionAt(epoch);
        refundableBond[id] = msg.value;
        _afterProposal(id);
    }
    function _afterProposal(uint256) internal virtual {}
    function _proposalDelay(uint256 id) internal view virtual returns (uint256) {
        return proposalPolicy[id].tier == Tier.CRITICAL ? criticalDelay : ordinaryDelay;
    }
    function _proposalGrace(uint256) internal view virtual returns (uint256) { return 2 * uint256(epochSeconds); }
    function _proposalVotingEnd(uint256 id) internal view virtual returns (uint256) {
        return miningCredential.genesisTime() + (proposalSnapshot(id) + 1) * epochSeconds;
    }
    function _actionClock() internal view virtual returns (uint256) { return block.timestamp; }
    function _proposalState(uint256 id) internal view virtual returns (ProposalState) { return Governor.state(id); }
    function _eligibleProposer(IGovernanceCredential source, uint256 epoch) private view returns (bool) {
        uint256 total = source.totalSupplyAt(epoch);
        return total != 0 && total <= type(uint120).max && source.getVotes(msg.sender, epoch, "") * 1000 >= total;
    }
    function _classify(address[] memory targets, uint256[] memory values, bytes[] memory data) private view returns (Policy memory result) {
        if (targets.length == 0 || targets.length != values.length || targets.length != data.length) revert InvalidConfiguration();
        bool stakingSeen;
        for (uint256 i; i < targets.length; ++i) {
            if (targetOwnerDomain[targets[i]] == Domain.STAKING) stakingSeen = true;
            else if (stakingSeen) revert InvalidConfiguration();
            bytes4 selector = data[i].length >= 4 ? bytes4(data[i]) : bytes4(0);
            Policy memory rule = routes[targets[i]][selector];
            if (rule.domain == Domain.INVALID || values[i] != 0) revert UnsupportedAction(targets[i], selector);
            if (result.domain == Domain.INVALID) result.domain = rule.domain;
            else if (result.domain != rule.domain) result.domain = Domain.CORE;
            if (rule.tier > result.tier) result.tier = rule.tier;
        }
        // Mixed-domain batches need both chambers, but only constitutionally
        // sensitive actions are escalated to the critical tier.
    }
    function _criticalCoreSelector(bytes4 s) private pure returns (bool) {
        return s == bytes4(keccak256("upgradeToAndCall(address,bytes)")) || s == bytes4(keccak256("upgradeTo(address)"))
            || s == bytes4(keccak256("transferOwnership(address)")) || s == bytes4(keccak256("acceptOwnership()"))
            || s == bytes4(keccak256("grantRole(bytes32,address)")) || s == bytes4(keccak256("revokeRole(bytes32,address)"))
            || s == bytes4(keccak256("renounceRole(bytes32,address)")) || s == bytes4(keccak256("changeAdmin(address)"))
            || s == bytes4(keccak256("announceRelease(uint256,string,string)"))
            || s == bytes4(keccak256("setBlocked(address,bool)"))
            || s == bytes4(keccak256("setBlockedBatch(address[],bool)"))
            || s == bytes4(keccak256("setRestricted(address,bool)"))
            || s == bytes4(keccak256("setRestrictedBatch(address[],bool)"))
            || s == bytes4(keccak256("setAllowedTo(address,address,bool)"))
            || s == bytes4(keccak256("setAllowedToBatch(address,address[],bool)"))
            || s == bytes4(keccak256("setStakingBlockedUntil(address,uint256)"))
            || s == bytes4(keccak256("pause()"))
            || s == bytes4(keccak256("unpause()"))
            || s == bytes4(keccak256("vetoMilestone()"))
            || s == bytes4(keccak256("resolveDelivery(bytes32,bool,bytes32)"));
    }

    function _getVotes(address who, uint256 credentialEpoch, bytes memory params) internal view override returns (uint256) {
        (Domain chamber, bytes memory inner) = abi.decode(params, (Domain, bytes));
        IGovernanceCredential source = _source(chamber);
        uint256 total = source.totalSupplyAt(credentialEpoch);
        _checkSupply(total);
        return source.getVotes(who, credentialEpoch, inner);
    }
    function _source(Domain chamber) private view returns (IGovernanceCredential) {
        if (chamber == Domain.MINING) return miningCredential;
        if (chamber == Domain.STAKING) return stakingCredential;
        revert InvalidBallot();
    }
    function _castVote(uint256 id, address who, uint8 support, string memory reason, bytes memory params)
        internal override returns (uint256 weight) {
        _validateStateBitmap(id, _encodeStateBitmap(ProposalState.Active));
        if (support > 2) revert InvalidBallot();
        if (params.length == 0) {
            Domain domain = proposalPolicy[id].domain;
            if (domain == Domain.CORE) revert InvalidBallot();
            params = abi.encode(domain, bytes(""));
        }
        _freeze(id);
        weight = _getVotes(who, proposalSnapshot(id), params);
        weight = _countVote(id, who, support, weight, params);
        emit VoteCastWithParams(who, id, support, weight, reason, params);
    }
    function _freeze(uint256 id) private {
        Policy memory policy = proposalPolicy[id];
        if (_tallies[id][Domain.MINING].total != 0 || _tallies[id][Domain.STAKING].total != 0) return;
        uint256 epoch = proposalSnapshot(id);
        uint256 m; uint256 s;
        if (policy.domain != Domain.STAKING) { m = miningCredential.totalSupplyAt(epoch); _checkSupply(m); _tallies[id][Domain.MINING].total = m; }
        if (policy.domain != Domain.MINING) { s = stakingCredential.totalSupplyAt(epoch); _checkSupply(s); _tallies[id][Domain.STAKING].total = s; }
        emit DenominatorsFrozen(id, epoch, m, s);
    }
    function _checkSupply(uint256 total) private pure { if (total == 0 || total > type(uint120).max) revert SupplyOutOfBounds(); }
    function _countVote(uint256 id, address who, uint8 support, uint256 weight, bytes memory params) internal override returns (uint256) {
        (Domain chamber,) = abi.decode(params, (Domain, bytes));
        Domain domain = proposalPolicy[id].domain;
        if ((chamber != Domain.MINING && chamber != Domain.STAKING) || (domain != Domain.CORE && domain != chamber) || weight == 0) revert InvalidBallot();
        Tally storage tally = _tallies[id][chamber];
        Ballot storage previous = _ballots[id][chamber][who];
        if (previous.cast) {
            // Proof/pool omissions cannot reduce a previously authenticated voting weight.
            if (weight < previous.weight) weight = previous.weight;
            _subtract(tally, previous.support, previous.weight);
        } else { ++tally.voters; }
        previous.cast = true; previous.weight = weight; previous.support = support;
        if (support == 0) tally.againstVotes += weight;
        else if (support == 1) tally.forVotes += weight;
        else tally.abstainVotes += weight;
        if (_participation(tally) > tally.total) revert SupplyOutOfBounds();
        return weight;
    }
    function _subtract(Tally storage tally, uint8 support, uint256 weight) private {
        if (support == 0) tally.againstVotes -= weight;
        else if (support == 1) tally.forVotes -= weight;
        else tally.abstainVotes -= weight;
    }
    function _participation(Tally memory tally) private pure returns (uint256) { return tally.againstVotes + tally.forVotes + tally.abstainVotes; }
    function _chamberQuorum(uint256 id, Domain chamber) private view returns (bool) {
        Tally memory tally = _tallies[id][chamber];
        return policyModule.quorumReached(
            proposalPolicyVersion[id], uint8(proposalPolicy[id].tier), uint8(chamber), _participation(tally), tally.total
        );
    }
    function _quorumReached(uint256 id) internal view override returns (bool) {
        Domain domain = proposalPolicy[id].domain;
        return (domain == Domain.STAKING || _chamberQuorum(id, Domain.MINING))
            && (domain == Domain.MINING || _chamberQuorum(id, Domain.STAKING));
    }
    function _voteSucceeded(uint256 id) internal view override returns (bool) {
        Policy memory policy = proposalPolicy[id];
        Tally memory m = _tallies[id][Domain.MINING];
        Tally memory s = _tallies[id][Domain.STAKING];
        if (policy.domain == Domain.CORE) {
            uint256 miningCast = _participation(m);
            uint256 stakingCast = _participation(s);
            return policyModule.coreSucceeded(
                proposalPolicyVersion[id], uint8(policy.tier), m.forVotes, miningCast, s.forVotes, stakingCast
            );
        }
        Tally memory selected = policy.domain == Domain.MINING ? m : s;
        return policyModule.specialistSucceeded(
            proposalPolicyVersion[id], uint8(policy.tier), selected.forVotes, _participation(selected)
        );
    }

    /// Overrides the OZ single-timelock bookkeeping because both owner domains must
    /// queue, execute or cancel as one approved proposal. Voting/nonce/state core stays OZ.
    function _partition(address[] memory targets, uint256[] memory values, bytes[] memory data, Domain domain) private view returns (Batch memory batch) {
        uint256 count;
        for (uint256 i; i < targets.length; ++i) if (targetOwnerDomain[targets[i]] == domain) ++count;
        batch.targets = new address[](count); batch.values = new uint256[](count); batch.data = new bytes[](count);
        count = 0;
        for (uint256 i; i < targets.length; ++i) if (targetOwnerDomain[targets[i]] == domain) {
            batch.targets[count] = targets[i]; batch.values[count] = values[i]; batch.data[count++] = data[i];
        }
    }
    function _salt(bytes32 descriptionHash) private view returns (bytes32) { return bytes20(address(this)) ^ descriptionHash; }
    function _queueOperations(uint256 id, address[] memory targets, uint256[] memory values, bytes[] memory data, bytes32 descriptionHash)
        internal override(Governor, GovernorTimelockControl) returns (uint48) {
        Policy memory verified = _classify(targets, values, data);
        if (verified.domain != proposalPolicy[id].domain || verified.tier != proposalPolicy[id].tier) revert InvalidConfiguration();
        uint256 delay = _proposalDelay(id);
        // The legacy clock is block.timestamp, leaving its behavior unchanged.
        // A versioned native floor must not shorten the agreed delay when an
        // incoming block timestamp temporarily moves backwards.
        uint256 now_ = _actionClock();
        if (now_ > block.timestamp) delay += now_ - block.timestamp;
        Batch memory m = _partition(targets, values, data, Domain.MINING);
        Batch memory s = _partition(targets, values, data, Domain.STAKING);
        operationIds[id] = Operations(_schedule(miningTimelock, m, descriptionHash, delay), _schedule(stakingTimelock, s, descriptionHash, delay));
        return SafeCast.toUint48(block.timestamp + delay);
    }
    function _schedule(TimelockController lock, Batch memory batch, bytes32 hash, uint256 delay) private returns (bytes32 id) {
        if (batch.targets.length == 0) return bytes32(0);
        id = lock.hashOperationBatch(batch.targets, batch.values, batch.data, 0, _salt(hash));
        lock.scheduleBatch(batch.targets, batch.values, batch.data, 0, _salt(hash), delay);
    }
    function state(uint256 id) public view override(Governor, GovernorTimelockControl) returns (ProposalState) {
        ProposalState result = _proposalState(id);
        if (result == ProposalState.Queued) {
            Operations memory op = operationIds[id];
            if ((op.mining != 0 && !miningTimelock.isOperationPending(op.mining)) || (op.staking != 0 && !stakingTimelock.isOperationPending(op.staking))) return ProposalState.Canceled;
            if (_actionClock() > proposalEta(id) + _proposalGrace(id)) return ProposalState.Expired;
        } else if (result == ProposalState.Succeeded) {
            if (_actionClock() > _proposalVotingEnd(id) + _proposalGrace(id)) return ProposalState.Expired;
        }
        return result;
    }
    function _executeOperations(uint256 id, address[] memory targets, uint256[] memory values, bytes[] memory data, bytes32 hash)
        internal override(Governor, GovernorTimelockControl) {
        if (msg.value != 0) revert InvalidConfiguration();
        if (proposalPolicy[id].domain == Domain.CORE) executingCoreProposal = id;
        Batch memory m = _partition(targets, values, data, Domain.MINING);
        Batch memory s = _partition(targets, values, data, Domain.STAKING);
        if (m.targets.length != 0) miningTimelock.executeBatch(m.targets, m.values, m.data, 0, _salt(hash));
        if (s.targets.length != 0) stakingTimelock.executeBatch(s.targets, s.values, s.data, 0, _salt(hash));
        executingCoreProposal = 0;
        delete operationIds[id];
    }
    function _cancel(address[] memory targets, uint256[] memory values, bytes[] memory data, bytes32 hash)
        internal override(Governor, GovernorTimelockControl) returns (uint256 id) {
        id = Governor._cancel(targets, values, data, hash);
        Operations memory op = operationIds[id];
        if (op.mining != 0) miningTimelock.cancel(op.mining);
        if (op.staking != 0) stakingTimelock.cancel(op.staking);
        delete operationIds[id];
    }
    function withdrawProposalBond(uint256 id) external {
        ProposalState status = state(id);
        if (proposalProposer(id) != msg.sender || (status != ProposalState.Executed && status != ProposalState.Canceled
            && status != ProposalState.Defeated && status != ProposalState.Expired)) revert RefundUnavailable();
        uint256 value = refundableBond[id];
        if (value == 0) revert RefundUnavailable();
        refundableBond[id] = 0;
        (bool ok,) = msg.sender.call{value:value}("");
        if (!ok) revert RefundFailed();
        emit BondRefunded(id, msg.sender, value);
    }
    function proposalNeedsQueuing(uint256) public pure override(Governor, GovernorTimelockControl) returns (bool) { return true; }
    function _executor() internal view override(Governor, GovernorTimelockControl) returns (address) { return address(miningTimelock); }
    function updateTimelock(TimelockController) external pure override { revert AuthorityFrozen(); }
    function relay(address, uint256, bytes calldata) external payable override { revert AuthorityFrozen(); }
}
