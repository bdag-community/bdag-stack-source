// Path B follow-up: after the node fork-height code swap installed V2 behind the proxy (no
// reinitializer ran), the owner calls initializeV2() directly on the proxy.
//
//   npm run devnet:init-v2      (= hardhat run scripts/devnet/init-v2-after-fork.js --network devnet)
//
// Env: STAKING_CONTRACT_ADDRESS (proxy). Signer[0] (DEVNET_OWNER_KEY) must equal owner().
// Idempotent: if v2ActivatedAt() is already > 0 nothing is sent and the script exits 0.
const L = require("./_lib");
const { ethers } = L;

async function main() {
    const provider = ethers.provider;
    const info = await L.requireProxy(provider);
    const V1 = await ethers.getContractFactory(L.V1_NAME);
    const V2 = await ethers.getContractFactory(L.V2_NAME);
    const ifaces = [V2.interface, V1.interface];
    const signers = await ethers.getSigners();
    if (!signers.length) L.exitWith(L.EXIT.PRECONDITION, "no signers: set DEVNET_OWNER_KEY");
    const owner = signers[0];
    const proxy = V2.attach(L.PROXY).connect(owner);
    const summary = { proxy: L.PROXY, ...info, signer: owner.address };

    const onChainOwner = await proxy.owner();
    const impl = await L.implFromSlot(provider, L.PROXY);
    summary.owner = onChainOwner;
    summary.impl = impl;
    console.log(`init-v2: proxy ${L.PROXY} on ${info.network} (chainId ${info.chainId})`);
    console.log(`  owner()    ${onChainOwner}`);
    console.log(`  signer[0]  ${owner.address}`);
    console.log(`  impl slot  ${impl}`);
    if (!L.sameAddr(onChainOwner, owner.address)) L.exitWith(L.EXIT.FAIL, `signer[0] ${owner.address} is not the owner ${onChainOwner}; set DEVNET_OWNER_KEY`, summary);

    let version = null;
    try { version = await proxy.version(); } catch (e) {
        L.exitWith(L.EXIT.FAIL, `version() reverted (${L.describeError(e, ifaces)}): the proxy is still V1 - wait for the fork block (path B) or run devnet:upgrade-v2 (path A)`, summary);
    }
    summary.version = version;
    if (version !== "2.0.0") L.exitWith(L.EXIT.FAIL, `version() = ${version}, expected 2.0.0`, summary);
    const before = await proxy.v2ActivatedAt();
    const initBefore = L.decodeInitializable(await provider.getStorage(L.PROXY, L.SLOT_INITIALIZABLE));
    summary.v2ActivatedAtBefore = before;
    summary.initializedBefore = initBefore.initialized;
    console.log(`  version()  ${version}; v2ActivatedAt ${before}; _initialized ${initBefore.initialized}`);
    if (before > 0n) {
        summary.ok = true;
        summary.alreadyInitialized = true;
        console.log(`\nalready initialized: v2ActivatedAt = ${before} (${new Date(Number(before) * 1000).toISOString()}); nothing sent`);
        L.printSummary(summary);
        return;
    }

    try { await proxy.initializeV2.staticCall(); } catch (e) {
        L.exitWith(L.EXIT.FAIL, `initializeV2() would revert: ${L.describeError(e, ifaces)}`, summary);
    }
    const fee = await L.feeOverrides(provider);
    summary.gasPrice = fee.gasPrice;
    const tx = await proxy.initializeV2({ gasLimit: 200_000, ...fee });
    console.log(`\n  initializeV2() tx ${tx.hash} sent (nonce ${tx.nonce}, gasPrice ${ethers.formatUnits(fee.gasPrice, "gwei")} gwei)`);
    const rc = await tx.wait();
    summary.txHash = rc.hash;
    summary.block = rc.blockNumber;
    summary.gasUsed = rc.gasUsed;
    if (rc.status !== 1) L.exitWith(L.EXIT.FAIL, `initializeV2 tx ${rc.hash} reverted in block ${rc.blockNumber}`, summary);
    console.log(`  mined in block ${rc.blockNumber}, gasUsed ${rc.gasUsed}`);

    const events = L.parseLogs(rc, ifaces);
    summary.events = events;
    for (const e of events) console.log(`  event ${e.name || "unknown"} ${e.args ? JSON.stringify(e.args) : (e.topics || []).join(",")}`);
    const problems = [];
    if (!events.some((e) => e.name === "Initialized" && String(e.args.version) === "2")) problems.push("Initialized(2) not emitted");
    if (!events.some((e) => e.name === "StakingVersion" && String(e.args.version) === "2")) problems.push("StakingVersion(2) not emitted");

    const after = await proxy.v2ActivatedAt();
    const blk = await provider.getBlock(rc.blockNumber);
    const initAfter = L.decodeInitializable(await provider.getStorage(L.PROXY, L.SLOT_INITIALIZABLE));
    summary.v2ActivatedAt = after;
    summary.blockTimestamp = blk ? blk.timestamp : null;
    summary.initializedAfter = initAfter.initialized;
    console.log(`  v2ActivatedAt ${after} (block timestamp ${summary.blockTimestamp}); _initialized ${initAfter.initialized}`);
    if (after === 0n) problems.push("v2ActivatedAt() still 0 after initializeV2");
    if (blk && after !== BigInt(blk.timestamp)) problems.push(`v2ActivatedAt ${after} != block timestamp ${blk.timestamp}`);
    if (initAfter.initialized !== 2n) problems.push(`_initialized = ${initAfter.initialized}, expected 2`);

    L.writeJson(L.outPath("init-v2.json"), { ...summary, ok: problems.length === 0, problems, createdAt: new Date().toISOString() });
    if (problems.length) L.exitWith(L.EXIT.FAIL, `initializeV2 post-checks failed:\n  - ${problems.join("\n  - ")}`, { ...summary, problems });
    summary.ok = true;
    console.log(`\ninitializeV2 complete (record: ${L.outPath("init-v2.json")})`);
    L.printSummary(summary);
}

main().then(() => process.exit(0)).catch((e) => { console.error(e); process.exit(1); });
