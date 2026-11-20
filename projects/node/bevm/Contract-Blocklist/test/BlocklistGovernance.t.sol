// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {BlocklistGovernance} from "../src/BlocklistGovernance.sol";

/// Minimal subset of the Foundry cheatcode interface so the test suite has no
/// external library dependencies.
interface Vm {
    function prank(address sender) external;
    function load(address target, bytes32 slot) external view returns (bytes32);
    function expectRevert(bytes calldata revertData) external;
}

contract BlocklistGovernanceTest {
    Vm constant vm = Vm(address(uint160(uint256(keccak256("hevm cheat code")))));

    address constant OWNER = address(0xA11CE);
    address constant OUTSIDER = address(0xBAD);
    address constant TARGET = address(0xDEAD);

    BlocklistGovernance gov;

    function setUp() public {
        vm.prank(OWNER);
        gov = new BlocklistGovernance();
    }

    function test_DeployerIsOwner() public view {
        require(gov.owner() == OWNER, "deployer must be owner");
    }

    function test_BlockAndUnblock() public {
        vm.prank(OWNER);
        gov.setBlocked(TARGET, true);
        require(gov.isBlocked(TARGET), "must be blocked");

        vm.prank(OWNER);
        gov.setBlocked(TARGET, false);
        require(!gov.isBlocked(TARGET), "must be unblocked");
    }

    function test_BlockBatch() public {
        address[] memory targets = new address[](2);
        targets[0] = TARGET;
        targets[1] = address(0xBEEF);

        vm.prank(OWNER);
        gov.setBlockedBatch(targets, true);
        require(gov.isBlocked(TARGET) && gov.isBlocked(address(0xBEEF)), "batch must block all");
    }

    /// The Go consensus code reads blocked[addr] straight from storage; this
    /// pins the exact slots it expects (see bevm/params/blocklist.go and
    /// blockedSlot() in bevm/bdag/blocklist.go).
    function test_ConsensusStorageLayout() public {
        vm.prank(OWNER);
        gov.setBlocked(TARGET, true);

        // slot 0: owner
        require(
            address(uint160(uint256(vm.load(address(gov), bytes32(0))))) == OWNER,
            "slot0 must be owner"
        );
        // slot 1 mapping: keccak256(abi.encode(addr, 1))
        bytes32 blockedSlot = keccak256(abi.encode(TARGET, uint256(1)));
        require(uint256(vm.load(address(gov), blockedSlot)) == 1, "blocked flag must live in slot-1 mapping");
    }

    function test_NonOwnerCannotBlock() public {
        vm.expectRevert(bytes("not owner"));
        vm.prank(OUTSIDER);
        gov.setBlocked(TARGET, true);
    }

    function test_TransferOwnership() public {
        address newOwner = address(0xCAFE);
        vm.prank(OWNER);
        gov.transferOwnership(newOwner);
        require(gov.owner() == newOwner, "ownership must transfer");

        vm.prank(newOwner);
        gov.setBlocked(TARGET, true);
        require(gov.isBlocked(TARGET), "new owner must be able to block");

        vm.expectRevert(bytes("not owner"));
        vm.prank(OWNER);
        gov.setBlocked(TARGET, false);
    }
}
