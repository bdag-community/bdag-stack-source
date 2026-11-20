// Storage-layout guard for the BDAG staking contract.
//
// The Go consensus engine (blockdag-corechain/bevm/params/staking.go) reads and writes
// this contract's storage BY SLOT INDEX every block. The table below is the engine's
// view. Any V2+ implementation must keep slots 0-17 byte-identical and only append.
// This test is the gate before any bytecode is placed in genesis or a fork table.
const { expect } = require("chai");
const hre = require("hardhat");
const { ethers } = hre;

// Engine table: bevm/params/staking.go (SlotOwner..SlotStakerUnbondingBDAG)
const ENGINE_LAYOUT = [
    [0, "owner", "t_address"],
    [1, "minerSplit", "t_uint256"],
    [2, "stakerSplit", "t_uint256"],
    [3, "stakerPoolShares", "t_mapping(t_address,t_mapping(t_address,t_uint256))"],
    [4, "__unused_slot_4", "t_mapping(t_address,t_mapping(t_address,t_uint256))"],
    [5, "poolTotalAssets", "t_mapping(t_address,t_uint256)"],
    [6, "poolTotalShares", "t_mapping(t_address,t_uint256)"],
    [7, "poolEpochAutoStake", "t_mapping(t_address,t_mapping(t_uint256,t_uint256))"],
    [8, "genesisTime", "t_uint256"],
    [9, "lastClaimedAutoStakeEpoch", "t_mapping(t_address,t_uint256)"],
    [10, "unbondingRequests", "t_mapping(t_address,t_array(t_struct(UnbondingRequest)"],
    [11, "userFirstDepositTimestamp", "t_mapping(t_address,t_uint256)"],
    [12, "stakerTotalLockedShares", "t_mapping(t_address,t_mapping(t_address,t_uint256))"],
    [13, "poolTotalStakers", "t_mapping(t_address,t_uint256)"],
    [14, "stakerPrincipalBDAG", "t_mapping(t_address,t_mapping(t_address,t_uint256))"],
    [15, "poolTotalStake", "t_mapping(t_address,t_uint256)"],
    [16, "isManager", "t_mapping(t_address,t_bool)"],
    [17, "stakerUnbondingBDAG", "t_mapping(t_address,t_mapping(t_address,t_uint256))"],
];
const V2_APPENDED = [
    [18, "v2ActivatedAt", "t_uint256"],
    [19, "_votingCheckpoints", "t_mapping(t_address,t_mapping(t_address,t_array(t_struct(VoteCheckpoint)"],
    [20, "_totalVotingCheckpoints", "t_array(t_struct(VoteCheckpoint)"],
    [21, "stakingBlockedUntil", "t_mapping(t_address,t_uint256)"],
    [22, "_retiredGuardian", "t_address"], [23, "_retiredGuardianExpiry", "t_uint256"],
    [24, "_minStakeOverride", "t_uint256"], [25, "_bondingOverridePlusOne", "t_uint256"],
    [26, "_unbondingOverride", "t_uint256"], [27, "pausedUntil", "t_uint256"],
    [28, "pendingOwner", "t_address"], [29, "_reservedRepairRoot", "t_bytes32"],
    [30, "_reservedRepaired", "t_mapping(t_bytes32,t_bool)"], [31, "_retiredVotingSeedEpoch", "t_uint256"],
    [32, "_retiredVotingSeeded", "t_bool"], [32, "daoInitialized", "t_bool", 1],
    [33, "_bondingEndsAt", "t_mapping(t_address,t_uint256)"],
    [34, "poolEpochRewardPerShare", "t_mapping(t_address,t_mapping(t_uint256,t_uint256))"],
    [35, "poolRewardRemainder", "t_mapping(t_address,t_uint256)"],
    [36, "stakingEpochVotingTotal", "t_mapping(t_uint256,t_uint256)"],
    [37, "operatorEpochVoting", "t_mapping(t_address,t_mapping(t_uint256,t_uint256))"],
    [38, "_rewardSettled", "t_mapping(t_address,t_mapping(t_address,t_mapping(t_uint256,t_uint256)))"],
    [39, "_rewardAccCheckpoint", "t_mapping(t_address,t_mapping(t_address,t_mapping(t_uint256,t_uint256)))"],
    [40, "_rewardShares", "t_mapping(t_address,t_mapping(t_address,t_array(t_struct(RewardShareCheckpoint)"],
];

