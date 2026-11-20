// Snapshot the staking proxy state at a pinned block, or diff two snapshots and assert the
// V1 -> V2 switchover invariants.
//
//   npm run devnet:snapshot          (= hardhat run scripts/devnet/snapshot-state.js --network devnet)
//
// Snapshot mode env:
//   SNAPSHOT_LABEL     file label (default "snap") -> out/devnet/snapshot-<label>-<block>.json
//   SNAPSHOT_BLOCK     decimal block to pin (default latest-2). EVERY read uses this blockTag.
//   SNAPSHOT_STAKERS   optional comma-separated extra staker addresses
//   SEED_FILE          staker list (default out/devnet/seed.json); the pool address itself is always included
//   STAKING_CONTRACT_ADDRESS / DEVNET_POOL_ADDRESS as usual
//
// Diff mode env (both set -> no chain reads, just compare the two files):
//   SNAPSHOT_DIFF_A / SNAPSHOT_DIFF_B   paths (absolute, cwd-relative, or bare names under out/devnet/)
//   EXPECT_IMPL_CHANGED=1|0             impl slot must differ / must be equal (unset: report only)
//
// Invariants asserted in diff mode (exit 1 on violation):
//   owner/minerSplit/stakerSplit/genesisTime unchanged (views + raw slots 0,1,2,8)
//   every common staker: shares (slot 3) / principal (14) / unbonding (17) / unbonding requests unchanged
//   poolTotalShares non-decreasing (engine auto-stake mints), poolTotalAssets and proxy balance non-decreasing
//   proxy code hash unchanged, impl slot per EXPECT_IMPL_CHANGED
const L = require("./_lib");
const { ethers } = L;

const LABEL = process.env.SNAPSHOT_LABEL || "snap";
const PROXY_VIEWS = ["owner", "minerSplit", "stakerSplit", "genesisTime", "currentEpoch", "timeToNextEpoch", "paused", "MIN_STAKE",
    "MAX_UNBONDING_REQUESTS", "MAX_MULTI_UNSTAKE_LIMIT", "INITIAL_BONDING_PERIOD", "UNBONDING_PERIOD", "EPOCH_DURATION", "AUTO_STAKE_LOCK_EPOCHS"];
const V2_VIEWS = ["version", "v2ActivatedAt"]; // best-effort: revert on V1 -> null
const PROXY_SLOTS = [0, 1, 2, 8, 18];
const POOL_SLOTS = [5, 6, 13, 15];
const STAKER_SLOTS = [3, 12, 14, 17];

const str = (v) => (typeof v === "bigint" ? v.toString() : v);

async function tryCall(fn) {
    try { return str(await fn()); } catch (_) { return null; }
}

/* =============================== snapshot mode =============================== */

