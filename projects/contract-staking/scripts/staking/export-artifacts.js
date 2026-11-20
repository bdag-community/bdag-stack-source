// Emits everything the node team needs to (a) regenerate the devnet genesis as
// proxy(0x08Bd..) -> V1 impl, and (b) fork-swap to V2 at a block height.
//
//   DEVNET_OWNER_ADDR=0x.. DEVNET_FAUCET_ADDR=0x.. npx hardhat run scripts/staking/export-artifacts.js
//
// Output (out/devnet/, gitignored):
//   UUPSProxy.runtime.hex                  ERC1967 proxy runtime (no immutables)
//   StakingContract.creation.hex           V1 creation bytecode
//   StakingContractV2.creation.hex         V2 creation bytecode  <- embedded by the node (go:embed)
//   StakingContract.runtime.<impl>.hex     V1 runtime with UUPS __self patched to the impl address
//   StakingContractV2.runtime.<impl>.hex   V2 runtime with __self patched
//   genesis-alloc.devnet.json              accounts to MERGE into the devnet genesis alloc
//   fork-swap.v2.json                      impl address + creation code + expected runtime hash
//
// Why patch __self: OZ UUPSUpgradeable stores address(this) as an immutable at construction.
// Artifact deployedBytecode has it zeroed; behind a proxy _checkProxy() would then always revert.
// The node derives the runtime by executing creation code at the target address, which yields
// exactly these patched bytes (expectedRuntimeHash cross-checks the two methods).
const fs = require("fs");
const path = require("path");
const { execSync } = require("child_process");
const hre = require("hardhat");
const { ethers } = hre;

const ROOT = path.join(__dirname, "..", "..");
const OUT = path.join(ROOT, "out", "devnet");
const PROXY = process.env.STAKING_CONTRACT_ADDRESS || "0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7";
const IMPL_V1 = process.env.DEVNET_IMPL_V1_ADDRESS || "0x1000000000000000000000000000000000000011";
const IMPL_V2 = process.env.DEVNET_IMPL_V2_ADDRESS || "0x1000000000000000000000000000000000000012";
const GENESIS_TIME = BigInt(process.env.DEVNET_GENESIS_TIME || "1758551400"); // devnet native genesis ts
const OWNER = process.env.DEVNET_OWNER_ADDR;
const FAUCET = process.env.DEVNET_FAUCET_ADDR;
const OWNER_PREMINE = process.env.DEVNET_OWNER_PREMINE || "1000000000000000000000000";        // 1M BDAG
const FAUCET_PREMINE = process.env.DEVNET_FAUCET_PREMINE || "1000000000000000000000000000";   // 1B BDAG

const SLOT_IMPL = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc";
const SLOT_INITIALIZABLE = "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00";
const SLOT_REENTRANCY = "0x9b779b17422d0df92223018b32b4d1fa46e071723d6817e2486d003becc55f00";
const coder = ethers.AbiCoder.defaultAbiCoder();
const h32 = (n) => ethers.toBeHex(BigInt(n), 32);
const slot1 = (key, base) => ethers.keccak256(coder.encode(["address", "uint256"], [key, base]));

async function contractOutput(fqn) {
    const [source, name] = fqn.split(":");
    const info = await hre.artifacts.getBuildInfo(fqn);
    if (!info) throw new Error(`no build-info for ${fqn}; run npx hardhat compile`);
    const c = info.output.contracts[source][name];
    return { creation: "0x" + c.evm.bytecode.object, runtime: "0x" + c.evm.deployedBytecode.object,
             immutableRefs: c.evm.deployedBytecode.immutableReferences || {}, solc: info.solcVersion, settings: info.input.settings };
}

function patchImmutables(runtimeHex, refs, address) {
    const code = ethers.getBytes(runtimeHex);
    const addr = ethers.getBytes(ethers.zeroPadValue(address, 32));
    let n = 0;
    for (const list of Object.values(refs)) for (const { start, length } of list) {
        if (length !== 32) throw new Error(`unexpected immutable length ${length}`);
        for (let i = 0; i < 32; i++) { if (code[start + i] !== 0) throw new Error(`immutable placeholder not zero at ${start + i}`); code[start + i] = addr[i]; }
        n++;
    }
    return { hex: ethers.hexlify(code), patched: n };
}

