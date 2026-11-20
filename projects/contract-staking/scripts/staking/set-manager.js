const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const CONTRACT_ADDRESS = process.env.STAKING_CONTRACT_ADDRESS;
    const MANAGER_ADDRESS = process.env.MANAGER_ADDRESS;

    if (!CONTRACT_ADDRESS || !MANAGER_ADDRESS) {
        console.error("Missing STAKING_CONTRACT_ADDRESS or MANAGER_ADDRESS in .env");
        process.exit(1);
    }

    const signers = await ethers.getSigners();
    if (signers.length === 0) {
        console.error("Error: No deployer account found.");
        process.exit(1);
    }
    const [owner] = signers;

    console.log(`Executing transaction as Owner: ${owner.address}`);
    console.log(`Setting Manager Role for: ${MANAGER_ADDRESS}`);

    const StakingContract = await ethers.getContractAt("StakingContract", CONTRACT_ADDRESS);

    // Call the addManagers function on the StakingContract
    const tx = await StakingContract.addManagers([MANAGER_ADDRESS]);
    console.log(`Transaction sent: ${tx.hash}`);

    await tx.wait();
    console.log(`✅ Manager successfully activated!`);

    // Auto-verify
    const isNowManager = await StakingContract.isManager(MANAGER_ADDRESS);
    console.log(`Confirmation Check (isManager): ${isNowManager}`);
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