// ERC-7201 namespaces the engine knows (bevm/params/staking.go SlotInitializableStorage / SlotReentrancyGuardState)
const ENGINE_INITIALIZABLE_SLOT = "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00";
const ENGINE_REENTRANCY_SLOT = "0x9b779b17422d0df92223018b32b4d1fa46e071723d6817e2486d003becc55f00";
// Pin the reviewed public surface as well as retaining every V1 wire signature.
const V2_EXTRA_ABI = new Set([
    "DaoInitialized(address,uint256)", "DisabledLegacyFunction()", "DuplicatePool()",
    "InvalidParameter()", "InvalidVotingSeed()", "OwnershipTransferStarted(address,address)",
    "ParameterChanged(bytes32,uint256)", "StakingBlockChanged(address,uint256)", "StakingBlocked()",
    "StakingPauseChanged(uint256)", "StakingVersion(uint256)", "VotingNotSeeded()", "VotingTotalSeeded(uint256,uint256,bytes32)",
    "acceptOwnership()", "bondingEndsAt(address)", "bondingPeriod()", "daoInitialized()", "epochDuration()", "initializeV2()",
    "minStake()", "operatorEpoch()", "operatorEpochVoting(address,uint256)", "pausedUntil()", "pendingOwner()",
    "poolEpochRewardPerShare(address,uint256)", "poolRewardRemainder(address)", "rewardEpochFinalized(uint256)",
    "setBondingPeriod(uint256)", "setMinStake(uint256)",
    "setStakingBlockedUntil(address,uint256)", "setUnbondingPeriod(uint256)", "stakingBlockedUntil(address)",
    "stakingEpochVotingTotal(uint256)", "totalVotingWeightAt(uint256)", "unbondingPeriod()", "v2ActivatedAt()", "version()",
    "votingWeightAt(address,address[],uint256)",
]);

const V1 = "contracts/staking/StakingPool.sol:StakingContract";
const V2 = "contracts/staking/StakingPoolV2.sol:StakingContractV2";
const DEVNET_V2 = "contracts/staking/StakingPoolV2.sol:StakingContractV2Devnet";
const TESTNET_V2 = "contracts/staking/StakingPoolV2.sol:StakingContractV2Testnet";

async function layoutOf(fqn) {
    const [source, name] = fqn.split(":");
    const info = await hre.artifacts.getBuildInfo(fqn);
    expect(info, `build-info for ${fqn}`).to.not.equal(undefined);
    return info.output.contracts[source][name].storageLayout.storage.map((e) => ({
        // solc suffixes struct types with an AST id (t_struct(X)1234_storage) which
        // differs per compilation unit; strip it so V1/V2 compare on shape only.
        slot: Number(e.slot), offset: e.offset, label: e.label, type: e.type.replace(/\)\d+_storage/g, ")_storage"),
    }));
}

function erc7201(id) {
    const h = BigInt(ethers.keccak256(ethers.toUtf8Bytes(id))) - 1n;
    const inner = ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(["uint256"], [h]));
    return ethers.toBeHex(BigInt(inner) & ~0xffn, 32);
}

// internalType carries the contract name (struct StakingContractV2.UnbondingRequest);
// the wire ABI does not. Compare wire shape only.
function shape(params) {
    return (params || []).map((p) => ({ name: p.name, type: p.type, indexed: p.indexed, components: shape(p.components) }));
}
function abiSignatures(abi) {
    const out = new Map();
    for (const f of abi) {
        if (f.type === "constructor" || f.type === "fallback" || f.type === "receive") continue;
        const sig = `${f.name}(${(f.inputs || []).map((i) => i.type).join(",")})`;
        out.set(sig, JSON.stringify({ t: f.type, in: shape(f.inputs), out: shape(f.outputs), sm: f.stateMutability }));
    }
    return out;
}

