// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {Initializable} from "@openzeppelin/contracts/proxy/utils/Initializable.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";

/// @notice Native-BDAG all-or-nothing funding. Lenient mode releases the goal to
/// the fixed payout wallet. Tracked mode releases reviewed, challengeable milestones.
contract ProjectEscrow is Initializable, ReentrancyGuard {
    address public governance;
    address public factory;
    address public developer;
    address public payout;
    uint64 public challengeSeconds;
    uint64 public deadline;
    uint256 public goal;
    uint256 public totalRaised;
    uint256 public released;
    uint256 public refunded;
    uint8 public mode;
    mapping(address => uint256) public contributions;
    mapping(address => bool) public refundClaimed;
    address[3] public committee;
    uint256[] public milestoneAmounts;
    uint256 public milestoneIndex;
    bytes32 public evidenceHash;
    uint64 public approvedAt;
    uint8 public approvals;
    uint64 public lastReleaseAt;
    bool public reclaiming;
    uint256 public refundPool;
    uint256 public refundedContributions;
    event Contributed(address indexed contributor, uint256 amount);
    event Refunded(address indexed contributor, uint256 amount);
    event Released(address indexed payout, uint256 amount, uint256 milestone);
    event MilestoneSubmitted(uint256 indexed index, bytes32 evidenceHash);
    event MilestoneApproved(uint256 indexed index, address indexed reviewer, uint8 approvals);
    event MilestoneVetoed(uint256 indexed index, bytes32 evidenceHash);
    event ReclaimActivated(uint256 refundable);
    constructor() { _disableInitializers(); }
    struct Configuration {
        address governance; address factory; address developer; address payout; uint256 goal; uint64 deadline;
        uint8 mode; address[3] committee; uint256[] amounts; uint64 challenge;
    }
    function initialize(Configuration calldata config) external initializer {
        _initializeTiming();
        require(config.governance != address(0) && config.factory.code.length > 0 && config.developer != address(0) && config.payout != address(0), "addresses");
        require(config.goal > 0 && config.deadline > block.timestamp && config.deadline <= block.timestamp + _maximumFundingWindow(), "funding");
        require(config.mode <= 1 && config.challenge >= _minimumChallenge(), "mode/challenge");
        governance = config.governance; factory = config.factory; developer = config.developer; payout = config.payout; goal = config.goal; deadline = config.deadline; mode = config.mode;
        challengeSeconds = config.challenge;
        if (config.mode == 0) require(config.amounts.length == 0, "lenient milestones");
        else {
            require(config.amounts.length > 0 && config.amounts.length <= 5, "milestones");
            uint256 sum;
            for (uint256 i; i < config.amounts.length; ++i) { require(config.amounts[i] > 0, "amount"); sum += config.amounts[i]; milestoneAmounts.push(config.amounts[i]); }
            require(sum == config.goal, "goal sum");
            for (uint256 i; i < 3; ++i) {
                require(config.committee[i] != address(0) && config.committee[i] != config.developer && config.committee[i] != config.payout, "conflict");
                for (uint256 j; j < i; ++j) require(config.committee[i] != config.committee[j], "duplicate reviewer");
            }
            committee = config.committee;
        }
    }

    function contribute() external payable nonReentrant {
        require(block.timestamp < deadline && !reclaiming && msg.value > 0 && totalRaised + msg.value <= goal, "funding closed/amount");
        contributions[msg.sender] += msg.value; totalRaised += msg.value; emit Contributed(msg.sender, msg.value);
    }
    function release() external nonReentrant {
        require(mode == 0 && totalRaised == goal && released == 0 && !reclaiming, "not releasable");
        released = goal; emit Released(payout, goal, 0); _pay(payout, goal);
    }
    function submitMilestone(bytes32 evidence) external {
        require(msg.sender == developer && mode == 1 && totalRaised == goal && !reclaiming, "developer/tracked");
        require(milestoneIndex < milestoneAmounts.length && evidenceHash == bytes32(0) && evidence != bytes32(0), "milestone");
        evidenceHash = evidence; approvals = 0; approvedAt = 0; emit MilestoneSubmitted(milestoneIndex, evidence);
    }
    function approveMilestone() external {
        require(evidenceHash != bytes32(0) && !reclaiming, "no pending milestone");
        uint8 bit;
        for (uint8 i; i < 3; ++i) if (committee[i] == msg.sender) bit = uint8(1 << i);
        require(bit != 0 && approvals & bit == 0, "reviewer/duplicate");
        approvals |= bit;
        // Start the challenge only when two different reviewers have approved.
        if (approvedAt == 0 && (approvals == 3 || approvals == 5 || approvals == 6 || approvals == 7)) approvedAt = uint64(block.timestamp);
        emit MilestoneApproved(milestoneIndex, msg.sender, approvals);
    }
    function vetoMilestone() external {
        require(msg.sender == governance || msg.sender == factory, "veto authority");
        require(evidenceHash != bytes32(0) && !reclaiming, "no milestone");
        emit MilestoneVetoed(milestoneIndex, evidenceHash); evidenceHash = bytes32(0); approvals = 0; approvedAt = 0;
    }
    function releaseMilestone() external nonReentrant {
        require(mode == 1 && !reclaiming && approvedAt != 0 && block.timestamp >= uint256(approvedAt) + challengeSeconds, "challenge");
        uint256 index = milestoneIndex++; uint256 amount = milestoneAmounts[index];
        evidenceHash = bytes32(0); approvals = 0; approvedAt = 0; released += amount; lastReleaseAt = uint64(block.timestamp);
        emit Released(payout, amount, index); _pay(payout, amount);
    }
    function activateReclaim() external {
        require(mode == 1 && totalRaised == goal && released < goal && !reclaiming, "not reclaimable");
        uint256 anchor = lastReleaseAt == 0 ? deadline : lastReleaseAt;
        require(block.timestamp >= anchor + _inactivityPeriod(), "inactivity period");
        reclaiming = true; refundPool = goal - released; emit ReclaimActivated(refundPool);
    }
    function refund() external { refundFor(msg.sender); }
    /// @notice Anyone can pay a refund to its original contributor, never themselves.
    function refundFor(address contributor) public nonReentrant {
        require((block.timestamp >= deadline && totalRaised < goal) || reclaiming, "not refundable");
        require(!refundClaimed[contributor] && contributions[contributor] > 0, "already/no contribution");
        uint256 amount = reclaiming ? Math.mulDiv(contributions[contributor], refundPool, totalRaised) : contributions[contributor];
        // The last contributor receives integer-division dust; no admin sweep exists.
        if (reclaiming && refundedContributions + contributions[contributor] == totalRaised) amount = refundPool - refunded;
        refundedContributions += contributions[contributor];
        refundClaimed[contributor] = true; refunded += amount; emit Refunded(contributor, amount); _pay(contributor, amount);
    }
    function _initializeTiming() internal virtual {}
    function _maximumFundingWindow() internal view virtual returns (uint256) { return 90 days; }
    function _minimumChallenge() internal view virtual returns (uint256) { return 28 days; }
    function _inactivityPeriod() internal view virtual returns (uint256) { return 180 days; }
    function _pay(address recipient, uint256 amount) private { (bool ok,) = recipient.call{value: amount}(""); require(ok, "payment failed"); }
}
