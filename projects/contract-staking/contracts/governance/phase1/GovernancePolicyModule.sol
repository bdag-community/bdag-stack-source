// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IEpochGovernorPolicyContext {
    function executingCoreProposal() external view returns (uint256);
    function proposalPolicy(uint256 id) external view returns (uint8 domain, uint8 tier);
    function clock() external view returns (uint48);
}

/// @notice Versioned, strengthening-only governance thresholds. Policy changes
/// are accepted only while the Governor executes a critical CORE proposal and
/// only take effect in a future epoch. Open proposals retain their version.
contract GovernancePolicyModule {
    struct RuleSet {
        uint16 miningOrdinaryTurnout;
        uint16 miningCriticalTurnout;
        uint16 stakingOrdinaryTurnout;
        uint16 stakingCriticalTurnout;
        uint16 ordinaryCoreChamberSupport;
        uint16 ordinaryCoreWeightedSupport;
        uint16 criticalCoreChamberSupport;
        uint16 criticalCoreWeightedSupport;
        uint16 specialistOrdinarySupport;
        uint16 specialistCriticalSupport;
    }

    address public immutable governor;
    address public immutable controller;
    uint32 public versionCount;
    mapping(uint32 => RuleSet) private _rules;
    mapping(uint32 => uint256) public effectiveEpoch;

    error Unauthorized();
    error InvalidPolicy();
    error TooManyVersions();
    event PolicyScheduled(uint32 indexed version, uint256 indexed effectiveEpoch, RuleSet rules);

    constructor(address governor_, address controller_) {
        if (governor_ == address(0) || controller_ == address(0)) revert InvalidPolicy();
        governor = governor_;
        controller = controller_;
        versionCount = 1;
        _rules[1] = RuleSet(15, 20, 10, 15, 40, 50, 50, 60, 50, 60);
        emit PolicyScheduled(1, 0, _rules[1]);
    }

    function getRules(uint32 version) external view returns (RuleSet memory) {
        if (version == 0 || version > versionCount) revert InvalidPolicy();
        return _rules[version];
    }

    function versionAt(uint256 epoch) public view returns (uint32) {
        uint32 count = versionCount;
        while (count > 1 && effectiveEpoch[count] > epoch) {
            unchecked { --count; }
        }
        return count;
    }

    function quorumReached(uint32 version, uint8 tier, uint8 chamber, uint256 participation, uint256 total)
        external view returns (bool) {
        RuleSet memory rules = _rules[version];
        if (version == 0 || version > versionCount || (chamber != 1 && chamber != 2) || tier == 0) revert InvalidPolicy();
        uint256 percentage = chamber == 1
            ? (tier == 2 ? rules.miningCriticalTurnout : rules.miningOrdinaryTurnout)
            : (tier == 2 ? rules.stakingCriticalTurnout : rules.stakingOrdinaryTurnout);
        return total != 0 && participation * 100 >= total * percentage;
    }

    function coreSucceeded(
        uint32 version, uint8 tier, uint256 miningFor, uint256 miningCast, uint256 stakingFor, uint256 stakingCast
    ) external view returns (bool) {
        RuleSet memory rules = _rules[version];
        if (version == 0 || version > versionCount || tier == 0 || miningCast == 0 || stakingCast == 0) return false;
        if (tier == 2) {
            if (miningFor * 100 <= miningCast * rules.criticalCoreChamberSupport
                || stakingFor * 100 <= stakingCast * rules.criticalCoreChamberSupport) return false;
        } else if (miningFor * 100 < miningCast * rules.ordinaryCoreChamberSupport
            || stakingFor * 100 < stakingCast * rules.ordinaryCoreChamberSupport) return false;
        uint256 score = 70 * miningFor * stakingCast + 30 * stakingFor * miningCast;
        uint256 base = miningCast * stakingCast;
        return tier == 2
            ? score >= uint256(rules.criticalCoreWeightedSupport) * base
            : score > uint256(rules.ordinaryCoreWeightedSupport) * base;
    }

    function specialistSucceeded(uint32 version, uint8 tier, uint256 forVotes, uint256 cast)
        external view returns (bool) {
        RuleSet memory rules = _rules[version];
        if (version == 0 || version > versionCount || tier == 0 || cast == 0) return false;
        return tier == 2
            ? forVotes * 100 >= cast * rules.specialistCriticalSupport
            : forVotes * 100 > cast * rules.specialistOrdinarySupport;
    }

    function schedulePolicy(RuleSet calldata next, uint256 startsAtEpoch) external {
        if (msg.sender != controller) revert Unauthorized();
        IEpochGovernorPolicyContext source = IEpochGovernorPolicyContext(governor);
        uint256 proposalId = source.executingCoreProposal();
        (uint8 domain, uint8 tier) = source.proposalPolicy(proposalId);
        if (proposalId == 0 || domain != 3 || tier != 2) revert Unauthorized();

        uint256 currentEpoch = uint256(source.clock()) - 1;
        uint32 count = versionCount;
        if (startsAtEpoch <= currentEpoch || startsAtEpoch <= effectiveEpoch[count]) revert InvalidPolicy();
        if (count == type(uint8).max) revert TooManyVersions();
        _validate(next);
        unchecked { ++count; }
        versionCount = count;
        effectiveEpoch[count] = startsAtEpoch;
        _rules[count] = next;
        emit PolicyScheduled(count, startsAtEpoch, next);
    }

    function _validate(RuleSet calldata next) private pure {
        if (next.miningOrdinaryTurnout < 15 || next.miningCriticalTurnout < 20
            || next.stakingOrdinaryTurnout < 10 || next.stakingCriticalTurnout < 15
            || next.ordinaryCoreChamberSupport < 40 || next.ordinaryCoreWeightedSupport < 50
            || next.criticalCoreChamberSupport < 50 || next.criticalCoreWeightedSupport < 60
            || next.specialistOrdinarySupport < 50 || next.specialistCriticalSupport < 60
            || next.miningCriticalTurnout < next.miningOrdinaryTurnout
            || next.stakingCriticalTurnout < next.stakingOrdinaryTurnout
            || next.criticalCoreChamberSupport < next.ordinaryCoreChamberSupport
            || next.criticalCoreWeightedSupport < next.ordinaryCoreWeightedSupport
            || next.specialistCriticalSupport < next.specialistOrdinarySupport
            || next.miningCriticalTurnout > 100 || next.stakingCriticalTurnout > 100
            || next.criticalCoreChamberSupport >= 100 || next.criticalCoreWeightedSupport > 100
            || next.specialistCriticalSupport > 100) revert InvalidPolicy();
    }
}
