// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IReaderEpochClock {
    function currentEpoch() external view returns (uint256);
}

/// @notice Read-only views over native reward accounting written by consensus.
/// @dev Slots 0..7 belong exclusively to the engine. This runtime never asks a
///      wallet to register and never needs a keeper to iterate an epoch.
contract ChainTallyReader {
    error EpochNotFinalized();
    error InvalidClock();

    bytes32 private constant REWARD_DOMAIN = keccak256("bdag.reward.voting.v1");

    // Historical ChainTally slots. Kept byte-for-byte for legacy inspection.
    uint256[4] private engineReserved;
    // New append-only reward-voting ledger.
    uint256 public rewardEpoch; // slot 4
    uint256 public rewardBootstrapEpochPlusOne; // slot 5
    uint256 public firstCompleteRewardEpochPlusOne; // slot 6
    mapping(uint256 => uint256) private rewardTotals; // slot 7

    IReaderEpochClock public immutable clockSource;

    constructor(address clock_) {
        if (clock_.code.length == 0) revert InvalidClock();
        clockSource = IReaderEpochClock(clock_);
    }

    function currentEpoch() public view virtual returns (uint256) { return clockSource.currentEpoch(); }
    function engineEpoch() external view returns (uint256) { return engineReserved[0]; }

    /// @notice Historical full-subsidy tally slot. This is audit-only and is
    ///         intentionally not used by current voting credentials.
    function legacyTallySlot(address miner, uint256 epoch) public pure returns (bytes32) {
        uint256 n;
        for (uint256 v = epoch; v != 0; v >>= 8) ++n;
        bytes memory encoded = new bytes(n);
        for (uint256 i = n; i != 0; --i) { encoded[i - 1] = bytes1(uint8(epoch)); epoch >>= 8; }
        return keccak256(abi.encode(miner, keccak256(encoded)));
    }

    function legacyTallyOf(address miner, uint256 epoch) external view returns (uint256 amount) {
        bytes32 slot = legacyTallySlot(miner, epoch);
        assembly { amount := sload(slot) }
    }

    function rewardTallySlot(address miner, uint256 epoch) public pure returns (bytes32) {
        return keccak256(abi.encode(miner, keccak256(abi.encode(REWARD_DOMAIN, epoch))));
    }

    function isFinalized(uint256 epoch) public view returns (bool) {
        uint256 first = firstCompleteRewardEpochPlusOne;
        return first != 0 && epoch + 1 >= first && epoch < rewardEpoch;
    }

    function totalAt(uint256 epoch) public view returns (uint256) {
        if (!isFinalized(epoch)) revert EpochNotFinalized();
        return rewardTotals[epoch];
    }

    function weightAt(address miner, uint256 epoch) external view returns (uint256 amount) {
        if (!isFinalized(epoch)) revert EpochNotFinalized();
        bytes32 slot = rewardTallySlot(miner, epoch);
        assembly { amount := sload(slot) }
    }
}
