const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const stakingContractAddress = process.env.STAKING_CONTRACT_ADDRESS;
    const newOwner = process.env.NEW_OWNER;

    if (!stakingContractAddress) {
        console.error("Error: STAKING_CONTRACT_ADDRESS not found in .env");
        process.exit(1);
    }

    if (!newOwner || !ethers.isAddress(newOwner)) {
        console.error("Error: NEW_OWNER not found in .env or is an invalid address");
        process.exit(1);
    }

    const signers = await ethers.getSigners();
    if (signers.length === 0) {
        console.error("Error: No signer found. Please ensure PRIVATE_KEY is set in .env");
        process.exit(1);
    }
    const [deployer] = signers;

    console.log(`Using address: ${deployer.address}`);
    console.log(`StakingContract Address: ${stakingContractAddress}`);
    console.log(`Network: ${hre.network.name}`);

    const StakingContract = await ethers.getContractAt("StakingContract", stakingContractAddress);

    // Check current owner
    const currentOwner = await StakingContract.owner();
    console.log(`Current Owner: ${currentOwner}`);

    if (currentOwner.toLowerCase() !== deployer.address.toLowerCase()) {
        console.error(`Error: Signer is not the current owner. Expected: ${currentOwner}, Actual: ${deployer.address}`);
        process.exit(1);
    }

    if (currentOwner.toLowerCase() === newOwner.toLowerCase()) {
        console.log("New owner is the same as the current owner. Nothing to do.");
        return;
    }

    console.log(`\nTransferring ownership to: ${newOwner}...`);

    const tx = await StakingContract.transferOwnership(newOwner);
    console.log(`Transaction Hash: ${tx.hash}`);

    console.log("Waiting for confirmation...");
    await tx.wait();

    console.log("✅ Ownership successfully transferred!");
    const verifiedOwner = await StakingContract.owner();
    console.log(`Verified New Owner: ${verifiedOwner}`);
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error("\n❌ Transaction failed. Check if you are the current owner or if the address is correct.");
        console.error(error.message);
        process.exit(1);
    });
