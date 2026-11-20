// Post-switchover user flow on the devnet, signed by a seed wallet (out/devnet/seed.local.json):
//   1. read the position (poolStakerAmounts / principal / shares)
//   2. unstake UNSTAKE_BDAG (default 25) from the pool -> unbonding request with epoch-aligned releaseTime
//   3. static-call claimStake and record the expected revert (4-week bonding gate / 3-week lockdown)
// Env: STAKING_CONTRACT_ADDRESS, DEVNET_POOL_ADDRESS, WALLET_INDEX (default 1), UNSTAKE_BDAG.
const hre = require("hardhat");
const { ethers } = hre;
const fs = require("fs");
const path = require("path");
require("dotenv").config();

async function main() {
    const proxyAddr = process.env.STAKING_CONTRACT_ADDRESS || "0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7";
    const pool = process.env.DEVNET_POOL_ADDRESS;
    if (!pool) throw new Error("DEVNET_POOL_ADDRESS required");
    const seedFile = path.join(__dirname, "..", "..", "out", "devnet", "seed.local.json");
    const seeds = JSON.parse(fs.readFileSync(seedFile, "utf8"));
    const list = Array.isArray(seeds) ? seeds : seeds.wallets;
    const idx = parseInt(process.env.WALLET_INDEX || "1", 10);
    const entry = list[idx];
    if (!entry || !entry.privateKey) throw new Error(`no seed wallet #${idx} with a key in ${seedFile}`);
    const wallet = new ethers.Wallet(entry.privateKey, ethers.provider);
    const c = await ethers.getContractAt("StakingContractV2", proxyAddr, wallet);
    const fee = await ethers.provider.getFeeData();
    const base = fee.gasPrice || 1_000_000_000n;
    const gasPrice = base * 2n > 1_000_000_000n ? base * 2n : 1_000_000_000n;

    const before = {
        amount: await c.poolStakerAmounts(pool, wallet.address),
        principal: await c.stakerPrincipalBDAG(wallet.address, pool),
        shares: await c.stakerPoolShares(wallet.address, pool),
        unbonding: await c.stakerUnbondingBDAG(wallet.address, pool),
        requests: (await c.getUnbondingRequests(wallet.address)).length,
    };
    console.log(`wallet #${idx} ${wallet.address} before: value=${ethers.formatEther(before.amount)} principal=${ethers.formatEther(before.principal)} shares=${before.shares} unbonding=${ethers.formatEther(before.unbonding)} requests=${before.requests}`);
    if (before.shares === 0n) throw new Error("wallet has no shares in the pool; run seed-stakers first");

    const amount = ethers.parseEther(process.env.UNSTAKE_BDAG || "25");
    const tx = await c.unstake(wallet.address, amount, [pool], { gasLimit: 600_000, gasPrice });
    console.log(`unstake ${ethers.formatEther(amount)} BDAG sent ${tx.hash}`);
    const rc = await tx.wait();
    const unstaked = rc.logs.map((l) => { try { return c.interface.parseLog(l); } catch { return null; } }).filter((e) => e && e.name === "Unstaked");
    console.log(`mined in block ${rc.blockNumber}; Unstaked events: ${unstaked.length}`);

    const reqs = await c.getUnbondingRequests(wallet.address);
    const last = reqs[reqs.length - 1];
    const blk = await ethers.provider.getBlock(rc.blockNumber);
    const genesisTime = await c.genesisTime();
    const WEEK = 7n * 24n * 3600n;
    const expectedRelease = BigInt(blk.timestamp) + 3n * WEEK + (WEEK - ((BigInt(blk.timestamp) - genesisTime) % WEEK));
    const after = {
        amount: await c.poolStakerAmounts(pool, wallet.address),
        principal: await c.stakerPrincipalBDAG(wallet.address, pool),
        shares: await c.stakerPoolShares(wallet.address, pool),
        unbonding: await c.stakerUnbondingBDAG(wallet.address, pool),
        requests: reqs.length,
    };
    console.log(`after: value=${ethers.formatEther(after.amount)} principal=${ethers.formatEther(after.principal)} shares=${after.shares} unbonding=${ethers.formatEther(after.unbonding)} requests=${after.requests}`);
    console.log(`last request: amount=${ethers.formatEther(last.amount)} releaseTime=${last.releaseTime} expected=${expectedRelease} pool=${last.pool} claimed=${last.isClaimed}`);

    let claimRevert = null;
    try { await c.claimStake.staticCall(wallet.address, pool); claimRevert = "NO_REVERT"; }
    catch (e) { claimRevert = (e.reason || e.shortMessage || String(e.message)).slice(0, 120); }
    console.log(`claimStake static call: ${claimRevert}`);

    const ok = unstaked.length === 1 && after.shares < before.shares && after.unbonding === before.unbonding + last.amount && last.releaseTime === expectedRelease && claimRevert !== "NO_REVERT";
    const summary = { ok, wallet: wallet.address, unstakeTx: tx.hash, block: rc.blockNumber, unstakedEvents: unstaked.length,
        sharesBefore: before.shares.toString(), sharesAfter: after.shares.toString(), unbondingAfter: after.unbonding.toString(),
        requestAmount: last.amount.toString(), releaseTime: last.releaseTime.toString(), expectedRelease: expectedRelease.toString(), claimRevert };
    console.log("JSON:" + JSON.stringify(summary));
    if (!ok) process.exit(1);
}
main().catch((e) => { console.error(e); process.exit(1); });
