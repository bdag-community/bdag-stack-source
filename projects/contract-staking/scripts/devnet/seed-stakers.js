// Seed N staker wallets on the devnet and stake them into the mining pool.
//
//   npm run devnet:seed            (= hardhat run scripts/devnet/seed-stakers.js --network devnet)
//
// Env (all optional unless noted):
//   STAKING_CONTRACT_ADDRESS  proxy (default 0x08Bd519F...)
//   DEVNET_POOL_ADDRESS       REQUIRED - the mining coinbase = pool to stake into
//   SEED_MNEMONIC             BIP-39 phrase; wallets are m/44'/60'/0'/0/i (default: fixed dev phrase below)
//   SEED_COUNT                number of wallets (default 5)
//   SEED_FUND_BDAG            BDAG sent from the faucet to each wallet (default 200)
//   SEED_STAKE_BDAG           BDAG each wallet stakes (default 100)
//   SEED_STAKE_GAS            gasLimit for stake txs (default 300000)
//   SEED_OUT_PREFIX           output file prefix (default "seed" -> seed.json / seed.local.json)
//
// Signer order (hardhat.config.js devnet): [0] owner, [1] faucet. The faucet funds the wallets.
// Wallet #0 stakes exactly 0.0015 BDAG: valid on V1 (MIN_STAKE 0.001), rejected on V2 (0.002).
// That is the switchover demonstration case - a BelowMinStake revert is logged as EXPECTED_ON_V2.
//
// Idempotent: funding is skipped when a wallet already holds >= half the target, staking is
// skipped when stakerPoolShares(wallet, pool) > 0.
const L = require("./_lib");
const { ethers } = L;

// Dev-only default (derived from fixed entropy; never holds real value). Override with SEED_MNEMONIC.
const DEFAULT_MNEMONIC = "vapor draw cause sunset jar bicycle prepare tip slide tape world frozen";
const COUNT = parseInt(process.env.SEED_COUNT || "5", 10);
const FUND = ethers.parseEther(process.env.SEED_FUND_BDAG || "200");
const STAKE = ethers.parseEther(process.env.SEED_STAKE_BDAG || "100");
const WALLET0_STAKE = ethers.parseEther("0.0015");
const STAKE_GAS = BigInt(process.env.SEED_STAKE_GAS || "300000");
const PREFIX = process.env.SEED_OUT_PREFIX || "seed";
const BATCH = 5;
const derivationPath = (i) => `m/44'/60'/0'/0/${i}`;

