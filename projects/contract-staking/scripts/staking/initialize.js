const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const [deployer] = await ethers.getSigners();
    const proxyAddress = process.env.STAKING_CONTRACT_ADDRESS;
    const multiSigAddress = process.env.MULTISIG_ADDRESS;
    const adminVaultAddress = process.env.ADMIN_VAULT_ADDRESS;

    if (!proxyAddress) {
        console.error("Error: STAKING_CONTRACT_ADDRESS must be set in .env");
        process.exit(1);
    }

    console.log("Checking initialization for Proxy:", proxyAddress);

    // Get the contract instance at the proxy address
    const StakingContract = await ethers.getContractAt("StakingContract", proxyAddress);

    // Check current state
    try {
        const currentOwner = await StakingContract.owner();

        console.log("Current Owner:", currentOwner);

        if (currentOwner !== ethers.ZeroAddress) {
            console.log("\n[!] Contract is ALREADY initialized.");
            return;
        }
    } catch (e) {
        console.log("Could not read state (contract might not be initialized or is invalid).");
    }

    console.log("\nAttempting to initialize...");

    try {
        const tx = await StakingContract.initialize();
        console.log("Transaction sent:", tx.hash);
        await tx.wait();
        console.log("Initialization successful!");
    } catch (error) {
        if (error.message.includes("Initializable: contract is already initialized") ||
            error.message.includes("InvalidInitialization")) {
            console.log("\n[!] Initialization failed: Contract is already initialized.");
        } else {
            console.error("\nError during initialization:", error);
        }
    }
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
