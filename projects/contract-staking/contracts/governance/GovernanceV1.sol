// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

/**
 * @title GovernanceV1
 * @notice Production-ready governance with council voting system
 * @dev Fixed: Council voting with For/Against/Abstain support
 */
contract GovernanceV1 is
    Initializable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable,
    PausableUpgradeable,
    UUPSUpgradeable
{
    using SafeERC20 for IERC20;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    // Roles
    bytes32 public constant ADMIN_ROLE = keccak256("ADMIN_ROLE");
    bytes32 public constant PROPOSER_ROLE = keccak256("PROPOSER_ROLE");
    bytes32 public constant EXECUTOR_ROLE = keccak256("EXECUTOR_ROLE");

    // Constants
    uint256 public constant MIN_VOTING_PERIOD = 1 minutes;
    uint256 public constant MAX_VOTING_PERIOD = 15 days;
    uint256 public constant MIN_PROPOSAL_THRESHOLD = 1 ether;
    uint256 public constant SLASH_PERCENTAGE = 50;
    uint256 public constant MAX_DESCRIPTION_LENGTH = 256;
    uint256 public constant COUNCIL_EXECUTION_QUORUM_BPS = 6000; // 60%
    uint256 public constant MIN_COUNCIL_VOTING_WINDOW = 1 minutes;
    uint256 public constant MAX_COUNCIL_VOTING_WINDOW = 7 days;
    uint256 public constant MAX_COUNCIL_SIZE = 10;

    // Core state
    uint256 public proposalCount;
    uint256 public votingPeriod;
    uint256 public threshold;
    uint256 public quorumVotes;
    uint256 public councilVotingWindow;
    address public treasury;
    uint256 public activeCouncilVotingCount;
    IERC20 public stakingToken;

    // Council Management
    address[] public councilMembers;
    mapping(address => bool) public isCouncilMember;

    // Council execution approval tracking
    mapping(uint256 => uint256) public councilExecutionVoteCount; // For votes
    mapping(uint256 => uint256) public councilAgainstVoteCount; // Against votes
    mapping(uint256 => uint256) public councilAbstainVoteCount; // Abstain votes
    mapping(uint256 => uint256) public councilVotingStartTime;
    mapping(uint256 => uint256) public councilVotingEndTime;

    enum ProposalState {
        Pending,
        Active,
        Succeeded,
        Failed,
        Canceled,
        Executed,
        CouncilVoting
    }
    enum VoteType {
        Against,
        For
    }
    enum CouncilType {
        Against,
        For,
        Abstain
    }

    struct Proposal {
        address proposer;
        string link;
        string sha256hash;
        uint256 startTime;
        uint256 endTime;
        uint256 forVotes;
        uint256 againstVotes;
        bool executed;
        bool canceled;
        mapping(address => VoteInfo) votes;
        mapping(address => CouncilVoteInfo) councilVotes;
        uint256 proposalVotingPeriod;
        uint256 proposalThreshold;
        uint256 proposalQuorumVotes;
        address proposalTreasury;
    }

    struct VoteInfo {
        bool hasVoted;
        VoteType vote;
        uint256 weight;
    }

    struct CouncilVoteInfo {
        bool hasVoted;
        CouncilType vote;
    }

    struct ProposalView {
        uint256 id;
        address proposer;
        string link;
        uint256 startTime;
        uint256 endTime;
        uint256 forVotes;
        uint256 againstVotes;
        bool executed;
        bool canceled;
        ProposalState state;
        uint256 councilForVotes;
        uint256 councilAgainstVotes;
        uint256 councilAbstainVotes;
    }

    struct ProposalSettings {
        uint256 proposalVotingPeriod;
        uint256 proposalThreshold;
        uint256 proposalQuorumVotes;
        address proposalTreasury;
        uint256 councilVotingDeadline;
    }

    mapping(uint256 => Proposal) internal proposals;
    mapping(uint256 => uint256) public proposerDeposits;
    mapping(uint256 => address[]) private votersList;
    mapping(uint256 => mapping(address => bool)) private inVotersList;

    // Events
    event ProposalCreated(
        uint256 indexed proposalId,
        address indexed proposer,
        string link,
        string indexed sha256Hash
    );
    event VoteCast(
        uint256 indexed proposalId,
        address indexed voter,
        VoteType vote,
        uint256 weight
    );

    event ProposalExecuted(
        uint256 indexed proposalId,
        string indexed link,
        string indexed sha256Hash
    );
    event ProposalCanceled(uint256 indexed proposalId);

    event CouncilVotingStarted(uint256 indexed proposalId, uint256 deadline);
    event CouncilExecutionVote(
        uint256 indexed proposalId,
        address indexed councilMember,
        CouncilType vote
    );
    event CouncilMemberAdded(address indexed member);
    event CouncilMemberRemoved(address indexed member);

    event StakeWithdrawn(
        uint256 indexed proposalId,
        address indexed voter,
        uint256 amount
    );
    event ProposerDepositWithdrawn(
        uint256 indexed proposalId,
        address indexed proposer,
        uint256 amount
    );

    event CouncilVotingWindowUpdated(uint256 oldWindow, uint256 newWindow);
    event VotingPeriodUpdated(uint256 oldPeriod, uint256 newPeriod);
    event QuorumVotesUpdated(uint256 oldQuorumVotes, uint256 newQuorumVotes);
    event ThresholdUpdated(uint256 oldThreshold, uint256 newThreshold);
    event TreasuryUpdated(
        address indexed oldTreasury,
        address indexed newTreasury
    );

    modifier validProposalId(uint256 proposalId) {
        require(
            proposalId > 0 && proposalId <= proposalCount,
            "Invalid proposal"
        );
        _;
    }

    modifier onlyCouncilMember() {
        require(isCouncilMember[msg.sender], "Not a council member");
        _;
    }

    // Update existing modifier
    modifier onlyCouncilOrAdmin() {
        require(
            isCouncilMember[msg.sender] || hasRole(ADMIN_ROLE, msg.sender),
            "Not council member or admin"
        );
        _;
    }

    function initialize(
        address _admin,
        uint256 _votingPeriod,
        uint256 _proposalThreshold,
        uint256 _quorumVotes,
        address _treasury,
        address _stakingToken
    ) external initializer {
        require(
            _admin != address(0) && _treasury != address(0),
            "Invalid addresses"
        );
        require(
            _votingPeriod >= MIN_VOTING_PERIOD &&
                _votingPeriod <= MAX_VOTING_PERIOD,
            "Invalid voting period"
        );
        require(
            _proposalThreshold >= MIN_PROPOSAL_THRESHOLD && _quorumVotes > 0,
            "Invalid parameters"
        );

        if (_stakingToken == address(0)) {
            require(
                _proposalThreshold >= MIN_PROPOSAL_THRESHOLD,
                "Invalid ETH proposal threshold"
            );
        } else {
            require(_proposalThreshold > 0, "Invalid token threshold");
        }

        // Validate token contract if provided
        if (_stakingToken != address(0)) {
            require(_stakingToken.code.length > 0, "Invalid token contract");
        }

        __AccessControl_init();
        __ReentrancyGuard_init();
        __Pausable_init();
        __UUPSUpgradeable_init();

        _grantRole(DEFAULT_ADMIN_ROLE, _admin);
        _grantRole(ADMIN_ROLE, _admin);
        _grantRole(PROPOSER_ROLE, _admin);
        _grantRole(EXECUTOR_ROLE, _admin);

        councilMembers.push(_admin);
        isCouncilMember[_admin] = true;

        votingPeriod = _votingPeriod;
        threshold = _proposalThreshold;
        quorumVotes = _quorumVotes;
        treasury = _treasury;
        councilVotingWindow = 2 days;
        activeCouncilVotingCount = 0;

        if (_stakingToken != address(0)) {
            stakingToken = IERC20(_stakingToken);
        }
    }

    // ==================== CORE GOVERNANCE FUNCTIONS ====================

    function propose(
        string calldata link,
        string calldata sha256hash,
        uint256 depositAmount
    )
        public
        payable
        virtual
        onlyRole(PROPOSER_ROLE)
        nonReentrant
        whenNotPaused
        returns (uint256)
    {
        require(bytes(link).length <= MAX_DESCRIPTION_LENGTH, "link too long");
        require(bytes(link).length > 0, "Empty link");
        require(bytes(sha256hash).length > 0, "Empty sha");
        require(bytes(sha256hash).length == 64, "Invalid SHA256 hash length");
        uint256 actualDeposit;

        if (address(stakingToken) == address(0)) {
            require(msg.value >= threshold, "Insufficient ETH");
            actualDeposit = msg.value;
        } else {
            require(msg.value == 0, "No ETH when using ERC20");
            require(depositAmount >= threshold, "Insufficient deposit");
            uint256 balanceBefore = stakingToken.balanceOf(address(this));
            stakingToken.safeTransferFrom(msg.sender, address(this), threshold);
            uint256 balanceAfter = stakingToken.balanceOf(address(this));

            actualDeposit = balanceAfter - balanceBefore;
            require(actualDeposit >= threshold, "Insufficient tokens received");
        }

        proposalCount++;
        uint256 proposalId = proposalCount;

        Proposal storage proposal = proposals[proposalId];
        proposal.proposer = msg.sender;
        proposal.link = link;
        proposal.sha256hash = sha256hash;
        proposal.startTime = block.timestamp;
        proposal.endTime = block.timestamp + votingPeriod;
        proposal.proposalVotingPeriod = votingPeriod;
        proposal.proposalThreshold = threshold;
        proposal.proposalQuorumVotes = quorumVotes;
        proposal.proposalTreasury = treasury;
        proposerDeposits[proposalId] = actualDeposit;

        emit ProposalCreated(proposalId, msg.sender, link, sha256hash);
        return proposalId;
    }

    function vote(
        uint256 proposalId,
        VoteType voteType,
        uint256 amount
    ) external payable validProposalId(proposalId) nonReentrant whenNotPaused {
        require(amount > 0, "Amount must be > 0");
        Proposal storage proposal = proposals[proposalId];
        require(
            getProposalState(proposalId) == ProposalState.Active,
            "Not active"
        );
        require(!proposal.votes[msg.sender].hasVoted, "Already voted");

        uint256 actualAmount;

        if (address(stakingToken) == address(0)) {
            require(msg.value == amount, "ETH amount mismatch");
            actualAmount = amount;
        } else {
            require(msg.value == 0, "No ETH when using ERC20");
            uint256 balanceBefore = stakingToken.balanceOf(address(this));
            stakingToken.safeTransferFrom(msg.sender, address(this), amount);
            uint256 balanceAfter = stakingToken.balanceOf(address(this));

            actualAmount = balanceAfter - balanceBefore;
            require(actualAmount > 0, "No tokens received");
        }

        proposal.votes[msg.sender] = VoteInfo({
            hasVoted: true,
            vote: voteType,
            weight: actualAmount
        });

        if (voteType == VoteType.For) {
            proposal.forVotes += actualAmount;
        } else {
            proposal.againstVotes += actualAmount;
        }

        if (!inVotersList[proposalId][msg.sender]) {
            votersList[proposalId].push(msg.sender);
            inVotersList[proposalId][msg.sender] = true;
        }

        emit VoteCast(proposalId, msg.sender, voteType, actualAmount);
    }

    function startCouncilVoting(
        uint256 proposalId
    ) external validProposalId(proposalId) onlyRole(ADMIN_ROLE) {
        require(
            getProposalState(proposalId) == ProposalState.Succeeded,
            "Proposal not succeeded"
        );
        require(
            councilVotingStartTime[proposalId] == 0,
            "Council voting already started"
        );
        require(
            councilMembers.length >= 3,
            "Minimum 3 council members required"
        );
        require(
            block.timestamp <= proposals[proposalId].endTime + 7 days,
            "Council voting window expired - too late to start"
        );
        councilVotingStartTime[proposalId] = block.timestamp;
        councilVotingEndTime[proposalId] =
            block.timestamp +
            councilVotingWindow;
        activeCouncilVotingCount++;
        emit CouncilVotingStarted(
            proposalId,
            block.timestamp + councilVotingWindow
        );
    }

    /**
     * @dev Council member votes with For/Against/Abstain options
     * @param proposalId The proposal ID
     * @param councilVote The vote type (Against=0, For=1, Abstain=2)
     */
    function councilVoteToExecute(
        uint256 proposalId,
        CouncilType councilVote
    ) external onlyCouncilMember validProposalId(proposalId) nonReentrant {
        Proposal storage proposal = proposals[proposalId];
        require(
            getProposalState(proposalId) == ProposalState.CouncilVoting,
            "Not in council voting phase"
        );
        require(
            !proposal.executed && !proposal.canceled,
            "Invalid proposal state"
        );
        require(!proposal.councilVotes[msg.sender].hasVoted, "Already voted");
        require(
            block.timestamp <= councilVotingEndTime[proposalId],
            "Council voting window expired"
        );

        proposal.councilVotes[msg.sender] = CouncilVoteInfo({
            hasVoted: true,
            vote: councilVote
        });
        if (councilVote == CouncilType.For) {
            councilExecutionVoteCount[proposalId] += 1;
        } else if (councilVote == CouncilType.Against) {
            councilAgainstVoteCount[proposalId] += 1;
        } else if (councilVote == CouncilType.Abstain) {
            councilAbstainVoteCount[proposalId] += 1;
        }

        emit CouncilExecutionVote(proposalId, msg.sender, councilVote);
    }

    /**
     * @dev Check if proposal can be executed
     * Logic: Only For/Against votes count toward quorum. Abstain votes are ignored.
     * Execution requires: For votes >= 60% of (For + Against) votes
     */
    function canExecute(
        uint256 proposalId
    ) public view validProposalId(proposalId) returns (bool) {
        if (getProposalState(proposalId) != ProposalState.CouncilVoting)
            return false;
        if (block.timestamp <= councilVotingEndTime[proposalId]) return false;

        uint256 forVotes = councilExecutionVoteCount[proposalId];
        uint256 againstVotes = councilAgainstVoteCount[proposalId];
        uint256 totalActiveVotes = forVotes + againstVotes; //  Abstain votes don't count

        // If no active votes (all abstained), cannot execute
        if (totalActiveVotes == 0) return false;

        // For votes must be >= 60% of active votes (For + Against)
        return
            forVotes * 10000 >= totalActiveVotes * COUNCIL_EXECUTION_QUORUM_BPS;
    }

    function execute(
        uint256 proposalId
    )
        external
        validProposalId(proposalId)
        onlyRole(EXECUTOR_ROLE)
        nonReentrant
    {
        Proposal storage proposal = proposals[proposalId];
        require(!proposal.executed && !proposal.canceled, "Invalid state");
        require(
            canExecute(proposalId),
            "Cannot execute - council voting incomplete or rejected"
        );

        // Decrement counter when proposal exits council voting
        if (councilVotingStartTime[proposalId] > 0) {
            activeCouncilVotingCount--;
        }

        proposal.executed = true;
        uint256 deposit = proposerDeposits[proposalId];
        if (deposit > 0) {
            proposerDeposits[proposalId] = 0;
            _transferFunds(proposal.proposer, deposit);
        }

        emit ProposalExecuted(proposalId, proposal.link, proposal.sha256hash);
    }

    function cancel(
        uint256 proposalId
    ) external validProposalId(proposalId) onlyCouncilOrAdmin nonReentrant {
        Proposal storage proposal = proposals[proposalId];
        require(!proposal.canceled && !proposal.executed, "Invalid state");

        ProposalState state = getProposalState(proposalId);

        // Check if cancellation is allowed based on different conditions
        bool canCancel = false;

        // 1. Proposal failed during normal voting
        if (state == ProposalState.Failed) {
            canCancel = true;
        }
        // 2. Proposal succeeded but council voting hasn't started within 7 days
        else if (state == ProposalState.Succeeded) {
            require(
                councilVotingStartTime[proposalId] == 0,
                "Council voting already started"
            );
            require(
                block.timestamp > proposal.endTime + 7 days,
                "Must wait 7 days after proposal ends before canceling"
            );
            canCancel = true;
        }

        // 3. Council voting period ended but proposal cannot be executed (failed quorum)
        else if (state == ProposalState.CouncilVoting) {
            require(
                block.timestamp > councilVotingEndTime[proposalId],
                "Council voting period not yet ended"
            );
            require(
                !canExecute(proposalId),
                "Proposal can be executed - cannot cancel"
            );
            canCancel = true;
        }

        require(canCancel, "Proposal cannot be canceled at this time");

        proposal.canceled = true;

        if (state == ProposalState.CouncilVoting) {
            activeCouncilVotingCount--;
        }

        uint256 deposit = proposerDeposits[proposalId];
        uint256 slashAmount = (deposit * SLASH_PERCENTAGE) / 100;
        uint256 refundAmount = deposit - slashAmount;

        proposerDeposits[proposalId] = 0;

        if (address(stakingToken) == address(0)) {
            // ETH mode
            if (slashAmount > 0) _transferFunds(treasury, slashAmount);
            if (refundAmount > 0)
                _transferFunds(proposal.proposer, refundAmount);
        } else {
            // ERC20 mode
            if (slashAmount > 0)
                stakingToken.safeTransfer(treasury, slashAmount);
            if (refundAmount > 0)
                stakingToken.safeTransfer(proposal.proposer, refundAmount);
        }

        emit ProposalCanceled(proposalId);
    }

    function withdrawStake(
        uint256 proposalId
    ) external nonReentrant validProposalId(proposalId) {
        Proposal storage proposal = proposals[proposalId];
        ProposalState state = getProposalState(proposalId);

        bool succeededTimeout = (state == ProposalState.Succeeded &&
            councilVotingStartTime[proposalId] == 0 &&
            block.timestamp > proposal.endTime + 7 days);

        require(
            state == ProposalState.Failed ||
                state == ProposalState.Canceled ||
                proposal.executed ||
                succeededTimeout ||
                (state == ProposalState.CouncilVoting &&
                    !canExecute(proposalId) &&
                    block.timestamp > councilVotingEndTime[proposalId]),
            "Cannot withdraw yet"
        );

        VoteInfo storage voteInfo = proposal.votes[msg.sender];
        require(
            voteInfo.hasVoted && voteInfo.weight > 0,
            "No stake to withdraw"
        );

        uint256 amount = voteInfo.weight;
        voteInfo.weight = 0;
        inVotersList[proposalId][msg.sender] = false;

        _transferFunds(msg.sender, amount);
        emit StakeWithdrawn(proposalId, msg.sender, amount);
    }

    // ==================== COUNCIL MANAGEMENT ====================

    function addCouncilMember(address member) external onlyRole(ADMIN_ROLE) {
        require(member != address(0), "Invalid member");
        require(!isCouncilMember[member], "Already council member");
        require(
            councilMembers.length < MAX_COUNCIL_SIZE,
            "Council size is too large"
        );

        councilMembers.push(member);
        isCouncilMember[member] = true;

        emit CouncilMemberAdded(member);
    }

    /**
     * @dev Remove council member - FIXED with active voting check
     */
    function removeCouncilMember(address member) external onlyRole(ADMIN_ROLE) {
        require(isCouncilMember[member], "Not a council member");
        require(
            councilMembers.length > 3,
            "Minimum 3 council members required"
        );
        // Check for active council voting using counter
        require(
            activeCouncilVotingCount == 0,
            "Cannot remove council member during active voting"
        );

        for (uint256 i = 0; i < councilMembers.length; i++) {
            if (councilMembers[i] == member) {
                councilMembers[i] = councilMembers[councilMembers.length - 1];
                councilMembers.pop();
                break;
            }
        }

        isCouncilMember[member] = false;
        emit CouncilMemberRemoved(member);
    }

    // ==================== VIEW FUNCTIONS ====================

    function getProposalState(
        uint256 proposalId
    ) public view validProposalId(proposalId) returns (ProposalState) {
        Proposal storage proposal = proposals[proposalId];

        if (proposal.canceled) return ProposalState.Canceled;
        if (proposal.executed) return ProposalState.Executed;
        if (block.timestamp < proposal.startTime) return ProposalState.Pending;
        if (block.timestamp <= proposal.endTime) return ProposalState.Active;

        uint256 totalVotes = proposal.forVotes + proposal.againstVotes;
        bool quorumMet = totalVotes >= proposal.proposalQuorumVotes;
        bool majorityFor = proposal.forVotes > proposal.againstVotes;

        if (quorumMet && majorityFor) {
            if (councilVotingStartTime[proposalId] > 0) {
                return ProposalState.CouncilVoting;
            } else {
                return ProposalState.Succeeded;
            }
        } else {
            return ProposalState.Failed;
        }
    }

    function getProposal(
        uint256 proposalId
    ) external view validProposalId(proposalId) returns (ProposalView memory) {
        Proposal storage proposal = proposals[proposalId];

        return
            ProposalView({
                id: proposalId,
                proposer: proposal.proposer,
                link: proposal.link,
                startTime: proposal.startTime,
                endTime: proposal.endTime,
                forVotes: proposal.forVotes,
                againstVotes: proposal.againstVotes,
                executed: proposal.executed,
                canceled: proposal.canceled,
                state: getProposalState(proposalId),
                councilForVotes: councilExecutionVoteCount[proposalId],
                councilAgainstVotes: councilAgainstVoteCount[proposalId],
                councilAbstainVotes: councilAbstainVoteCount[proposalId]
            });
    }

    /**
     * @dev Get proposal settings and configuration details
     * @param proposalId The proposal ID
     * @return settings The proposal settings including voting parameters and deadlines
     */
    function getProposalSettings(
        uint256 proposalId
    )
        external
        view
        validProposalId(proposalId)
        returns (ProposalSettings memory)
    {
        Proposal storage proposal = proposals[proposalId];

        uint256 councilDeadline = 0;
        if (councilVotingStartTime[proposalId] > 0) {
            councilDeadline = councilVotingEndTime[proposalId];
        }

        return
            ProposalSettings({
                proposalVotingPeriod: proposal.proposalVotingPeriod,
                proposalThreshold: proposal.proposalThreshold,
                proposalQuorumVotes: proposal.proposalQuorumVotes,
                proposalTreasury: proposal.proposalTreasury,
                councilVotingDeadline: councilDeadline
            });
    }

    function getCouncilMembers() external view returns (address[] memory) {
        return councilMembers;
    }

    /**
     * @dev Get council vote details for a proposal
     */
    function getCouncilVoteInfo(
        uint256 proposalId,
        address councilMember
    )
        external
        view
        validProposalId(proposalId)
        returns (bool hasVoted, CouncilType Councilvote)
    {
        Proposal storage proposal = proposals[proposalId];
        CouncilVoteInfo storage voteInfo = proposal.councilVotes[councilMember];
        return (voteInfo.hasVoted, voteInfo.vote);
    }

    /**
     * @dev Check if there's any active council voting
     */
    function hasActiveCouncilVoting() external view returns (bool) {
        return activeCouncilVotingCount > 0;
    }

    // ==================== ADMIN FUNCTIONS ====================
    /**
     * @dev Update multiple governance parameters at once (admin only)
     * @param newVotingPeriod New voting period (0 to skip)
     * @param newThreshold New threshold (0 to skip)
     * @param newQuorumVotes New quorum votes (0 to skip)
     * @param newCouncilVotingWindow New council voting window (0 to skip)
     * @param newTreasury New treasury address (address(0) to skip)
     */
    function updateGovernanceParameters(
        uint256 newVotingPeriod,
        uint256 newThreshold,
        uint256 newQuorumVotes,
        uint256 newCouncilVotingWindow,
        address newTreasury
    ) external onlyRole(ADMIN_ROLE) {
        // Update voting period if provided
        if (newVotingPeriod > 0 && newVotingPeriod != votingPeriod) {
            require(
                newVotingPeriod >= MIN_VOTING_PERIOD &&
                    newVotingPeriod <= MAX_VOTING_PERIOD,
                "Invalid voting period"
            );
            uint256 oldPeriod = votingPeriod;
            votingPeriod = newVotingPeriod;
            emit VotingPeriodUpdated(oldPeriod, newVotingPeriod);
        }

        // Update threshold if provided
        if (newThreshold > 0 && newThreshold != threshold) {
            if (address(stakingToken) == address(0)) {
                require(
                    newThreshold >= MIN_PROPOSAL_THRESHOLD,
                    "Threshold too low for ETH mode"
                );
            } else {
                require(newThreshold > 0, "Invalid token threshold");
            }
            uint256 oldThreshold = threshold;
            threshold = newThreshold;
            emit ThresholdUpdated(oldThreshold, newThreshold);
        }

        // Update quorum votes if provided
        if (newQuorumVotes > 0 && newQuorumVotes != quorumVotes) {
            uint256 oldQuorumVotes = quorumVotes;
            quorumVotes = newQuorumVotes;
            emit QuorumVotesUpdated(oldQuorumVotes, newQuorumVotes);
        }

        // Update council voting window if provided
        if (
            newCouncilVotingWindow > 0 &&
            newCouncilVotingWindow != councilVotingWindow
        ) {
            require(
                newCouncilVotingWindow >= MIN_COUNCIL_VOTING_WINDOW &&
                    newCouncilVotingWindow <= MAX_COUNCIL_VOTING_WINDOW,
                "Invalid council voting window"
            );
            uint256 oldWindow = councilVotingWindow;
            councilVotingWindow = newCouncilVotingWindow;
            emit CouncilVotingWindowUpdated(oldWindow, newCouncilVotingWindow);
        }

        // Update treasury if provided
        if (newTreasury != address(0) && newTreasury != treasury) {
            address oldTreasury = treasury;
            treasury = newTreasury;
            emit TreasuryUpdated(oldTreasury, newTreasury);
        }
    }

    function setStakingToken(address newToken) external onlyRole(ADMIN_ROLE) {
        require(newToken != address(0), "Invalid token");
        require(proposalCount == 0, "Cannot change after proposals started");
        stakingToken = IERC20(newToken);
    }

    function pause() external onlyCouncilOrAdmin {
        _pause();
    }

    function unpause() external onlyRole(ADMIN_ROLE) {
        _unpause();
    }

    function _transferFunds(address to, uint256 amount) private {
        if (address(stakingToken) == address(0)) {
            (bool success, ) = payable(to).call{value: amount}("");
            require(success, "ETH transfer failed");
        } else {
            stakingToken.safeTransfer(to, amount);
        }
    }

    function withdrawProposerDeposit(
        uint256 proposalId
    ) external validProposalId(proposalId) nonReentrant {
        Proposal storage proposal = proposals[proposalId];
        require(proposal.proposer == msg.sender, "Not proposer");

        ProposalState state = getProposalState(proposalId);
        require(
            state == ProposalState.Failed ||
                (state == ProposalState.Succeeded &&
                    councilVotingStartTime[proposalId] == 0 &&
                    block.timestamp > proposal.endTime + 7 days),
            "Cannot withdraw deposit yet"
        );

        uint256 deposit = proposerDeposits[proposalId];
        require(deposit > 0, "No deposit to withdraw");

        proposerDeposits[proposalId] = 0;
        _transferFunds(proposal.proposer, deposit);

        emit ProposerDepositWithdrawn(proposalId, proposal.proposer, deposit);
    }

    function _authorizeUpgrade(
        address newImplementation
    ) internal view virtual override onlyRole(ADMIN_ROLE) {
        require(newImplementation != address(0), "Invalid implementation");
    }

    receive() external payable {
        require(
            address(stakingToken) == address(0),
            "Contract not accepting ETH"
        );
    }

    uint256[100] private __gap;
}
