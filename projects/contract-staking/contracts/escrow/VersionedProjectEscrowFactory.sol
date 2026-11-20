// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Clones} from "@openzeppelin/contracts/proxy/Clones.sol";
import {ProjectEscrow} from "./ProjectEscrow.sol";
import {VersionedProjectEscrow} from "./VersionedProjectEscrow.sol";
import {ApprovedDevRegistry} from "./ApprovedDevRegistry.sol";
import {IVersionedEpochClock} from "../credentials/IVersionedEpochClock.sol";
import {VersionedGovernanceTiming} from "../governance/phase1/VersionedGovernanceTiming.sol";

/// @notice Uninstalled epoch-aware factory. Native migration must pin its
/// immutable addresses and keep projects in slot 0. Old clones are never edited.
contract VersionedProjectEscrowFactory {
    address public immutable governance;
    ApprovedDevRegistry public immutable registry;
    address public immutable implementation;
    IVersionedEpochClock public immutable clock;
    VersionedGovernanceTiming public immutable timing;
    address public immutable legacyImplementation;
    uint64 public immutable legacyChallengeSeconds;
    mapping(bytes32 => address) public projects;
    event ProjectCreated(bytes32 indexed projectId, address indexed escrow, address indexed developer, address payout, uint256 goal, uint64 deadline, uint8 mode);

    constructor(address owner, address registry_, IVersionedEpochClock clock_, VersionedGovernanceTiming timing_, address legacyImplementation_, uint64 legacyChallenge_) {
        require(owner != address(0) && registry_.code.length > 0
            && address(clock_).code.length > 0 && address(timing_).code.length > 0
            && address(timing_.clock()) == address(clock_) && legacyImplementation_.code.length > 0
            && legacyChallenge_ != 0, "configuration");
        governance = owner; registry = ApprovedDevRegistry(registry_);
        clock = clock_; timing = timing_;
        legacyImplementation = legacyImplementation_; legacyChallengeSeconds = legacyChallenge_;
        implementation = address(new VersionedProjectEscrow(clock_, timing_));
    }

    function challengeSeconds() public view returns (uint64) {
        uint256 duration = clock.epochStatus().duration;
        (, uint256 critical,) = timing.delaysForDuration(duration);
        uint256 challenge = 3 * duration + critical;
        require(challenge <= type(uint64).max, "challenge overflow");
        return uint64(challenge);
    }

    function createProject(bytes32 id, address developer, address payout, uint256 goal, uint64 deadline, uint8 mode, address[3] calldata committee, uint256[] calldata amounts) external returns (address escrow) {
        require(msg.sender == governance, "governance");
        require(timing.activeExecutionId() != 0, "execution context");
        require(id != bytes32(0) && projects[id] == address(0) && registry.isApproved(developer), "project/developer");
        bool oldPolicy = timing.legacyExecution();
        escrow = Clones.clone(oldPolicy ? legacyImplementation : implementation); projects[id] = escrow;
        ProjectEscrow.Configuration memory config;
        config.governance = governance; config.factory = address(this); config.developer = developer;
        config.payout = payout; config.goal = goal; config.deadline = deadline; config.mode = mode;
        config.committee = committee; config.amounts = amounts;
        config.challenge = oldPolicy ? legacyChallengeSeconds : challengeSeconds();
        ProjectEscrow(escrow).initialize(config);
        emit ProjectCreated(id, escrow, developer, payout, goal, deadline, mode);
    }

    function vetoProject(bytes32 id) external {
        require(msg.sender == governance && projects[id] != address(0), "governance/project");
        ProjectEscrow(projects[id]).vetoMilestone();
    }
}