async function main() {
    const provider = ethers.provider;
    if (!Number.isInteger(COUNT) || COUNT < 1) L.exitWith(L.EXIT.PRECONDITION, `SEED_COUNT must be a positive integer (got ${process.env.SEED_COUNT})`);
    if (!L.POOL) L.exitWith(L.EXIT.PRECONDITION, "DEVNET_POOL_ADDRESS is required (the mining coinbase / pool to stake into)");
    const info = await L.requireProxy(provider, { pool: L.POOL });

    const signers = await ethers.getSigners();
    if (signers.length < 2) L.exitWith(L.EXIT.PRECONDITION, "faucet signer[1] missing: set DEVNET_FAUCET_KEY (and DEVNET_OWNER_KEY) in .env", { signers: signers.length });
    const faucet = signers[1];

    const mnemonic = (process.env.SEED_MNEMONIC || "").trim() || DEFAULT_MNEMONIC;
    if (!process.env.SEED_MNEMONIC) console.log("SEED_MNEMONIC not set -> using the fixed dev mnemonic from seed-stakers.js");
    if (!ethers.Mnemonic.isValidMnemonic(mnemonic)) L.exitWith(L.EXIT.PRECONDITION, "SEED_MNEMONIC is not a valid BIP-39 phrase");

    const wallets = [];
    for (let i = 0; i < COUNT; i++) wallets.push(ethers.HDNodeWallet.fromPhrase(mnemonic, undefined, derivationPath(i)).connect(provider));

    console.log(`network ${info.network} chainId ${info.chainId}`);
    console.log(`proxy   ${L.PROXY}`);
    console.log(`pool    ${L.POOL}`);
    console.log(`faucet  ${faucet.address} balance ${L.fmtBDAG(await provider.getBalance(faucet.address))}`);
    console.log(`wallets ${COUNT} x fund ${L.fmtBDAG(FUND)} / stake ${L.fmtBDAG(STAKE)} (wallet #0 stakes ${L.fmtBDAG(WALLET0_STAKE)})`);

    const fee = await L.feeOverrides(provider);
    console.log(`gasPrice ${ethers.formatUnits(fee.gasPrice, "gwei")} gwei`);

    const staking = await ethers.getContractAt(L.V1_NAME, L.PROXY);
    const ifaces = [staking.interface];
    const results = wallets.map((w, i) => ({ index: i, address: w.address, path: derivationPath(i), funding: null, stake: null }));
    const failures = [];

    /* ---------- 1. funding (faucet -> wallets), explicit nonces, receipts in batches of 5 ---------- */
    console.log("\n1. Funding wallets from the faucet");
    let needed = 0n;
    const toFund = [];
    for (let i = 0; i < COUNT; i++) {
        const bal = await provider.getBalance(wallets[i].address);
        const shares = await staking.stakerPoolShares(wallets[i].address, L.POOL);
        if (bal >= FUND / 2n) {
            results[i].funding = { skipped: true, reason: "balance >= half target", balance: bal };
            console.log(`  #${i} ${wallets[i].address} has ${L.fmtBDAG(bal)} -> skip funding`);
        } else if (shares > 0n) {
            // Already staked on a previous run: nothing left to pay for, do not drain the faucet on reruns.
            results[i].funding = { skipped: true, reason: "already staked", balance: bal, shares };
            console.log(`  #${i} ${wallets[i].address} already staked (${shares} shares), balance ${L.fmtBDAG(bal)} -> skip funding`);
        } else {
            toFund.push(i);
            needed += FUND;
        }
    }
    const faucetBal = await provider.getBalance(faucet.address);
    if (faucetBal < needed + ethers.parseEther("1")) {
        L.exitWith(L.EXIT.PRECONDITION, `faucet ${faucet.address} balance ${L.fmtBDAG(faucetBal)} < required ${L.fmtBDAG(needed)} + gas`, { faucet: faucet.address, needed });
    }

    let pending = [];
    const flush = async (label) => {
        for (const p of pending) {
            const rc = await p.tx.wait();
            const rec = { txHash: rc.hash, block: rc.blockNumber, status: rc.status, gasUsed: rc.gasUsed };
            p.result[label] = { ...(p.result[label] || {}), ...rec };
            if (rc.status !== 1) {
                failures.push(`${label} tx ${rc.hash} for wallet #${p.result.index} reverted`);
                console.log(`  #${p.result.index} ${label} tx ${rc.hash} REVERTED (block ${rc.blockNumber})`);
            } else {
                console.log(`  #${p.result.index} ${label} tx ${rc.hash} mined in block ${rc.blockNumber}`);
            }
        }
        pending = [];
    };

    if (toFund.length) {
        let nonce = await provider.getTransactionCount(faucet.address, "pending");
        for (const i of toFund) {
            const tx = await faucet.sendTransaction({ to: wallets[i].address, value: FUND, nonce: nonce++, gasLimit: 21000, ...fee });
            console.log(`  #${i} fund ${L.fmtBDAG(FUND)} -> ${wallets[i].address} sent ${tx.hash} (nonce ${tx.nonce})`);
            results[i].funding = { amount: FUND };
            pending.push({ tx, result: results[i] });
            if (pending.length >= BATCH) await flush("funding");
        }
        await flush("funding");
    }

    /* ---------- 2. staking (each wallet -> pool), one nonce read per sender ---------- */
    console.log("\n2. Staking into the pool");
    for (let i = 0; i < COUNT; i++) {
        const w = wallets[i];
        const amount = i === 0 ? WALLET0_STAKE : STAKE;
        const shares = await staking.stakerPoolShares(w.address, L.POOL);
        if (shares > 0n) {
            results[i].stake = { skipped: true, reason: "already staked", amount, shares };
            console.log(`  #${i} ${w.address} already has ${shares} shares -> skip staking`);
            continue;
        }
        const bal = await provider.getBalance(w.address);
        if (bal < amount + STAKE_GAS * fee.gasPrice) {
            results[i].stake = { error: "insufficient balance", balance: bal, amount };
            failures.push(`wallet #${i} balance ${L.fmtBDAG(bal)} < stake ${L.fmtBDAG(amount)} + gas`);
            console.log(`  #${i} ${w.address} balance ${L.fmtBDAG(bal)} too low to stake ${L.fmtBDAG(amount)} -> FAIL`);
            continue;
        }
        const c = staking.connect(w);
        // Static call first so a revert is decoded (BelowMinStake, EnforcedPause, ...) instead of burning gas.
        try {
            await c.stake.staticCall(w.address, amount, L.POOL, { value: amount });
        } catch (e) {
            const dec = L.decodeRevert(L.revertData(e), ifaces);
            if (i === 0 && dec.name === "BelowMinStake") {
                results[i].stake = { skipped: true, expectedOnV2: true, amount, revert: "BelowMinStake", status: "EXPECTED_ON_V2" };
                console.log(`  #0 ${w.address} stake ${L.fmtBDAG(amount)} reverted with BelowMinStake -> EXPECTED_ON_V2 (MIN_STAKE is 0.002 on V2)`);
                continue;
            }
            const why = L.describeError(e, ifaces);
            results[i].stake = { error: why, amount };
            failures.push(`wallet #${i} stake ${L.fmtBDAG(amount)} would revert: ${why}`);
            console.log(`  #${i} ${w.address} stake ${L.fmtBDAG(amount)} would revert: ${why} -> FAIL`);
            continue;
        }
        const nonce = await provider.getTransactionCount(w.address, "pending");
        const tx = await c.stake(w.address, amount, L.POOL, { value: amount, nonce, gasLimit: STAKE_GAS, type: 0, ...fee });
        console.log(`  #${i} stake ${L.fmtBDAG(amount)} from ${w.address} sent ${tx.hash} (nonce ${nonce})`);
        results[i].stake = { amount };
        pending.push({ tx, result: results[i] });
        if (pending.length >= BATCH) await flush("stake");
    }
    await flush("stake");

    // A mined-but-reverted stake for wallet #0 is still the expected V2 outcome if the reason is BelowMinStake.
    for (const r of results) {
        if (r.stake && r.stake.status === 0 && r.index === 0) {
            try { await staking.connect(wallets[0]).stake.staticCall(wallets[0].address, WALLET0_STAKE, L.POOL, { value: WALLET0_STAKE }); }
            catch (e) {
                if (L.decodeRevert(L.revertData(e), ifaces).name === "BelowMinStake") {
                    r.stake = { ...r.stake, expectedOnV2: true, revert: "BelowMinStake", status: "EXPECTED_ON_V2" };
                    failures.splice(failures.findIndex((f) => f.includes("wallet #0")), 1);
                    console.log("  #0 on-chain revert classified as BelowMinStake -> EXPECTED_ON_V2");
                }
            }
        }
    }

    /* ---------- 3. write out/devnet/seed.json (+ .local with keys) ---------- */
    const meta = { chainId: info.chainId, network: info.network, proxy: L.PROXY, pool: L.POOL, derivationPath: "m/44'/60'/0'/0/{index}", createdAt: new Date().toISOString() };
    const pub = L.writeJson(L.outPath(`${PREFIX}.json`), { ...meta, wallets: results.map((r) => ({ index: r.index, address: r.address })) });
    const local = L.writeJson(L.outPath(`${PREFIX}.local.json`), {
        ...meta, mnemonic, wallets: results.map((r, i) => ({ index: r.index, address: r.address, path: r.path, privateKey: wallets[i].privateKey })),
    }, 0o600);
    console.log(`\nwrote ${pub}\nwrote ${local} (mode 0600, contains private keys; out/ is gitignored)`);

    // Post-state for the summary.
    const post = [];
    for (const r of results) {
        post.push({ index: r.index, address: r.address, shares: await staking.stakerPoolShares(r.address, L.POOL), principal: await staking.stakerPrincipalBDAG(r.address, L.POOL), balance: await provider.getBalance(r.address) });
    }
    console.log("\nstate after seeding:");
    for (const p of post) console.log(`  #${p.index} ${p.address} shares ${p.shares} principal ${L.fmtBDAG(p.principal)} balance ${L.fmtBDAG(p.balance)}`);
    console.log(`pool ${L.POOL}: totalAssets ${L.fmtBDAG(await staking.poolTotalAssets(L.POOL))} totalShares ${await staking.poolTotalShares(L.POOL)} stakers ${await staking.poolTotalStakers(L.POOL)}`);

    const summary = {
        ok: failures.length === 0, ...info, proxy: L.PROXY, pool: L.POOL, faucet: faucet.address, count: COUNT,
        fundBDAG: ethers.formatEther(FUND), stakeBDAG: ethers.formatEther(STAKE), gasPrice: fee.gasPrice,
        wallets: results.map((r, i) => ({ ...r, shares: post[i].shares, principal: post[i].principal, balance: post[i].balance })),
        files: { seed: pub, seedLocal: local }, failures,
    };
    if (failures.length) L.exitWith(L.EXIT.FAIL, `${failures.length} wallet(s) failed:\n  - ${failures.join("\n  - ")}`, summary);
    console.log("\nseeding complete");
    L.printSummary(summary);
}

main().then(() => process.exit(0)).catch((e) => { console.error(e); process.exit(1); });