async function snapshot() {
    const provider = ethers.provider;
    const info = await L.requireProxy(provider, { label: LABEL });
    const latest = await provider.getBlockNumber();
    let block;
    if (process.env.SNAPSHOT_BLOCK !== undefined && process.env.SNAPSHOT_BLOCK !== "") {
        block = parseInt(process.env.SNAPSHOT_BLOCK, 10);
        if (!Number.isInteger(block) || block < 0) L.exitWith(L.EXIT.PRECONDITION, `SNAPSHOT_BLOCK must be a decimal block number (got ${process.env.SNAPSHOT_BLOCK})`);
        if (block > latest) L.exitWith(L.EXIT.PRECONDITION, `SNAPSHOT_BLOCK ${block} > latest ${latest}`);
    } else {
        block = Math.max(0, latest - 2);
    }
    const blockTag = block;
    const o = { blockTag };
    const blk = await provider.getBlock(block);
    console.log(`network ${info.network} chainId ${info.chainId}; latest ${latest}; snapshot pinned at block ${block} (${blk ? blk.hash : "?"}) label "${LABEL}"`);

    const c = await ethers.getContractAt(L.V2_NAME, L.PROXY); // superset ABI: all V1 views + version/v2ActivatedAt
    const snap = {
        meta: { label: LABEL, block, blockHash: blk ? blk.hash : null, timestamp: blk ? blk.timestamp : null, chainId: str(info.chainId), network: info.network,
            proxy: L.PROXY, pool: L.POOL, createdAt: new Date().toISOString() },
        proxy: { views: {}, raw: {}, impl: {} },
        pool: null,
        stakers: {},
    };

    /* ---- proxy: arg-less views ---- */
    for (const v of PROXY_VIEWS) snap.proxy.views[v] = str(await c[v](o));
    for (const v of V2_VIEWS) snap.proxy.views[v] = await tryCall(() => c[v](o));
    snap.proxy.balance = str(await provider.getBalance(L.PROXY, blockTag));
    for (const s of PROXY_SLOTS) snap.proxy.raw[String(s)] = await provider.getStorage(L.PROXY, s, blockTag);
    snap.proxy.raw.erc1967Impl = await provider.getStorage(L.PROXY, L.ERC1967_IMPL_SLOT, blockTag);
    snap.proxy.raw.initializable = await provider.getStorage(L.PROXY, L.SLOT_INITIALIZABLE, blockTag);
    snap.proxy.raw.reentrancy = await provider.getStorage(L.PROXY, L.SLOT_REENTRANCY, blockTag);
    const init = L.decodeInitializable(snap.proxy.raw.initializable);
    snap.proxy.initializedVersion = str(init.initialized);
    const pcode = await L.codeInfo(provider, L.PROXY, blockTag);
    snap.proxy.codeHash = pcode.hash;
    snap.proxy.codeLength = pcode.length;
    const implAddr = L.addressFromSlotValue(snap.proxy.raw.erc1967Impl);
    const icode = await L.codeInfo(provider, implAddr, blockTag);
    snap.proxy.impl = { address: implAddr, codeHash: icode.hash, codeLength: icode.length };
    snap.meta.impl = implAddr;
    console.log(`proxy owner ${snap.proxy.views.owner} splits ${snap.proxy.views.minerSplit}/${snap.proxy.views.stakerSplit} genesis ${snap.proxy.views.genesisTime} paused ${snap.proxy.views.paused} MIN_STAKE ${snap.proxy.views.MIN_STAKE}`);
    console.log(`proxy version ${snap.proxy.views.version} v2ActivatedAt ${snap.proxy.views.v2ActivatedAt} initialized=${snap.proxy.initializedVersion} balance ${L.fmtBDAG(BigInt(snap.proxy.balance))}`);
    console.log(`impl  ${implAddr} codeHash ${icode.hash} (${icode.length} bytes); proxy codeHash ${pcode.hash}`);

    /* ---- pool ---- */
    const stakers = new Map();
    if (L.POOL) {
        const pool = L.POOL;
        snap.pool = { address: pool, views: {}, raw: {} };
        snap.pool.views.poolTotalAssets = str(await c.poolTotalAssets(pool, o));
        snap.pool.views.poolTotalShares = str(await c.poolTotalShares(pool, o));
        snap.pool.views.poolTotalStake = str(await c.poolTotalStake(pool, o));
        snap.pool.views.poolTotalStakers = str(await c.poolTotalStakers(pool, o));
        for (const s of POOL_SLOTS) snap.pool.raw[String(s)] = await provider.getStorage(L.PROXY, L.slot1(pool, s), blockTag);
        snap.pool.balance = str(await provider.getBalance(pool, blockTag));
        console.log(`pool  ${pool}: assets ${L.fmtBDAG(BigInt(snap.pool.views.poolTotalAssets))} shares ${snap.pool.views.poolTotalShares} stake ${L.fmtBDAG(BigInt(snap.pool.views.poolTotalStake))} stakers ${snap.pool.views.poolTotalStakers}`);
        stakers.set(pool.toLowerCase(), { address: pool, label: "pool-operator" });
    } else {
        console.log("DEVNET_POOL_ADDRESS not set -> pool and staker sections skipped");
    }

    /* ---- stakers: seed.json wallets + SNAPSHOT_STAKERS + the pool itself ---- */
    const seed = L.readSeed();
    if (seed && Array.isArray(seed.wallets)) for (const w of seed.wallets) stakers.set(w.address.toLowerCase(), { address: ethers.getAddress(w.address), label: `seed#${w.index}` });
    else console.log(`no seed file (${process.env.SEED_FILE || L.outPath("seed.json")}) -> only the pool operator is snapshotted as a staker`);
    for (const a of (process.env.SNAPSHOT_STAKERS || "").split(",").map((s) => s.trim()).filter(Boolean)) stakers.set(a.toLowerCase(), { address: ethers.getAddress(a), label: "extra" });

    if (L.POOL) {
        const pool = L.POOL;
        for (const { address, label } of stakers.values()) {
            const s = { label, views: {}, raw: {}, requests: [] };
            s.views.stakerPoolShares = str(await c.stakerPoolShares(address, pool, o));
            s.views.stakerTotalLockedShares = str(await c.stakerTotalLockedShares(address, pool, o));
            s.views.stakerPrincipalBDAG = str(await c.stakerPrincipalBDAG(address, pool, o));
            s.views.stakerUnbondingBDAG = str(await c.stakerUnbondingBDAG(address, pool, o));
            s.views.poolStakerAmounts = str(await c.poolStakerAmounts(pool, address, o));
            s.views.poolStakerShares = str(await c.poolStakerShares(pool, address, o));
            s.views.poolStakerYield = str(await c.poolStakerYield(pool, address, o));
            s.views.userFirstDepositTimestamp = str(await c.userFirstDepositTimestamp(address, o));
            s.views.isManager = await c.isManager(address, o);
            s.requests = (await c.getUnbondingRequests(address, o)).map((r) => ({ shares: str(r.shares), releaseTime: str(r.releaseTime), pool: r.pool, isClaimed: r.isClaimed, amount: str(r.amount) }));
            for (const n of STAKER_SLOTS) s.raw[String(n)] = await provider.getStorage(L.PROXY, L.slot2(address, pool, n), blockTag);
            s.balance = str(await provider.getBalance(address, blockTag));
            snap.stakers[address] = s;
            console.log(`  ${label.padEnd(14)} ${address} shares ${s.views.stakerPoolShares} locked ${s.views.stakerTotalLockedShares} principal ${L.fmtBDAG(BigInt(s.views.stakerPrincipalBDAG))} unbonding ${L.fmtBDAG(BigInt(s.views.stakerUnbondingBDAG))} requests ${s.requests.length}`);
        }
    }

    const file = L.writeJson(L.outPath(`snapshot-${LABEL}-${block}.json`), snap);
    console.log(`\nwrote ${file}`);
    L.printSummary({ ok: true, mode: "snapshot", file, label: LABEL, block, blockHash: snap.meta.blockHash, chainId: str(info.chainId), proxy: L.PROXY, pool: L.POOL,
        impl: implAddr, implCodeHash: icode.hash, proxyCodeHash: pcode.hash, version: snap.proxy.views.version, v2ActivatedAt: snap.proxy.views.v2ActivatedAt,
        owner: snap.proxy.views.owner, balance: snap.proxy.balance, stakers: Object.keys(snap.stakers).length,
        poolTotalAssets: snap.pool ? snap.pool.views.poolTotalAssets : null, poolTotalShares: snap.pool ? snap.pool.views.poolTotalShares : null });
}

