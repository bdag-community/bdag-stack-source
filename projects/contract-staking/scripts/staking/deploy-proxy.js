const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const signers = await ethers.getSigners();
    if (signers.length === 0) {
        console.error("Error: No deployer account found. Please ensure PRIVATE_KEY is set in your .env file.");
        process.exit(1);
    }
    const [deployer] = signers;

    console.log("Deploying contracts with the account:", deployer.address);

    // 1. Deploy Implementation
    console.log("\n1. Deploying StakingContract implementation...");
    const StakingContract = await ethers.getContractFactory("StakingContract");
    const implementation = await StakingContract.deploy({
        gasLimit: 6000000,
        gasPrice: ethers.parseUnits("1", "gwei")
    });
    await implementation.waitForDeployment();
    const implementationAddress = await implementation.getAddress();
    console.log("Implementation deployed to:", implementationAddress);

    // 2. Prepare Initialization Data
    console.log("\n2. Preparing initialization data...");
    const initData = StakingContract.interface.encodeFunctionData("initialize");

    // 3. Deploy Proxy (ERC1967Proxy)
    console.log("\n3. Deploying ERC1967Proxy...");
    const ERC1967ProxyFactory = await ethers.getContractFactory("@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol:ERC1967Proxy");
    const proxy = await ERC1967ProxyFactory.deploy(implementationAddress, initData, {
        gasLimit: 3000000,
        gasPrice: ethers.parseUnits("1", "gwei")
    });
    await proxy.waitForDeployment();
    const proxyAddress = await proxy.getAddress();

    console.log("StakingContract Proxy deployed to:", proxyAddress);

    console.log("\nSummary:");
    console.log(`- Proxy Address: ${proxyAddress}`);
    console.log(`- Implementation: ${implementationAddress}`);

    console.log("\nNext steps:");
    console.log("1. Add STAKING_CONTRACT_ADDRESS to your .env file:");
    console.log(`   STAKING_CONTRACT_ADDRESS=${proxyAddress}`);
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
