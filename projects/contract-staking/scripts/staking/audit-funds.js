const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const CONTRACT_ADDRESS = process.env.STAKING_CONTRACT_ADDRESS;
    const INACTIVE_POOLS = process.env.INACTIVE_POOLS ? process.env.INACTIVE_POOLS.split(",") : [];
    const ACTIVE_POOLS = process.env.ACTIVE_POOLS ? process.env.ACTIVE_POOLS.split(",") : [];

    const StakingContract = await ethers.getContractAt("StakingContract", CONTRACT_ADDRESS);

    const actualBalance = await ethers.provider.getBalance(CONTRACT_ADDRESS);
    console.log(`\n--- CONTRACT BALANCE ---`);
    console.log(`Native BDAG Balance: ${ethers.formatEther(actualBalance)} BDAG`);

    let totalAssetsAccounted = 0n;

    console.log(`\n--- INACTIVE POOLS ASSETS ---`);
    for (const pool of INACTIVE_POOLS) {
        const assets = await StakingContract.poolTotalAssets(pool.trim());
        totalAssetsAccounted += assets;
        console.log(`${pool.trim()}: ${ethers.formatEther(assets)} BDAG`);
    }

    console.log(`\n--- ACTIVE POOLS ASSETS ---`);
    for (const pool of ACTIVE_POOLS) {
        const assets = await StakingContract.poolTotalAssets(pool.trim());
        totalAssetsAccounted += assets;
        console.log(`${pool.trim()}: ${ethers.formatEther(assets)} BDAG`);
    }

    console.log(`\n--- SUMMARY ---`);
    console.log(`Total Assets in Accounted Pools: ${ethers.formatEther(totalAssetsAccounted)} BDAG`);
    console.log(`Difference (Unallocated/Yield Buffer): ${ethers.formatEther(actualBalance - totalAssetsAccounted)} BDAG`);
}

main().catch(console.error);
