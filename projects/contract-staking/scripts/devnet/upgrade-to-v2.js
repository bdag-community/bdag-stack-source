// Path A: UUPS upgrade of the staking proxy V1 -> V2 via upgradeToAndCall(initializeV2).
//
//   DRY_RUN=1 npm run devnet:upgrade-v2   validate only (+ gas estimate when a V2 impl is available)
//   npm run devnet:upgrade-v2             deploy V2, upgrade, verify version()/v2ActivatedAt()
//
// Env:
//   STAKING_CONTRACT_ADDRESS   proxy (default 0x08Bd519F...)
//   DRY_RUN=1                  stop after OZ validation (+ estimateGas); sends nothing unless DRY_RUN_DEPLOY=1
//   DRY_RUN_DEPLOY=1           in dry-run, deploy a V2 impl on chain purely to estimate the upgrade gas
//   UPGRADE_IMPL_ADDRESS       reuse an already-deployed V2 impl instead of deploying a new one
//
// Signer[0] (DEVNET_OWNER_KEY) must equal owner(). Storage-layout validation is never skipped
// (no unsafeSkipStorageCheck): slots 0-17 are read by the Go engine by index.
const L = require("./_lib");
const { hre, ethers } = L;
const { upgrades } = hre;

const DRY_RUN = process.env.DRY_RUN === "1";

