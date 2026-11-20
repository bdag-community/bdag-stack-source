// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {Clones} from "@openzeppelin/contracts/proxy/Clones.sol";
import {ProjectEscrow} from "./ProjectEscrow.sol";
import {ApprovedDevRegistry} from "./ApprovedDevRegistry.sol";
contract ProjectEscrowFactory {
    address public immutable governance;
    ApprovedDevRegistry public immutable registry;
    address public immutable implementation;
    uint64 public immutable challengeSeconds;
    mapping(bytes32 => address) public projects;
    event ProjectCreated(bytes32 indexed projectId, address indexed escrow, address indexed developer, address payout, uint256 goal, uint64 deadline, uint8 mode);
    constructor(address owner, address registry_, uint64 challenge) {
        require(owner != address(0) && registry_.code.length > 0 && challenge >= 28 days, "configuration");
        governance = owner; registry = ApprovedDevRegistry(registry_); challengeSeconds = challenge;
        implementation = address(new ProjectEscrow());
    }
    function createProject(bytes32 id, address developer, address payout, uint256 goal, uint64 deadline, uint8 mode, address[3] calldata committee, uint256[] calldata amounts) external returns (address escrow) {
        require(msg.sender == governance, "governance");
        require(id != bytes32(0) && projects[id] == address(0) && registry.isApproved(developer), "project/developer");
        escrow = Clones.clone(implementation); projects[id] = escrow;
        ProjectEscrow.Configuration memory config;
        config.governance = governance; config.factory = address(this); config.developer = developer; config.payout = payout; config.goal = goal; config.deadline = deadline; config.mode = mode;
        config.committee = committee; config.amounts = amounts; config.challenge = challengeSeconds;
        ProjectEscrow(escrow).initialize(config);
        emit ProjectCreated(id, escrow, developer, payout, goal, deadline, mode);
    }
    /// @notice CORE-routed governance can veto an existing tracked milestone, but
    /// the factory cannot make arbitrary calls or move escrow funds.
    function vetoProject(bytes32 id) external {
        require(msg.sender == governance && projects[id] != address(0), "governance/project");
        ProjectEscrow(projects[id]).vetoMilestone();
    }
}
