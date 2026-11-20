// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ERC1155} from "@openzeppelin/contracts/token/ERC1155/ERC1155.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {SignatureChecker} from "@openzeppelin/contracts/utils/cryptography/SignatureChecker.sol";
import {Checkpoints} from "@openzeppelin/contracts/utils/structs/Checkpoints.sol";
import {SafeCast} from "@openzeppelin/contracts/utils/math/SafeCast.sol";
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {IVersionedEpochClock} from "./IVersionedEpochClock.sol";
import {EpochSchedule} from "./EpochSchedule.sol";
import {NativeEpochClock} from "./NativeEpochClock.sol";

interface ISeparatedCredentialReader {
    function stakingSourceAt(uint256 sourceEpoch) external view returns (address);
}

interface IEpochStakingSource {
    function currentEpoch() external view returns (uint256);
    function epochDuration() external view returns (uint256);
    function genesisTime() external view returns (uint256);
    function rewardEpochFinalized(uint256 sourceEpoch) external view returns (bool);
    function votingWeightAt(address account, address[] calldata pools, uint256 sourceEpoch) external view returns (uint256);
    function totalVotingWeightAt(uint256 sourceEpoch) external view returns (uint256);
    function stakerPrincipalBDAG(address account, address pool) external view returns (uint256);
}

interface ILegacyEpochClock {
    function EPOCH_DURATION() external view returns (uint256);
}

interface IEpochMiningSource {
    function clockSource() external view returns (address);
    function isFinalized(uint256 epoch) external view returns (bool);
    function weightAt(address miner, uint256 epoch) external view returns (uint256);
    function totalAt(uint256 epoch) external view returns (uint256);
    function currentEpoch() external view returns (uint256);
}