async function main() {
    const provider = ethers.provider;
    const V1 = await ethers.getContractFactory(L.V1_NAME);
    const V2 = await ethers.getContractFactory(L.V2_NAME);
    const ifaces = [V1.interface, V2.interface];
    const summary = { mode: DRY_RUN ? "dry-run" : "upgrade", proxy: L.PROXY };

    /* ---------- 1. static OZ validation (no chain needed) ---------- */
    console.log(`1. OZ upgrade validation (${L.V1_NAME} -> ${L.V2_NAME}, kind uups, storage check ON)`);
    await upgrades.validateImplementation(V2, { kind: "uups" });
    console.log("   validateImplementation(V2): ok");
    await upgrades.validateUpgrade(V1, V2, { kind: "uups" });
    console.log("   validateUpgrade(V1 -> V2): ok (storage layout compatible, append-only)");
    summary.validation = { validateImplementation: "ok", validateUpgrade: "ok" };

    /* ---------- 2. chain preconditions ---------- */
    const info = await L.requireProxy(provider, { ...summary });
    Object.assign(summary, info);
    const signers = await ethers.getSigners();
    if (!signers.length) L.exitWith(L.EXIT.PRECONDITION, "no signers: set DEVNET_OWNER_KEY", summary);
    const owner = signers[0];
    const proxyV1 = V1.attach(L.PROXY).connect(owner);
    const proxyV2 = V2.attach(L.PROXY).connect(owner);
    const onChainOwner = await proxyV1.owner();
    const oldImpl = await L.implFromSlot(provider, L.PROXY);
    const oldImplCode = await L.codeInfo(provider, oldImpl);
    const versionBefore = await (async () => { try { return await proxyV2.version(); } catch (_) { return null; } })();
    const activatedBefore = await (async () => { try { return await proxyV2.v2ActivatedAt(); } catch (_) { return null; } })();
    Object.assign(summary, { signer: owner.address, owner: onChainOwner, oldImpl, oldImplCodeHash: oldImplCode.hash, versionBefore, v2ActivatedAtBefore: activatedBefore });
    console.log(`\n2. Proxy ${L.PROXY} on ${info.network} (chainId ${info.chainId})`);
    console.log(`   owner()        ${onChainOwner}`);
    console.log(`   signer[0]      ${owner.address}`);
    console.log(`   impl slot      ${oldImpl} (codeHash ${oldImplCode.hash}, ${oldImplCode.length} bytes)`);
    console.log(`   version()      ${versionBefore === null ? "reverted (V1)" : versionBefore}`);
    console.log(`   v2ActivatedAt  ${activatedBefore === null ? "reverted (V1)" : activatedBefore}`);
    if (!L.sameAddr(onChainOwner, owner.address)) {
        L.exitWith(L.EXIT.FAIL, `signer[0] ${owner.address} is not the contract owner ${onChainOwner}; set DEVNET_OWNER_KEY to the owner key`, summary);
    }
    if (activatedBefore !== null && activatedBefore > 0n) {
        console.log(`   NOTE: v2ActivatedAt already ${activatedBefore}: initializeV2 has run; upgradeToAndCall(initializeV2) would revert with InvalidInitialization`);
    }

    const fee = await L.feeOverrides(provider);
    const initData = V2.interface.encodeFunctionData("initializeV2");
    summary.gasPrice = fee.gasPrice;
    console.log(`   gasPrice       ${ethers.formatUnits(fee.gasPrice, "gwei")} gwei`);

    /* ---------- 3. dry run: estimate and stop ---------- */
    if (DRY_RUN) {
        console.log("\n3. DRY_RUN=1 -> no upgrade will be sent");
        let implCandidate = null, source = null;
        for (const [addr, label] of [[process.env.UPGRADE_IMPL_ADDRESS, "UPGRADE_IMPL_ADDRESS"], [L.IMPL_V2, "DEVNET_IMPL_V2_ADDRESS"]]) {
            if (addr && (await L.hasCode(provider, addr))) { implCandidate = ethers.getAddress(addr); source = label; break; }
        }
        if (!implCandidate && process.env.DRY_RUN_DEPLOY === "1") {
            console.log("   DRY_RUN_DEPLOY=1 -> deploying a V2 implementation to estimate against");
            const impl = await V2.deploy({ gasLimit: 6_000_000, ...fee });
            await impl.waitForDeployment();
            implCandidate = await impl.getAddress();
            source = "deployed (DRY_RUN_DEPLOY)";
            console.log(`   V2 impl deployed at ${implCandidate}`);
        }
        summary.estimateImpl = implCandidate;
        summary.estimateImplSource = source;
        if (implCandidate) {
            try {
                const gas = await proxyV1.upgradeToAndCall.estimateGas(implCandidate, initData);
                summary.estimatedGas = gas;
                console.log(`   estimateGas upgradeToAndCall(${implCandidate}, initializeV2) from ${owner.address}: ${gas} (impl from ${source})`);
            } catch (e) {
                summary.estimateError = L.describeError(e, ifaces);
                console.log(`   estimateGas failed: ${summary.estimateError} (impl from ${source})`);
            }
        } else {
            console.log("   no V2 implementation with code available (set UPGRADE_IMPL_ADDRESS, or DRY_RUN_DEPLOY=1) -> gas estimate skipped");
        }
        summary.ok = true;
        console.log("\ndry run complete: validation passed, nothing sent");
        L.printSummary(summary);
        return;
    }

    /* ---------- 4. deploy (or reuse) the V2 implementation ---------- */
    let v2Addr;
    if (process.env.UPGRADE_IMPL_ADDRESS) {
        v2Addr = ethers.getAddress(process.env.UPGRADE_IMPL_ADDRESS);
        if (!(await L.hasCode(provider, v2Addr))) L.exitWith(L.EXIT.PRECONDITION, `UPGRADE_IMPL_ADDRESS ${v2Addr} has no code`, summary);
        console.log(`\n3. Reusing V2 implementation at ${v2Addr} (UPGRADE_IMPL_ADDRESS)`);
    } else {
        console.log("\n3. Deploying StakingContractV2 implementation");
        const impl = await V2.deploy({ gasLimit: 6_000_000, ...fee });
        const dtx = impl.deploymentTransaction();
        console.log(`   deploy tx ${dtx.hash}`);
        await impl.waitForDeployment();
        v2Addr = await impl.getAddress();
        const drc = await provider.getTransactionReceipt(dtx.hash);
        summary.deployTxHash = dtx.hash;
        summary.deployBlock = drc ? drc.blockNumber : null;
        console.log(`   V2 implementation at ${v2Addr} (block ${summary.deployBlock})`);
    }
    summary.newImpl = v2Addr;
    const newCode = await L.codeInfo(provider, v2Addr);
    summary.newImplCodeHash = newCode.hash;
    summary.newImplCodeLength = newCode.length;
    const uuid = await V2.attach(v2Addr).proxiableUUID();
    if (uuid.toLowerCase() !== L.ERC1967_IMPL_SLOT) L.exitWith(L.EXIT.FAIL, `impl.proxiableUUID() ${uuid} != ERC1967 impl slot ${L.ERC1967_IMPL_SLOT}`, summary);
    console.log(`   impl.proxiableUUID() == ERC1967 implementation slot: ok`);
    const fork = L.readForkSwap();
    if (fork && fork.expectedRuntimeLength && fork.expectedRuntimeLength !== newCode.length) {
        console.log(`   NOTE: runtime length ${newCode.length} != fork-swap.v2.json expectedRuntimeLength ${fork.expectedRuntimeLength} (different compiler settings?)`);
    }

    /* ---------- 5. upgradeToAndCall(v2, initializeV2()) ---------- */
    console.log("\n4. upgradeToAndCall(v2, initializeV2())");
    try {
        await proxyV1.upgradeToAndCall.staticCall(v2Addr, initData);
    } catch (e) {
        L.exitWith(L.EXIT.FAIL, `upgradeToAndCall would revert: ${L.describeError(e, ifaces)}`, summary);
    }
    const tx = await proxyV1.upgradeToAndCall(v2Addr, initData, { gasLimit: 2_000_000, ...fee });
    console.log(`   tx ${tx.hash} sent (nonce ${tx.nonce})`);
    const rc = await tx.wait();
    summary.txHash = rc.hash;
    summary.block = rc.blockNumber;
    summary.gasUsed = rc.gasUsed;
    if (rc.status !== 1) L.exitWith(L.EXIT.FAIL, `upgrade tx ${rc.hash} reverted in block ${rc.blockNumber}`, summary);
    console.log(`   mined in block ${rc.blockNumber}, gasUsed ${rc.gasUsed}`);

    const events = L.parseLogs(rc, ifaces);
    summary.events = events;
    const find = (name) => events.filter((e) => e.name === name);
    const upgraded = find("Upgraded"), initialized = find("Initialized"), stakingVersion = find("StakingVersion");
    for (const e of events) console.log(`   event ${e.name || "unknown"} ${e.args ? JSON.stringify(e.args) : (e.topics || []).join(",")}`);
    const problems = [];
    if (!upgraded.some((e) => L.sameAddr(e.args.implementation, v2Addr))) problems.push(`Upgraded(${v2Addr}) not emitted`);
    if (!initialized.some((e) => String(e.args.version) === "2")) problems.push("Initialized(2) not emitted");
    if (!stakingVersion.some((e) => String(e.args.version) === "2")) problems.push("StakingVersion(2) not emitted");

    /* ---------- 6. post-checks through the proxy ---------- */
    const newImplSlot = await L.implFromSlot(provider, L.PROXY);
    let version = null, activated = null;
    try { version = await proxyV2.version(); } catch (e) { problems.push(`version() reverted after upgrade: ${L.describeError(e, ifaces)}`); }
    try { activated = await proxyV2.v2ActivatedAt(); } catch (e) { problems.push(`v2ActivatedAt() reverted after upgrade: ${L.describeError(e, ifaces)}`); }
    const minStake = await proxyV2.MIN_STAKE();
    Object.assign(summary, { implSlotAfter: newImplSlot, version, v2ActivatedAt: activated, minStake, eventsFound: { Upgraded: upgraded.length, Initialized: initialized.length, StakingVersion: stakingVersion.length } });
    console.log(`\n5. Post-upgrade state`);
    console.log(`   impl slot      ${newImplSlot}`);
    console.log(`   version()      ${version}`);
    console.log(`   v2ActivatedAt  ${activated}`);
    console.log(`   MIN_STAKE      ${minStake}`);
    if (!L.sameAddr(newImplSlot, v2Addr)) problems.push(`impl slot ${newImplSlot} != new impl ${v2Addr}`);
    if (version !== "2.0.0") problems.push(`version() = ${version}, expected 2.0.0`);
    if (activated === null || activated === 0n) problems.push("v2ActivatedAt() == 0 after initializeV2");
    if (minStake !== ethers.parseEther("0.002")) problems.push(`MIN_STAKE ${minStake} != 2e15`);

    L.writeJson(L.outPath("upgrade-v2.json"), { ...summary, ok: problems.length === 0, problems, createdAt: new Date().toISOString() });
    if (problems.length) L.exitWith(L.EXIT.FAIL, `upgrade post-checks failed:\n  - ${problems.join("\n  - ")}`, { ...summary, problems });
    summary.ok = true;
    console.log(`\nupgrade complete: ${L.PROXY} ${oldImpl} -> ${v2Addr} (record: ${L.outPath("upgrade-v2.json")})`);
    L.printSummary(summary);
}

main().then(() => process.exit(0)).catch((e) => { console.error(e); process.exit(1); });
