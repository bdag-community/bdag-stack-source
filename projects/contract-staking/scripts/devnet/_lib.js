// Shared helpers for scripts/devnet/*.js (seed / snapshot / upgrade / verify / init).
//
// Conventions used by every script in this directory:
//   - ALL parameters come from environment variables. Hardhat 2.28 rejects `--` passthrough
//     args, so nothing reads process.argv.
//   - Human-readable logs, then ONE final line `JSON:{...}` that a bash E2E harness can parse.
//   - Exit codes: 0 = ok, 1 = assertion / transaction failure, 2 = precondition not met
//     (no proxy code on this network, missing env, wrong signer set).
//   - Never call hardhat_* cheat RPCs here: these scripts run against the real devnet.
require("dotenv").config();
const fs = require("fs");
const path = require("path");
const hre = require("hardhat");
const { ethers } = hre;
const { slot1, slot2, ERC1967_IMPL_SLOT } = require("../../test/helpers");

const ROOT = path.join(__dirname, "..", "..");
const OUT = path.join(ROOT, "out", "devnet");

const PROXY = ethers.getAddress(process.env.STAKING_CONTRACT_ADDRESS || "0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7");
const POOL = process.env.DEVNET_POOL_ADDRESS ? ethers.getAddress(process.env.DEVNET_POOL_ADDRESS) : null;
const IMPL_V1 = ethers.getAddress(process.env.DEVNET_IMPL_V1_ADDRESS || "0x1000000000000000000000000000000000000011");
const IMPL_V2 = ethers.getAddress(process.env.DEVNET_IMPL_V2_ADDRESS || "0x1000000000000000000000000000000000000012");

// ERC-7201 namespaces the Go engine knows (bevm/params/staking.go).
const SLOT_INITIALIZABLE = "0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00";
const SLOT_REENTRANCY = "0x9b779b17422d0df92223018b32b4d1fa46e071723d6817e2486d003becc55f00";

const V1_NAME = "StakingContract";
const V2_NAME = "StakingContractV2";

const EXIT = { OK: 0, FAIL: 1, PRECONDITION: 2 };

/* ---------- output ---------- */

function bigintReplacer(_k, v) {
    return typeof v === "bigint" ? v.toString() : v;
}

function printSummary(summary) {
    console.log("JSON:" + JSON.stringify(summary, bigintReplacer));
}

// Print a message + summary line and exit. `code` defaults to FAIL (1).
function exitWith(code, message, summary = {}) {
    if (code === EXIT.OK) console.log(message);
    else console.error((code === EXIT.PRECONDITION ? "PRECONDITION: " : "FAIL: ") + message);
    printSummary({ ok: code === EXIT.OK, exitCode: code, message, ...summary });
    process.exit(code);
}

function fmtBDAG(wei) {
    return `${ethers.formatEther(wei)} BDAG`;
}

/* ---------- files ---------- */

// Output files always go under out/devnet/ unless an absolute path is given.
function outPath(name) {
    return path.isAbsolute(name) ? name : path.join(OUT, name);
}

// Input files: absolute path, or a path relative to the cwd that exists, or a bare name under out/devnet/.
function resolveInput(name) {
    if (path.isAbsolute(name)) return name;
    const rel = path.resolve(process.cwd(), name);
    if (fs.existsSync(rel)) return rel;
    return path.join(OUT, name);
}

function readJsonIfExists(file) {
    if (!fs.existsSync(file)) return null;
    return JSON.parse(fs.readFileSync(file, "utf8"));
}

function writeJson(file, obj, mode) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, JSON.stringify(obj, bigintReplacer, 2) + "\n", mode ? { mode } : undefined);
    if (mode) fs.chmodSync(file, mode);
    return file;
}

function readSeed() {
    return readJsonIfExists(process.env.SEED_FILE ? resolveInput(process.env.SEED_FILE) : outPath("seed.json"));
}

function readForkSwap() {
    return readJsonIfExists(outPath("fork-swap.v2.json"));
}

/* ---------- chain ---------- */

// Legacy-style gas price: max(1 gwei, 2 * baseFee). London nodes accept legacy txs as long
// as gasPrice >= baseFee; doubling gives headroom for a few blocks of base-fee growth.
async function feeOverrides(provider) {
    const floor = ethers.parseUnits("1", "gwei");
    let base = 0n;
    try {
        const blk = await provider.getBlock("latest");
        if (blk && blk.baseFeePerGas != null) base = BigInt(blk.baseFeePerGas);
    } catch (_) { /* pre-London or odd node: fall through to the floor */ }
    if (base === 0n) {
        try {
            const fd = await provider.getFeeData();
            if (fd.gasPrice != null) base = BigInt(fd.gasPrice) / 2n;
        } catch (_) { /* ignore */ }
    }
    const doubled = 2n * base;
    return { gasPrice: doubled > floor ? doubled : floor };
}

async function hasCode(provider, addr, blockTag) {
    const code = await provider.getCode(addr, blockTag);
    return code !== undefined && code !== null && code !== "0x";
}

