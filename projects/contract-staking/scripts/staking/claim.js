const hre = require("hardhat");

async function main() {
    const [signer] = await hre.ethers.getSigners();
    const CONTRACT_ADDRESS = process.env.STAKING_CONTRACT_ADDRESS;
    const POOL_ADDRESS = process.env.POOL_ADDRESS; // Optional, can be 0x0

    if (!CONTRACT_ADDRESS) throw new Error("STAKING_CONTRACT_ADDRESS env var not set");

    // If POOL_ADDRESS is not set, use zero address (claim pending only)
    const pool = POOL_ADDRESS || hre.ethers.ZeroAddress;

    console.log(`Claiming stake from contract ${CONTRACT_ADDRESS} (Pool: ${pool})...`);

    const StakingContract = await hre.ethers.getContractAt("StakingContract", CONTRACT_ADDRESS, signer);

    const tx = await StakingContract.claimStake(signer.address, pool);
    console.log("Transaction sent:", tx.hash);

    await tx.wait();
    console.log("Claim successful!");
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