describe("Storage layout (consensus-critical)", function () {
    it("V1 linear layout matches the Go engine slot table exactly", async function () {
        const v1 = await layoutOf(V1);
        expect(v1.length, "V1 must expose exactly 18 linear slots").to.equal(ENGINE_LAYOUT.length);
        expect(v1[0].label).to.equal("owner");
        for (const [slot, label, typePrefix] of ENGINE_LAYOUT) {
            const e = v1[slot];
            expect(e.slot, `${label} slot`).to.equal(slot);
            expect(e.offset, `${label} offset`).to.equal(0);
            expect(e.label, `slot ${slot} label`).to.equal(label);
            expect(e.type.startsWith(typePrefix), `slot ${slot} type ${e.type}`).to.equal(true);
        }
    });

    for (const fqn of [V2, DEVNET_V2, TESTNET_V2]) it(`${fqn.split(":")[1]} preserves slots 0-17 and pins every addition through slot 40`, async function () {
        const v1 = await layoutOf(V1);
        const v2 = await layoutOf(fqn);
        expect(v2.length).to.equal(ENGINE_LAYOUT.length + V2_APPENDED.length);
        for (let i = 0; i < v1.length; i++) {
            expect(v2[i], `slot ${i}`).to.deep.equal(v1[i]);
        }
        for (const [slot, label, type, offset = 0] of V2_APPENDED) {
            const actual = v2.find(entry => entry.label === label);
            expect(actual.slot, label).to.equal(slot);
            expect(actual.offset, label).to.equal(offset);
            expect(actual.type.startsWith(type), `${label} type ${actual.type}`).to.equal(true);
        }
    });

    it("OZ namespaced slots match the engine's pinned ERC-7201 locations", function () {
        expect(erc7201("openzeppelin.storage.Initializable")).to.equal(ENGINE_INITIALIZABLE_SLOT);
        expect(erc7201("openzeppelin.storage.ReentrancyGuard")).to.equal(ENGINE_REENTRANCY_SLOT);
    });

    for (const fqn of [V2, DEVNET_V2, TESTNET_V2]) it(`${fqn.split(":")[1]} retains the complete V1 ABI and pins the new DAO surface`, async function () {
        const a1 = abiSignatures((await hre.artifacts.readArtifact(V1)).abi);
        const a2 = abiSignatures((await hre.artifacts.readArtifact(fqn)).abi);
        for (const [sig, shape] of a1) {
            expect(a2.has(sig), `V2 is missing ${sig}`).to.equal(true);
            expect(a2.get(sig), `V2 changed ${sig}`).to.equal(shape);
        }
        const extras = [...a2.keys()].filter((s) => !a1.has(s)).sort();
        expect(extras).to.deep.equal([...V2_EXTRA_ABI].sort());
    });

    it("all staking artifacts fit the code-size limit and target London without PUSH0 or MCOPY", async function () {
        // Cheap heuristic: disassemble by walking PUSH lengths and assert 0x5f never appears
        // as an instruction. A false positive is impossible for valid london code.
        for (const fqn of [V1, V2, DEVNET_V2, TESTNET_V2]) {
            const code = ethers.getBytes((await hre.artifacts.readArtifact(fqn)).deployedBytecode);
            expect(code.length, `${fqn} runtime size`).to.be.at.most(24576);
            let i = 0, push0 = 0, mcopy = 0;
            while (i < code.length) {
                const op = code[i];
                if (op === 0x5f) push0++;
                if (op === 0x5e) mcopy++;
                if (op >= 0x60 && op <= 0x7f) i += op - 0x5f; // skip PUSH data
                i++;
                if (op === 0xfe) break; // INVALID: metadata follows
            }
            expect(push0, `${fqn} contains PUSH0`).to.equal(0);
            expect(mcopy, `${fqn} contains MCOPY`).to.equal(0);
        }
    });
});