async function main() {
    if (!OWNER || !FAUCET) throw new Error("DEVNET_OWNER_ADDR and DEVNET_FAUCET_ADDR are required");
    fs.mkdirSync(OUT, { recursive: true });
    const proxy = await contractOutput("contracts/proxy/UUPSProxy.sol:UUPSProxy");
    const v1 = await contractOutput("contracts/staking/StakingPool.sol:StakingContract");
    const v2 = await contractOutput("contracts/staking/StakingPoolV2.sol:StakingContractV2");
    if (Object.keys(proxy.immutableRefs).length) throw new Error("proxy unexpectedly has immutables");
    if (v1.settings.evmVersion !== "london" || v2.settings.evmVersion !== "london") throw new Error("evmVersion must be london");

    const v1p = patchImmutables(v1.runtime, v1.immutableRefs, IMPL_V1);
    const v2p = patchImmutables(v2.runtime, v2.immutableRefs, IMPL_V2);
    if (v1p.patched === 0 || v2p.patched === 0) throw new Error("expected UUPS __self immutable references");

    const w = (f, s) => fs.writeFileSync(path.join(OUT, f), s.endsWith("\n") ? s : s + "\n");
    w("UUPSProxy.runtime.hex", proxy.runtime);
    w("StakingContract.creation.hex", v1.creation);
    w("StakingContractV2.creation.hex", v2.creation);
    w(`StakingContract.runtime.${IMPL_V1}.hex`, v1p.hex);
    w(`StakingContractV2.runtime.${IMPL_V2}.hex`, v2p.hex);

    const ownerHash = ethers.zeroPadValue(OWNER, 32);
    const alloc = {
        [PROXY]: {
            balance: "0",
            code: proxy.runtime,
            storage: {
                [h32(0)]: ownerHash,                       // owner
                [h32(1)]: h32(70),                         // minerSplit (read by the engine)
                [h32(2)]: h32(30),                         // stakerSplit
                [h32(8)]: h32(GENESIS_TIME),               // genesisTime = native devnet genesis ts
                [slot1(OWNER, 16)]: h32(1),                // isManager[owner]
                [SLOT_INITIALIZABLE]: h32(1),              // _initialized = 1
                [SLOT_REENTRANCY]: h32(1),                 // NOT_ENTERED
                [SLOT_IMPL]: ethers.zeroPadValue(IMPL_V1, 32),
            },
        },
        [IMPL_V1]: { balance: "0", code: v1p.hex, nonce: 1 },
        [OWNER]: { balance: OWNER_PREMINE },
        [FAUCET]: { balance: FAUCET_PREMINE },
    };
    w("genesis-alloc.devnet.json", JSON.stringify(alloc, null, 2));

    let sourceCommit = "unknown"; try { sourceCommit = execSync("git rev-parse HEAD", { cwd: ROOT }).toString().trim(); } catch {}
    w("fork-swap.v2.json", JSON.stringify({
        contract: "contracts/staking/StakingPoolV2.sol:StakingContractV2",
        proxyAddress: PROXY, implAddress: IMPL_V2,
        creationHex: v2.creation,
        expectedRuntimeHash: ethers.keccak256(v2p.hex), expectedRuntimeLength: ethers.getBytes(v2p.hex).length,
        v1ImplAddress: IMPL_V1, v1RuntimeHash: ethers.keccak256(v1p.hex),
        solc: v1.solc, evmVersion: "london", optimizerRuns: v1.settings.optimizer.runs, sourceCommit,
        notes: [
            "Embed creationHex and execute it AT implAddress to derive the runtime (UUPS __self immutable).",
            "Then SetCode(implAddress, runtime), nonce 1, and write the ERC-1967 implementation slot of proxyAddress = implAddress.",
            "eth_getCode(implAddress) must hash to expectedRuntimeHash; verify-v2.js asserts this.",
        ],
    }, null, 2));

    console.log(JSON.stringify({ out: OUT, proxy: PROXY, implV1: IMPL_V1, implV2: IMPL_V2, owner: OWNER, faucet: FAUCET,
        v1RuntimeBytes: ethers.getBytes(v1p.hex).length, v2RuntimeBytes: ethers.getBytes(v2p.hex).length,
        v1Patched: v1p.patched, v2Patched: v2p.patched, v2RuntimeHash: ethers.keccak256(v2p.hex) }, null, 2));
}
main().catch((e) => { console.error(e); process.exit(1); });