async function codeInfo(provider, addr, blockTag) {
    const code = await provider.getCode(addr, blockTag);
    const bytes = ethers.getBytes(code);
    return { hash: ethers.keccak256(code), length: bytes.length, empty: bytes.length === 0 };
}

function addressFromSlotValue(raw) {
    // raw is a 32-byte hex word; the address is the low 20 bytes.
    return ethers.getAddress(ethers.dataSlice(ethers.zeroPadValue(raw, 32), 12));
}

async function implFromSlot(provider, proxy, blockTag) {
    return addressFromSlotValue(await provider.getStorage(proxy, ERC1967_IMPL_SLOT, blockTag));
}

// Initializable namespace word: low 64 bits = _initialized, next byte = _initializing.
function decodeInitializable(raw) {
    const v = BigInt(raw);
    return { initialized: v & ((1n << 64n) - 1n), initializing: ((v >> 64n) & 0xffn) !== 0n };
}

function sameAddr(a, b) {
    return !!a && !!b && a.toLowerCase() === b.toLowerCase();
}

async function netInfo(provider) {
    const n = await provider.getNetwork();
    return { chainId: n.chainId, network: hre.network.name };
}

// Require the proxy to have code on the connected network; otherwise exit 2 gracefully.
async function requireProxy(provider, extra = {}) {
    const info = await netInfo(provider);
    if (!(await hasCode(provider, PROXY))) {
        exitWith(EXIT.PRECONDITION,
            `no contract code at proxy ${PROXY} on network "${info.network}" (chainId ${info.chainId}); ` +
            `nothing to do. Set STAKING_CONTRACT_ADDRESS and run with --network devnet.`,
            { proxy: PROXY, ...info, ...extra });
    }
    return info;
}

/* ---------- errors / logs ---------- */

// Best-effort extraction of revert data from an ethers v6 / hardhat error object.
function revertData(err, depth = 0) {
    if (!err || depth > 6) return null;
    const isHex = (v) => typeof v === "string" && /^0x[0-9a-fA-F]*$/.test(v) && v.length >= 10;
    if (isHex(err.data)) return err.data;
    if (err.data && isHex(err.data.data)) return err.data.data;
    for (const key of ["error", "info", "cause", "revert", "innerError", "originalError"]) {
        const inner = err[key];
        if (inner && typeof inner === "object") {
            const d = revertData(inner, depth + 1);
            if (d) return d;
        }
    }
    if (typeof err.message === "string") {
        const m = err.message.match(/(0x[0-9a-fA-F]{8,})/);
        if (m) return m[1];
    }
    return null;
}

// Decode revert data against a list of interfaces. Returns { name, selector, args, raw }.
function decodeRevert(data, ifaces) {
    if (!data) return { name: null, selector: null, args: null, raw: null, empty: true };
    const selector = data.slice(0, 10);
    for (const iface of ifaces) {
        try {
            const parsed = iface.parseError(data);
            if (parsed) return { name: parsed.name, selector, args: parsed.args.map((a) => (typeof a === "bigint" ? a.toString() : a)), raw: data };
        } catch (_) { /* try next */ }
    }
    return { name: null, selector, args: null, raw: data };
}

function describeError(err, ifaces) {
    const dec = decodeRevert(revertData(err), ifaces);
    if (dec.name) return `${dec.name}(${(dec.args || []).join(",")})`;
    if (dec.selector) return `unknown revert selector ${dec.selector}`;
    return (err && (err.shortMessage || err.reason || err.message)) || String(err);
}

// Parse receipt logs with several interfaces; unknown logs are kept as raw topics.
function parseLogs(receipt, ifaces) {
    const out = [];
    for (const log of receipt.logs || []) {
        let parsed = null;
        for (const iface of ifaces) {
            try {
                parsed = iface.parseLog({ topics: [...log.topics], data: log.data });
                if (parsed) break;
            } catch (_) { /* next */ }
        }
        if (parsed) {
            const args = {};
            parsed.fragment.inputs.forEach((inp, i) => { const v = parsed.args[i]; args[inp.name || String(i)] = typeof v === "bigint" ? v.toString() : v; });
            out.push({ name: parsed.name, signature: parsed.signature, address: log.address, args });
        } else {
            out.push({ name: null, address: log.address, topics: [...log.topics] });
        }
    }
    return out;
}

function selectorOf(signature) {
    return ethers.id(signature).slice(0, 10);
}

module.exports = {
    hre, ethers, ROOT, OUT, PROXY, POOL, IMPL_V1, IMPL_V2,
    ERC1967_IMPL_SLOT, SLOT_INITIALIZABLE, SLOT_REENTRANCY, V1_NAME, V2_NAME, EXIT,
    slot1, slot2,
    bigintReplacer, printSummary, exitWith, fmtBDAG,
    outPath, resolveInput, readJsonIfExists, writeJson, readSeed, readForkSwap,
    feeOverrides, hasCode, codeInfo, addressFromSlotValue, implFromSlot, decodeInitializable, sameAddr, netInfo, requireProxy,
    revertData, decodeRevert, describeError, parseLogs, selectorOf,
};