/// @notice Storage-compatible dual-source credential; historical implementation is unchanged.
/// Epoch E+1 represents the closed balances/tallies of source epoch E.
/// @dev ERC1155 mint balances are visibility receipts only. Governor weights read
///      the underlying immutable source history and never require a claim.
///      ERC20-style balanceOf(address) is a raw-weight view over registered pools.
contract SeparatedEpochCredential is ERC1155, EIP712, ReentrancyGuard {
    using Checkpoints for Checkpoints.Trace208;

    error InvalidSource();
    error InvalidEpoch();
    error VotingUnavailable();
    error InvalidList();
    error UnauthorizedSource();
    error InvalidPool();
    error InvalidSignature();
    error InvalidNonce();
    error SignatureExpired();
    error ClaimWindowClosed();
    error NothingToClaim();
    error Soulbound();

    IEpochStakingSource public immutable stakingSource;
    IEpochMiningSource public immutable miningSource;
    uint256 private immutable _genesisTime;
    uint256 private immutable _epochDuration;
    string public name;
    string public symbol;
    uint8 public constant decimals = 18;
    uint256 public constant MAX_POOLS = 200;
    address public constant SYSTEM_READER = 0x1000000000000000000000000000000000000002;
    bytes32 public constant DELEGATION_TYPEHASH = keccak256("Delegation(address account,address delegatee,uint256 nonce,uint256 deadline)");

    mapping(address => address[]) private _pools;
    mapping(address => Checkpoints.Trace208) private _delegates;
    mapping(address => Checkpoints.Trace208) private _incoming;
    mapping(address => uint256) public nonces;

    event PoolRegistered(address indexed account, address indexed pool);
    event DelegationChanged(address indexed account, address indexed previousDelegate, address indexed newDelegate, uint256 effectiveCredentialEpoch);
    event CredentialClaimed(address indexed account, uint256 indexed credentialEpoch, uint256 rawWeight, uint256 mintedDelta);

    constructor(address staking_, address mining_, string memory name_, string memory symbol_)
        ERC1155("") EIP712(name_, "2") {
        if (staking_.code.length == 0) revert InvalidSource();
        // The canonical ChainTally address is intentionally codeless until the
        // fork installs Reader runtime. No arbitrary undeployed reader is allowed.
        if (mining_ != address(0) && mining_.code.length == 0 && mining_ != SYSTEM_READER) revert InvalidSource();
        stakingSource = IEpochStakingSource(staking_);
        miningSource = IEpochMiningSource(mining_);
        // Governance is deployed before the fork, while the real V1 exposes only
        // EPOCH_DURATION(). Cache the reviewed clock so a compromised V1 owner
        // cannot move governance time by mutating the proxy's genesis slot later.
        uint256 duration;
        try stakingSource.epochDuration() returns (uint256 value) { duration = value; }
        catch { duration = ILegacyEpochClock(staking_).EPOCH_DURATION(); }
        if (duration == 0) revert InvalidSource();
        _epochDuration = duration;
        _genesisTime = stakingSource.genesisTime();
        if (mining_.code.length != 0 && miningSource.clockSource() != staking_) revert InvalidSource();
        name = name_;
        symbol = symbol_;
    }

    function currentEpoch() public view virtual returns (uint256) {
        return _separatedClock().epoch;
    }
    function epochDuration() public view virtual returns (uint256) { return _separatedClock().duration; }

    function _separatedClock() private view returns (EpochSchedule.Status memory) {
        IVersionedEpochClock clock = IVersionedEpochClock(SYSTEM_READER);
        if (clock.clockSource() != address(stakingSource) || clock.genesisTime() != _genesisTime
            || stakingSource.genesisTime() != _genesisTime) revert InvalidSource();
        return clock.epochStatus();
    }

    function stakingSourceAt(uint256 sourceEpoch) public view returns (IEpochStakingSource source) {
        address selected = ISeparatedCredentialReader(SYSTEM_READER).stakingSourceAt(sourceEpoch);
        if (selected.code.length == 0) revert InvalidSource();
        source = IEpochStakingSource(selected);
    }
    function genesisTime() public view returns (uint256) { return _genesisTime; }

    function _stakingReady(uint256 sourceEpoch) private view returns (bool) {
        IEpochStakingSource selected = stakingSourceAt(sourceEpoch);
        // Missing V3 functions or a clock mismatch must fail closed. Catching the
        // expected legacy selector reverts permits safe, inert pre-fork deployment.
        try selected.epochDuration() returns (uint256 duration) {
            if (duration != epochDuration()) return false;
        } catch { return false; }
        try selected.genesisTime() returns (uint256 genesis) {
            if (genesis != _genesisTime) return false;
        } catch { return false; }
        try selected.rewardEpochFinalized(sourceEpoch) returns (bool finalized) { return finalized; }
        catch { return false; }
    }

    function votingReady() public view returns (bool) {
        uint256 epoch = currentEpoch();
        if (epoch == 0) return false;
        uint256 sourceEpoch = epoch - 1;
        return address(miningSource) == address(0) ? _stakingReady(sourceEpoch) : _miningReady(sourceEpoch);
    }

    function _miningReady(uint256 sourceEpoch) private view returns (bool) {
        address reader = address(miningSource);
        if (reader == address(0)) return true;
        if (reader.code.length == 0) return false;
        try miningSource.clockSource() returns (address clock) {
            if (clock != address(stakingSource)) return false;
        } catch { return false; }
        try miningSource.isFinalized(sourceEpoch) returns (bool finalized) { return finalized; }
        catch { return false; }
    }

    function _sourceEpoch(uint256 credentialEpoch) private view returns (uint256 sourceEpoch) {
        if (credentialEpoch == 0 || credentialEpoch > currentEpoch()) revert InvalidEpoch();
        sourceEpoch = credentialEpoch - 1;
        bool ready = address(miningSource) == address(0) ? _stakingReady(sourceEpoch) : _miningReady(sourceEpoch);
        if (!ready) revert VotingUnavailable();
    }

    function totalSupplyAt(uint256 credentialEpoch) public view returns (uint256) {
        uint256 sourceEpoch = _sourceEpoch(credentialEpoch);
        return address(miningSource) == address(0)
            ? stakingSourceAt(sourceEpoch).totalVotingWeightAt(sourceEpoch)
            : miningSource.totalAt(sourceEpoch);
    }

    function totalSupply() external view returns (uint256) {
        return votingReady() ? totalSupplyAt(currentEpoch()) : 0;
    }

    function _validatePools(address[] memory pools) private pure {
        if (pools.length > MAX_POOLS) revert InvalidList();
        address previous;
        for (uint256 i; i < pools.length; ++i) {
            if (pools[i] <= previous) revert InvalidList();
            previous = pools[i];
        }
    }

    /// @notice Permissionless discovery only; cannot invent any voting weight.
    ///         Explicit ballot/claim pools remain available without registration.
    function registerPools(address account, address[] calldata pools) external {
        if (address(miningSource) != address(0) || account == address(0) || pools.length == 0) revert InvalidPool();
        _validatePools(pools);
        address[] storage registeredPools = _pools[account];
        for (uint256 i; i < pools.length; ++i) {
            address pool = pools[i];
            if (pool == address(0)) revert InvalidPool();
            uint256 at;
            while (at < registeredPools.length && registeredPools[at] < pool) ++at;
            if (at < registeredPools.length && registeredPools[at] == pool) continue;
            if (registeredPools.length == MAX_POOLS) revert InvalidList();
            registeredPools.push(pool);
            for (uint256 j = registeredPools.length - 1; j > at; --j) registeredPools[j] = registeredPools[j - 1];
            registeredPools[at] = pool;
            emit PoolRegistered(account, pool);
        }
    }

    function poolsOf(address account) external view returns (address[] memory) { return _pools[account]; }

    function _rawWeight(address account, uint256 sourceEpoch, address[] memory pools) private view returns (uint256) {
        if (address(miningSource) != address(0)) {
            if (pools.length != 0) revert InvalidPool();
            return miningSource.weightAt(account, sourceEpoch);
        }
        if (pools.length == 0) pools = _pools[account];
        _validatePools(pools);
        return stakingSourceAt(sourceEpoch).votingWeightAt(account, pools, sourceEpoch);
    }

    function rawWeightOf(address account, uint256 credentialEpoch, address[] memory pools) public view returns (uint256) {
        return _rawWeight(account, _sourceEpoch(credentialEpoch), pools);
    }

    /// @notice Raw current-epoch weight for wallet display, independent of caps,
    ///         delegation and claims. Register all pools for a complete default view.
    function balanceOf(address account) public view returns (uint256) {
        return votingReady() ? rawWeightOf(account, currentEpoch(), new address[](0)) : 0;
    }

    function delegateAt(address account, uint256 credentialEpoch) public view returns (address) {
        address result = address(uint160(_delegates[account].upperLookup(SafeCast.toUint48(credentialEpoch))));
        return result == address(0) ? account : result;
    }

    function isDelegate(address account, uint256 credentialEpoch) external view returns (bool) {
        return _incoming[account].upperLookup(SafeCast.toUint48(credentialEpoch)) != 0;
    }

    function delegate(address delegatee) external {
        // A direct instruction also invalidates any outstanding signed nonce.
        ++nonces[msg.sender];
        _delegate(msg.sender, delegatee);
    }

    function delegateBySig(address account, address delegatee, uint256 nonce, uint256 deadline, bytes calldata signature) external {
        if (block.timestamp > deadline) revert SignatureExpired();
        if (nonce != nonces[account]) revert InvalidNonce();
        bytes32 digest = _hashTypedDataV4(keccak256(abi.encode(DELEGATION_TYPEHASH, account, delegatee, nonce, deadline)));
        if (account == address(0) || !SignatureChecker.isValidSignatureNow(account, digest, signature)) revert InvalidSignature();
        ++nonces[account];
        _delegate(account, delegatee);
    }

    function _delegate(address account, address delegatee) private {
        uint48 effective = SafeCast.toUint48(currentEpoch() + 1);
        if (delegatee == address(0)) delegatee = account;
        address previous = delegateAt(account, effective);
        if (previous != account) {
            Checkpoints.Trace208 storage incoming = _incoming[previous];
            incoming.push(effective, incoming.upperLookup(effective) - 1);
        }
        if (delegatee != account) {
            Checkpoints.Trace208 storage incoming = _incoming[delegatee];
            incoming.push(effective, incoming.upperLookup(effective) + 1);
        }
        _delegates[account].push(effective, uint208(uint160(delegatee)));
        emit DelegationChanged(account, previous, delegatee, effective);
    }

    /// @notice Every earned unit carries its full weight. params encodes sorted
    ///         owners and their corresponding sorted pool lists for delegation.
    ///         Delegation is one hop: received votes are never forwarded onward.
    function getVotes(address voter, uint256 credentialEpoch, bytes calldata params) external view returns (uint256 votes) {
        uint256 sourceEpoch = _sourceEpoch(credentialEpoch);
        if (params.length == 0) {
            if (delegateAt(voter, credentialEpoch) != voter) return 0;
            return _rawWeight(voter, sourceEpoch, new address[](0));
        }
        (address[] memory sources, address[][] memory pools) = abi.decode(params, (address[], address[][]));
        if (sources.length == 0 || sources.length != pools.length) revert InvalidList();
        address previous;
        for (uint256 i; i < sources.length; ++i) {
            if (sources[i] <= previous) revert InvalidList();
            previous = sources[i];
            if (delegateAt(sources[i], credentialEpoch) != voter) revert UnauthorizedSource();
            votes += _rawWeight(sources[i], sourceEpoch, pools[i]);
        }
    }

    function claim(uint256 credentialEpoch, address[] calldata pools) external nonReentrant {
        _claimFor(msg.sender, credentialEpoch, pools);
    }

    function claimFor(address account, uint256 credentialEpoch, address[] calldata pools) external nonReentrant {
        _claimFor(account, credentialEpoch, pools);
    }

    function _claimFor(address account, uint256 credentialEpoch, address[] memory pools) private {
        uint256 epoch = currentEpoch();
        if (credentialEpoch > epoch || epoch - credentialEpoch > 2) revert ClaimWindowClosed();
        uint256 raw = rawWeightOf(account, credentialEpoch, pools);
        uint256 alreadyMinted = super.balanceOf(account, credentialEpoch);
        if (raw <= alreadyMinted) revert NothingToClaim();
        uint256 delta = raw - alreadyMinted;
        _mint(account, credentialEpoch, delta, "");
        emit CredentialClaimed(account, credentialEpoch, raw, delta);
    }

    function setApprovalForAll(address, bool) public pure override { revert Soulbound(); }
    function safeTransferFrom(address, address, uint256, uint256, bytes memory) public pure override { revert Soulbound(); }
    function safeBatchTransferFrom(address, address, uint256[] memory, uint256[] memory, bytes memory) public pure override { revert Soulbound(); }
    function approve(address, uint256) external pure returns (bool) { revert Soulbound(); }
    function transfer(address, uint256) external pure returns (bool) { revert Soulbound(); }
    function transferFrom(address, address, uint256) external pure returns (bool) { revert Soulbound(); }
    function allowance(address, address) external pure returns (uint256) { return 0; }
}
