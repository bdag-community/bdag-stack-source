// Read-only verification that the staking proxy is running V2 (after path A upgrade or path B
// fork-height code swap). Prints PASS/FAIL/INFO per check; exit 1 on any FAIL.
//
//   MODE=A npm run devnet:verify-v2      v2ActivatedAt must be > 0 (UUPS path, or path B after init-v2)
//   MODE=B npm run devnet:verify-v2      v2ActivatedAt reported only (fork swap before initializeV2)
//
// Env:
//   STAKING_CONTRACT_ADDRESS   proxy
//   DEVNET_POOL_ADDRESS        pool used for the stake() static calls (any non-zero address works)
//   EXPECT_IMPL                impl slot must equal this address (else reported)
//   EXPECT_RUNTIME_HASH        keccak256(eth_getCode(impl)) must equal this (else fork-swap.v2.json is used when impl matches its implAddress)
//   VERIFY_FROM                `from` for the stake() static calls (default: seed wallet #0 from out/devnet/seed.json, then signer[0])
//   MODE=A|B                   v2ActivatedAt expectation (unset: report only)
const L = require("./_lib");
const { ethers } = L;

const MODE = (process.env.MODE || "").toUpperCase();
const BELOW = ethers.parseEther("0.0015");
const OK = ethers.parseEther("0.002");
const FALLBACK_POOL = "0x000000000000000000000000000000000000dEaD";

