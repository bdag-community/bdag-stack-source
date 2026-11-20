// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {SelectiveBlocklist} from "../src/SelectiveBlocklist.sol";

interface Vm {
    function prank(address sender) external;
    function load(address target, bytes32 slot) external view returns (bytes32);
    function expectRevert(bytes calldata revertData) external;
}

contract SelectiveBlocklistTest {
    Vm constant vm = Vm(address(uint160(uint256(keccak256("hevm cheat code")))));

    address constant OWNER    = address(0xA11CE);
    address constant OUTSIDER = address(0xBAD);
    address constant SENDER   = address(0xDEAD);
    address constant DEST     = address(0xBEEF);

    SelectiveBlocklist sel;

    function setUp() public {
        vm.prank(OWNER);
        sel = new SelectiveBlocklist();
    }

    function test_DeployerIsOwner() public view {
        require(sel.owner() == OWNER, "deployer must be owner");
    }

    function test_RestrictAndUnrestrict() public {
        vm.prank(OWNER);
        sel.setRestricted(SENDER, true);
        require(sel.isRestricted(SENDER), "must be restricted");

        vm.prank(OWNER);
        sel.setRestricted(SENDER, false);
        require(!sel.isRestricted(SENDER), "must be unrestricted");
    }

    function test_RestrictBatch() public {
        address[] memory accounts = new address[](2);
        accounts[0] = SENDER;
        accounts[1] = address(0xCAFE);

        vm.prank(OWNER);
        sel.setRestrictedBatch(accounts, true);
        require(sel.isRestricted(SENDER) && sel.isRestricted(address(0xCAFE)), "batch must restrict all");
    }

    function test_SetAllowedTo() public {
        vm.prank(OWNER);
        sel.setAllowedTo(SENDER, DEST, true);
        require(sel.isAllowedTo(SENDER, DEST), "must be allowed");

        vm.prank(OWNER);
        sel.setAllowedTo(SENDER, DEST, false);
        require(!sel.isAllowedTo(SENDER, DEST), "must be revoked");
    }

    function test_SetAllowedToBatch() public {
        address[] memory tos = new address[](2);
        tos[0] = DEST;
        tos[1] = address(0x1234);

        vm.prank(OWNER);
        sel.setAllowedToBatch(SENDER, tos, true);
        require(sel.isAllowedTo(SENDER, DEST) && sel.isAllowedTo(SENDER, address(0x1234)), "batch must allow all");
    }

    /// Pins the exact storage slots read by Go consensus
    /// (see bevm/params/blocklist.go and bevm/bdag/blocklist.go).
    function test_ConsensusStorageLayout() public {
        vm.prank(OWNER);
        sel.setRestricted(SENDER, true);
        vm.prank(OWNER);
        sel.setAllowedTo(SENDER, DEST, true);

        // slot 0: owner
        require(
            address(uint160(uint256(vm.load(address(sel), bytes32(0))))) == OWNER,
            "slot0 must be owner"
        );

        // slot 1 mapping: restricted[SENDER] = keccak256(abi.encode(SENDER, 1))
        bytes32 restrictedSlot = keccak256(abi.encode(SENDER, uint256(1)));
        require(
            uint256(vm.load(address(sel), restrictedSlot)) == 1,
            "restricted flag must live in slot-1 mapping"
        );

        // slot 2 nested mapping: allowedTo[SENDER][DEST]
        //   level1 = keccak256(abi.encode(SENDER, 2))
        //   slot   = keccak256(abi.encode(DEST, level1))
        bytes32 level1 = keccak256(abi.encode(SENDER, uint256(2)));
        bytes32 allowedToSlot = keccak256(abi.encode(DEST, level1));
        require(
            uint256(vm.load(address(sel), allowedToSlot)) == 1,
            "allowedTo flag must live in slot-2 nested mapping"
        );
    }

    function test_NonOwnerCannotRestrict() public {
        vm.expectRevert(bytes("not owner"));
        vm.prank(OUTSIDER);
        sel.setRestricted(SENDER, true);
    }

    function test_NonOwnerCannotSetAllowedTo() public {
        vm.expectRevert(bytes("not owner"));
        vm.prank(OUTSIDER);
        sel.setAllowedTo(SENDER, DEST, true);
    }

    function test_TransferOwnership() public {
        address newOwner = address(0xCAFE);
        vm.prank(OWNER);
        sel.transferOwnership(newOwner);
        require(sel.owner() == newOwner, "ownership must transfer");

        vm.prank(newOwner);
        sel.setRestricted(SENDER, true);
        require(sel.isRestricted(SENDER), "new owner must be able to restrict");

        vm.expectRevert(bytes("not owner"));
        vm.prank(OWNER);
        sel.setRestricted(SENDER, false);
    }
}