/* =============================== diff mode =============================== */

function flatten(obj, prefix = "", out = {}) {
    if (obj === null || obj === undefined) { out[prefix] = String(obj); return out; }
    if (Array.isArray(obj)) {
        if (obj.length === 0) out[prefix] = "[]";
        obj.forEach((v, i) => flatten(v, `${prefix}[${i}]`, out));
        return out;
    }
    if (typeof obj === "object") {
        for (const k of Object.keys(obj)) flatten(obj[k], prefix ? `${prefix}.${k}` : k, out);
        return out;
    }
    out[prefix] = String(obj);
    return out;
}

const get = (obj, pathStr) => pathStr.split(".").reduce((o, k) => (o == null ? undefined : o[k]), obj);
const big = (v) => (v === null || v === undefined || v === "null" ? null : BigInt(v));

function diff() {
    const fa = L.resolveInput(process.env.SNAPSHOT_DIFF_A), fb = L.resolveInput(process.env.SNAPSHOT_DIFF_B);
    const A = L.readJsonIfExists(fa), B = L.readJsonIfExists(fb);
    if (!A) L.exitWith(L.EXIT.PRECONDITION, `SNAPSHOT_DIFF_A not found: ${fa}`);
    if (!B) L.exitWith(L.EXIT.PRECONDITION, `SNAPSHOT_DIFF_B not found: ${fb}`);
    console.log(`A: ${fa} (label ${A.meta.label}, block ${A.meta.block})\nB: ${fb} (label ${B.meta.label}, block ${B.meta.block})`);
    if (A.meta.proxy !== B.meta.proxy) console.log(`WARNING: snapshots are of different proxies (${A.meta.proxy} vs ${B.meta.proxy})`);
    if (A.meta.chainId !== B.meta.chainId) console.log(`WARNING: snapshots are from different chains (${A.meta.chainId} vs ${B.meta.chainId})`);

    /* ---- field-level differences (meta excluded) ---- */
    const flatA = flatten({ proxy: A.proxy, pool: A.pool, stakers: A.stakers });
    const flatB = flatten({ proxy: B.proxy, pool: B.pool, stakers: B.stakers });
    const keys = [...new Set([...Object.keys(flatA), ...Object.keys(flatB)])].sort();
    const diffs = [];
    console.log("\nfield-level differences (A -> B):");
    for (const k of keys) {
        if (flatA[k] !== flatB[k]) {
            diffs.push({ path: k, a: flatA[k] === undefined ? "<missing>" : flatA[k], b: flatB[k] === undefined ? "<missing>" : flatB[k] });
            console.log(`  ${k}: ${flatA[k] === undefined ? "<missing>" : flatA[k]} -> ${flatB[k] === undefined ? "<missing>" : flatB[k]}`);
        }
    }
    if (!diffs.length) console.log("  (none)");

    /* ---- invariants ---- */
    const checks = [];
    const violations = [];
    const check = (name, ok, detail, level = "PASS") => {
        const status = ok ? level : "FAIL";
        checks.push({ name, status, detail });
        console.log(`  [${status}] ${name}${detail ? ` - ${detail}` : ""}`);
        if (!ok) violations.push(`${name}: ${detail}`);
    };
    const eqPath = (name, p) => {
        const a = get(A, p), b = get(B, p);
        check(name, String(a) === String(b), String(a) === String(b) ? `${a}` : `${a} -> ${b}`);
    };
    const nonDecreasing = (name, p) => {
        const a = big(get(A, p)), b = big(get(B, p));
        if (a === null || b === null) { check(name, true, "not present in both snapshots", "INFO"); return; }
        check(name, b >= a, b >= a ? `${a} -> ${b}${b > a ? " (increased)" : " (unchanged)"}` : `${a} -> ${b} DECREASED`);
    };

    console.log("\nswitchover invariants:");
    for (const v of ["owner", "minerSplit", "stakerSplit", "genesisTime"]) eqPath(`proxy.${v} unchanged`, `proxy.views.${v}`);
    for (const s of ["0", "1", "2", "8"]) eqPath(`proxy raw slot ${s} unchanged`, `proxy.raw.${s}`);

    const stakersA = Object.keys(A.stakers || {}), stakersB = new Set(Object.keys(B.stakers || {}).map((k) => k.toLowerCase()));
    // DIFF_IGNORE_ADDRESSES: comma list of stakers whose positions legitimately move between snapshots
    // (the pool operator receives engine auto-stake shares/principal every block). Reported, not asserted.
    const ignore = new Set((process.env.DIFF_IGNORE_ADDRESSES || "").split(",").map((x) => x.trim().toLowerCase()).filter(Boolean));
    let common = 0;
    for (const addr of stakersA) {
        const sb = Object.keys(B.stakers || {}).find((k) => k.toLowerCase() === addr.toLowerCase());
        if (!sb) { check(`staker ${addr} present in B`, true, "missing in B (skipped)", "WARN"); continue; }
        if (ignore.has(addr.toLowerCase())) {
            const ia = A.stakers[addr], ib = B.stakers[sb];
            check(`staker ${ia.label || ""} ${addr} ignored (DIFF_IGNORE_ADDRESSES)`, true, `shares ${ia.views.stakerPoolShares} -> ${ib.views.stakerPoolShares}, principal ${ia.views.stakerPrincipalBDAG} -> ${ib.views.stakerPrincipalBDAG}`, "INFO");
            continue;
        }
        common++;
        const a = A.stakers[addr], b = B.stakers[sb];
        const lbl = `staker ${a.label || ""} ${addr}`;
        check(`${lbl} shares unchanged`, a.views.stakerPoolShares === b.views.stakerPoolShares, `${a.views.stakerPoolShares} -> ${b.views.stakerPoolShares}`);
        check(`${lbl} principal unchanged`, a.views.stakerPrincipalBDAG === b.views.stakerPrincipalBDAG, `${a.views.stakerPrincipalBDAG} -> ${b.views.stakerPrincipalBDAG}`);
        check(`${lbl} unbonding unchanged`, a.views.stakerUnbondingBDAG === b.views.stakerUnbondingBDAG, `${a.views.stakerUnbondingBDAG} -> ${b.views.stakerUnbondingBDAG}`);
        const ra = JSON.stringify(a.requests), rb = JSON.stringify(b.requests);
        check(`${lbl} unbonding requests unchanged`, ra === rb, ra === rb ? `${a.requests.length} request(s)` : `${ra} -> ${rb}`);
        for (const n of ["3", "14", "17"]) check(`${lbl} raw slot ${n} unchanged`, a.raw[n] === b.raw[n], a.raw[n] === b.raw[n] ? "" : `${a.raw[n]} -> ${b.raw[n]}`);
        if (a.views.stakerTotalLockedShares !== b.views.stakerTotalLockedShares) check(`${lbl} locked shares (slot 12)`, true, `${a.views.stakerTotalLockedShares} -> ${b.views.stakerTotalLockedShares} (engine auto-stake; report only)`, "INFO");
    }
    check("stakers compared", common > 0 || stakersA.length === 0, `${common} common staker(s)`, common > 0 ? "PASS" : "WARN");
    for (const k of stakersB) if (!stakersA.find((a) => a.toLowerCase() === k)) console.log(`  [INFO] staker ${k} only in B`);

    if (A.pool && B.pool) {
        nonDecreasing("poolTotalShares non-decreasing (engine auto-stake mints)", "pool.views.poolTotalShares");
        nonDecreasing("poolTotalAssets non-decreasing", "pool.views.poolTotalAssets");
        // New stakes/unstakes between two snapshots are legitimate on a live devnet: report only.
        check("poolTotalStake", true, `${get(A, "pool.views.poolTotalStake")} -> ${get(B, "pool.views.poolTotalStake")}`, "INFO");
        check("poolTotalStakers", true, `${get(A, "pool.views.poolTotalStakers")} -> ${get(B, "pool.views.poolTotalStakers")}`, "INFO");
    } else {
        check("pool section", true, "missing in one snapshot (skipped)", "WARN");
    }
    nonDecreasing("proxy native balance non-decreasing", "proxy.balance");
    eqPath("proxy code hash unchanged", "proxy.codeHash");

    const implA = get(A, "proxy.impl.address"), implB = get(B, "proxy.impl.address");
    const implChanged = !L.sameAddr(implA, implB);
    const expect = process.env.EXPECT_IMPL_CHANGED;
    if (expect === "1") check("impl slot changed (EXPECT_IMPL_CHANGED=1)", implChanged, `${implA} -> ${implB}`);
    else if (expect === "0") check("impl slot unchanged (EXPECT_IMPL_CHANGED=0)", !implChanged, `${implA} -> ${implB}`);
    else check("impl slot", true, `${implA} -> ${implB} (${implChanged ? "CHANGED" : "unchanged"}; set EXPECT_IMPL_CHANGED=1|0 to assert)`, "INFO");
    check("impl code hash", true, `${get(A, "proxy.impl.codeHash")} -> ${get(B, "proxy.impl.codeHash")}`, "INFO");
    check("version()", true, `${get(A, "proxy.views.version")} -> ${get(B, "proxy.views.version")}`, "INFO");
    check("v2ActivatedAt()", true, `${get(A, "proxy.views.v2ActivatedAt")} -> ${get(B, "proxy.views.v2ActivatedAt")}`, "INFO");
    check("initialized version", true, `${get(A, "proxy.initializedVersion")} -> ${get(B, "proxy.initializedVersion")}`, "INFO");

    const summary = { ok: violations.length === 0, mode: "diff", a: fa, b: fb, blockA: A.meta.block, blockB: B.meta.block, implA, implB, implChanged,
        versionA: get(A, "proxy.views.version"), versionB: get(B, "proxy.views.version"), diffCount: diffs.length, diffs, checks, violations };
    if (violations.length) L.exitWith(L.EXIT.FAIL, `switchover invariants violated (${violations.length}):\n  - ${violations.join("\n  - ")}`, summary);
    console.log(`\nall switchover invariants hold (${diffs.length} field difference(s) reported above)`);
    L.printSummary(summary);
}

async function main() {
    if (process.env.SNAPSHOT_DIFF_A && process.env.SNAPSHOT_DIFF_B) return diff();
    if (process.env.SNAPSHOT_DIFF_A || process.env.SNAPSHOT_DIFF_B) L.exitWith(L.EXIT.PRECONDITION, "set BOTH SNAPSHOT_DIFF_A and SNAPSHOT_DIFF_B for diff mode");
    return snapshot();
}

main().then(() => process.exit(0)).catch((e) => { console.error(e); process.exit(1); });
