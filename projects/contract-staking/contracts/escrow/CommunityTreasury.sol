// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
interface ITreasuryClock { function currentEpoch() external view returns (uint256); }
interface ITreasuryGovernor { function executingCoreProposal() external view returns (uint256); }
/// @notice Donation-funded treasury, entirely separate from the staking reward vault.
/// This first version requires CORE execution because the current coordinator only
/// exposes a trustworthy execution id for CORE. Per-call ids would permit cap bypass.
contract CommunityTreasury is ReentrancyGuard {
    address public immutable owner;
    ITreasuryGovernor public immutable governor;
    ITreasuryClock public immutable clock;
    mapping(uint256 => uint256) public epochBudget;
    mapping(uint256 => bool) public epochOpened;
    mapping(uint256 => uint256) public epochSpent;
    mapping(uint256 => uint256) public proposalSpent;
    mapping(uint256 => uint256) public proposalBudget;
    mapping(uint256 => bool) public proposalOpened;
    event Donation(address indexed donor, uint256 amount);
    event Spent(uint256 indexed proposalId, uint256 indexed epoch, address indexed recipient, uint256 amount);
    constructor(address timelock, address governor_, address clock_) {
        require(timelock != address(0) && governor_.code.length > 0 && clock_.code.length > 0, "configuration");
        owner = timelock; governor = ITreasuryGovernor(governor_); clock = ITreasuryClock(clock_);
    }
    receive() external payable { emit Donation(msg.sender, msg.value); }
    function spend(address recipient, uint256 amount) external nonReentrant {
        require(msg.sender == owner, "governance");
        uint256 id = governor.executingCoreProposal(); require(id != 0, "CORE execution");
        require(recipient != address(0) && recipient != address(this) && amount > 0, "payment");
        uint256 epoch = clock.currentEpoch();
        if (!epochOpened[epoch]) { epochOpened[epoch] = true; epochBudget[epoch] = address(this).balance; }
        if (!proposalOpened[id]) { proposalOpened[id] = true; proposalBudget[id] = epochBudget[epoch]; }
        require(proposalSpent[id] + amount <= proposalBudget[id] / 100, "proposal cap");
        require(epochSpent[epoch] + amount <= epochBudget[epoch] / 20, "epoch cap");
        proposalSpent[id] += amount; epochSpent[epoch] += amount;
        emit Spent(id, epoch, recipient, amount);
        (bool ok,) = recipient.call{value: amount}(""); require(ok, "payment failed");
    }
}
