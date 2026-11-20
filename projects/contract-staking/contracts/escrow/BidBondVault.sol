// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {ApprovedDevRegistry} from "./ApprovedDevRegistry.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";
contract BidBondVault is ReentrancyGuard {
    enum Status { NONE, ACTIVE, ABANDONED, UNSELECTED, DELIVERED }
    struct Bond { address developer; uint256 amount; Status status; bytes32 evidence; }
    address public immutable governance;
    address public immutable treasury;
    ApprovedDevRegistry public immutable registry;
    mapping(bytes32 => Bond) public bonds;
    mapping(address => uint256) public credits;
    event BondDeposited(bytes32 indexed bidId, address indexed developer, uint256 quote, uint256 amount);
    event BondResolved(bytes32 indexed bidId, Status status, bytes32 indexed evidence);
    event CreditWithdrawn(address indexed recipient, uint256 amount);
    constructor(address owner, address registry_, address treasury_) {
        require(owner != address(0) && registry_.code.length > 0 && treasury_.code.length > 0, "configuration");
        governance = owner; registry = ApprovedDevRegistry(registry_); treasury = treasury_;
    }
    function deposit(bytes32 id, uint256 quote) external payable {
        require(registry.isApproved(msg.sender) && id != bytes32(0) && bonds[id].developer == address(0) && quote > 0, "bid");
        require(msg.value == Math.mulDiv(quote, 2, 100, Math.Rounding.Ceil), "2 percent bond");
        bonds[id] = Bond(msg.sender, msg.value, Status.ACTIVE, bytes32(0)); emit BondDeposited(id, msg.sender, quote, msg.value);
    }
    /// @notice A selected developer's bond remains locked until an evidenced DAO
    ///         delivery decision. Rejection/abandonment forfeits to CommunityTreasury.
    function resolveDelivery(bytes32 id, bool accepted, bytes32 evidence) external {
        require(msg.sender == governance && evidence != bytes32(0), "governance/evidence");
        Bond storage b = bonds[id];
        require(b.status == Status.ACTIVE, "bond");
        b.status = accepted ? Status.DELIVERED : Status.ABANDONED;
        b.evidence = evidence;
        credits[accepted ? b.developer : treasury] += b.amount;
        emit BondResolved(id, b.status, evidence);
    }
    /// @notice Once selection is final, unsuccessful bidders recover their bond.
    function resolveUnselected(bytes32 id, bytes32 evidence) external {
        require(msg.sender == governance && evidence != bytes32(0), "governance/evidence");
        Bond storage b = bonds[id];
        require(b.status == Status.ACTIVE, "bond");
        b.status = Status.UNSELECTED;
        b.evidence = evidence;
        credits[b.developer] += b.amount;
        emit BondResolved(id, Status.UNSELECTED, evidence);
    }
    function withdraw() external { withdrawFor(msg.sender); }
    function withdrawFor(address recipient) public nonReentrant {
        uint256 amount = credits[recipient]; require(amount > 0, "no credit"); credits[recipient] = 0;
        emit CreditWithdrawn(recipient, amount); (bool ok,) = recipient.call{value: amount}(""); require(ok, "payment failed");
    }
}
