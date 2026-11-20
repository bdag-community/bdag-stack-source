const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const signers = await ethers.getSigners();
    if (signers.length === 0) {
        console.error("Error: No deployer account found.");
        process.exit(1);
    }
    const [deployer] = signers;

    console.log("Upgrading contract with the account:", deployer.address);

    // The address of the existing proxy contract you want to upgrade
    const proxyAddress = process.env.STAKING_CONTRACT_ADDRESS || process.env.PROXY_ADDRESS;
    if (!proxyAddress) {
        console.error("Error: STAKING_CONTRACT_ADDRESS or PROXY_ADDRESS must be set in .env");
        process.exit(1);
    }

    console.log(`Target Proxy to Upgrade: ${proxyAddress}`);

    // 1. Deploy the NEW Implementation
    console.log("\n1. Deploying NEW StakingContract implementation...");
    const StakingContract = await ethers.getContractFactory("StakingContract");
    const newImplementation = await StakingContract.deploy({
        gasPrice: ethers.parseUnits("1", "gwei")
    });
    await newImplementation.waitForDeployment();

    const newImplementationAddress = await newImplementation.getAddress();
    console.log("-> New Implementation deployed to:", newImplementationAddress);

    // 2. Perform the Upgrade
    console.log("\n2. Calling upgradeToAndCall on the Proxy...");

    // Attach the factory to the EXISTING proxy address so we can call its UUPS functions
    const proxyContract = StakingContract.attach(proxyAddress);

    // Note: Since this is a UUPS proxy, the `upgradeToAndCall` function is actually
    // embedded in the implementation and exposed through the proxy fallback.
    // It requires the caller to be the Owner address defined in the contract.
    const upgradeTx = await proxyContract.upgradeToAndCall(
        newImplementationAddress,
        "0x", // No initialization data needed for this upgrade
        { gasLimit: 2000000 }
    );
    console.log(`-> Upgrade transaction sent: ${upgradeTx.hash}`);
    await upgradeTx.wait();
    console.log("✅ Upgrade Successful!");

    console.log("\nSummary:");
    console.log(`- Proxy Address Remains the Same: ${proxyAddress}`);
    console.log(`- New Implementation Address    : ${newImplementationAddress}`);
    console.log("- The new functions (currentEpoch, timeToNextEpoch) are now available!");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