async function main() {
    const provider = ethers.provider;
    const info = await L.requireProxy(provider, { mode: MODE || null });
    const V1 = await ethers.getContractFactory(L.V1_NAME);
    const V2 = await ethers.getContractFactory(L.V2_NAME);
    const ifaces = [V2.interface, V1.interface];
    const proxy = V2.attach(L.PROXY).connect(provider);
    const belowSel = L.selectorOf("BelowMinStake()");

    const checks = [];
    const values = { proxy: L.PROXY, ...info, mode: MODE || null };
    const rec = (name, status, detail) => { checks.push({ name, status, detail }); console.log(`  [${status}] ${name}${detail ? ` - ${detail}` : ""}`); };
    const pass = (name, detail) => rec(name, "PASS", detail);
    const failc = (name, detail) => rec(name, "FAIL", detail);
    const infoc = (name, detail) => rec(name, "INFO", detail);
    const assert = (name, cond, detail) => (cond ? pass(name, detail) : failc(name, detail));

    console.log(`verify-v2: proxy ${L.PROXY} on ${info.network} (chainId ${info.chainId}) mode ${MODE || "(unset)"}\n`);

    /* ---- version / MIN_STAKE ---- */
    try {
        values.version = await proxy.version();
        assert("version() == 2.0.0", values.version === "2.0.0", values.version);
    } catch (e) {
        values.version = null;
        failc("version() == 2.0.0", `reverted (${L.describeError(e, ifaces)}) -> proxy is still V1`);
    }
    values.minStake = await proxy.MIN_STAKE();
    assert("MIN_STAKE() == 2e15", values.minStake === OK, `${values.minStake}${values.minStake === ethers.parseEther("0.001") ? " (V1 value: still V1)" : ""}`);

    /* ---- implementation slot + runtime hash ---- */
    const impl = await L.implFromSlot(provider, L.PROXY);
    const implCode = await L.codeInfo(provider, impl);
    values.impl = impl;
    values.implCodeHash = implCode.hash;
    values.implCodeLength = implCode.length;
    assert("impl slot address has code", !implCode.empty, `${impl} (${implCode.length} bytes)`);
    if (process.env.EXPECT_IMPL) {
        const exp = ethers.getAddress(process.env.EXPECT_IMPL);
        values.expectImpl = exp;
        assert("impl slot == EXPECT_IMPL", L.sameAddr(impl, exp), `${impl} vs ${exp}`);
    } else {
        infoc("impl slot", `${impl}${L.sameAddr(impl, L.IMPL_V2) ? " (== DEVNET_IMPL_V2_ADDRESS)" : L.sameAddr(impl, L.IMPL_V1) ? " (== DEVNET_IMPL_V1_ADDRESS: still V1?)" : ""}`);
    }
    const fork = L.readForkSwap();
    if (process.env.EXPECT_RUNTIME_HASH) {
        const exp = process.env.EXPECT_RUNTIME_HASH.toLowerCase();
        values.expectRuntimeHash = exp;
        assert("keccak256(eth_getCode(impl)) == EXPECT_RUNTIME_HASH", implCode.hash === exp, `${implCode.hash} vs ${exp}`);
    } else if (fork && L.sameAddr(impl, fork.implAddress)) {
        values.expectRuntimeHash = fork.expectedRuntimeHash;
        assert("keccak256(eth_getCode(impl)) == fork-swap.v2.json expectedRuntimeHash", implCode.hash === String(fork.expectedRuntimeHash).toLowerCase(), `${implCode.hash} vs ${fork.expectedRuntimeHash}`);
        if (fork.expectedRuntimeLength) assert("eth_getCode(impl) length == expectedRuntimeLength", implCode.length === fork.expectedRuntimeLength, `${implCode.length} vs ${fork.expectedRuntimeLength}`);
    } else {
        infoc("impl runtime hash", `${implCode.hash} (${implCode.length} bytes)${fork ? `; fork-swap.v2.json implAddress ${fork.implAddress} differs -> not compared (UUPS-deployed impl has its own __self)` : "; no fork-swap.v2.json"}`);
    }

    /* ---- proxiableUUID: direct returns the slot, via proxy reverts ---- */
    try {
        const uuid = await V2.attach(impl).connect(provider).proxiableUUID();
        values.implProxiableUUID = uuid;
        assert("impl.proxiableUUID() == ERC1967 impl slot", uuid.toLowerCase() === L.ERC1967_IMPL_SLOT, uuid);
    } catch (e) {
        failc("impl.proxiableUUID() == ERC1967 impl slot", `reverted: ${L.describeError(e, ifaces)}`);
    }
    try {
        const uuid = await proxy.proxiableUUID();
        failc("proxy.proxiableUUID() reverts (notDelegated)", `returned ${uuid} instead of reverting`);
    } catch (e) {
        const why = L.describeError(e, ifaces);
        values.proxyProxiableUUIDRevert = why;
        pass("proxy.proxiableUUID() reverts (notDelegated)", why);
    }

    /* ---- stake() static calls: 0.0015 must revert BelowMinStake, 0.002 must succeed ---- */
    const pool = L.POOL || FALLBACK_POOL;
    if (!L.POOL) infoc("pool for stake() static calls", `DEVNET_POOL_ADDRESS unset -> using ${FALLBACK_POOL}`);
    let from = null, fromSource = null;
    const seed = L.readSeed();
    const candidates = [];
    if (process.env.VERIFY_FROM) candidates.push([ethers.getAddress(process.env.VERIFY_FROM), "VERIFY_FROM"]);
    if (seed && seed.wallets && seed.wallets[0]) candidates.push([ethers.getAddress(seed.wallets[0].address), "seed wallet #0"]);
    const signers = await ethers.getSigners();
    if (signers[0]) candidates.push([signers[0].address, "signer[0]"]);
    candidates.push([ethers.Wallet.createRandom().address, "random (unfunded: eth_call may reject the value transfer)"]);
    for (const [addr, src] of candidates) {
        const bal = await provider.getBalance(addr);
        if (bal >= OK || src.startsWith("random")) { from = addr; fromSource = src; values.fromBalance = bal; break; }
        console.log(`  (skipping ${src} ${addr}: balance ${L.fmtBDAG(bal)} < 0.002)`);
    }
    values.stakeFrom = from;
    values.stakeFromSource = fromSource;
    values.paused = await proxy.paused();
    if (values.paused) infoc("paused()", "true -> stake() static calls will revert with EnforcedPause");
    console.log(`  stake() static calls from ${from} (${fromSource}) into pool ${pool}`);

    const stakeCall = async (amount) => provider.call({ to: L.PROXY, from, value: amount, data: V2.interface.encodeFunctionData("stake", [from, amount, pool]) });
    try {
        await stakeCall(BELOW);
        values.stakeBelow = "succeeded";
        failc("stake(0.0015) reverts with BelowMinStake", "static call SUCCEEDED -> MIN_STAKE not enforced at 0.002: still V1");
    } catch (e) {
        const dec = L.decodeRevert(L.revertData(e), ifaces);
        values.stakeBelow = dec.name || dec.selector || (e.shortMessage || e.message);
        assert("stake(0.0015) reverts with BelowMinStake", dec.selector === belowSel, `${dec.name || dec.selector || (e.shortMessage || e.message)} (selector ${belowSel})`);
    }
    try {
        await stakeCall(OK);
        values.stakeOk = "succeeded";
        pass("stake(0.002) static call succeeds", "ok");
    } catch (e) {
        values.stakeOk = L.describeError(e, ifaces);
        failc("stake(0.002) static call succeeds", values.stakeOk);
    }

    /* ---- v2ActivatedAt / initializer state ---- */
    try {
        values.v2ActivatedAt = await proxy.v2ActivatedAt();
        if (MODE === "A") assert("v2ActivatedAt() > 0 (MODE=A)", values.v2ActivatedAt > 0n, String(values.v2ActivatedAt));
        else infoc(`v2ActivatedAt()${MODE === "B" ? " (MODE=B: 0 until init-v2)" : ""}`, String(values.v2ActivatedAt));
    } catch (e) {
        values.v2ActivatedAt = null;
        if (MODE === "A") failc("v2ActivatedAt() > 0 (MODE=A)", `reverted: ${L.describeError(e, ifaces)}`);
        else infoc("v2ActivatedAt()", `reverted: ${L.describeError(e, ifaces)}`);
    }
    const init = L.decodeInitializable(await provider.getStorage(L.PROXY, L.SLOT_INITIALIZABLE));
    values.initializedVersion = init.initialized;
    infoc("Initializable._initialized", `${init.initialized}${init.initialized === 2n ? " (initializeV2 ran)" : init.initialized === 1n ? " (only initialize() ran)" : ""}`);
    values.owner = await proxy.owner();
    infoc("owner()", values.owner);

    const failed = checks.filter((c) => c.status === "FAIL");
    const summary = { ok: failed.length === 0, ...values, checks, passed: checks.filter((c) => c.status === "PASS").length, failed: failed.length };
    if (failed.length) L.exitWith(L.EXIT.FAIL, `${failed.length} check(s) failed:\n  - ${failed.map((c) => `${c.name}: ${c.detail}`).join("\n  - ")}`, summary);
    console.log(`\nverify-v2: all ${summary.passed} checks passed`);
    L.printSummary(summary);
}

main().then(() => process.exit(0)).catch((e) => { console.error(e); process.exit(1); });
