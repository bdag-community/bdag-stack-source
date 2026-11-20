const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const stakingContractAddress = process.env.STAKING_CONTRACT_ADDRESS;
    if (!stakingContractAddress) {
        console.error("Error: STAKING_CONTRACT_ADDRESS not found in .env");
        process.exit(1);
    }

    console.log(`Scanning for pools in StakingContract at: ${stakingContractAddress}`);
    console.log(`Network: ${hre.network.name}`);

    const StakingContract = await ethers.getContractAt("StakingContract", stakingContractAddress);

    // Scan for Staked events
    console.log("Fetching 'Staked' events...");
    const filter = StakingContract.filters.Staked();
    const events = await StakingContract.queryFilter(filter);

    const pools = new Set();
    events.forEach(event => {
        // In Staked(address indexed staker, uint256 amount, address indexed pool)
        // event.args[2] is the pool address
        pools.add(event.args[2]);
    });

    console.log("\n=============================================");
    console.log("                ACTIVE POOLS                 ");
    console.log("=============================================");

    if (pools.size === 0) {
        console.log("No pools found (no 'Staked' events detected).");
    } else {
        Array.from(pools).forEach((pool, index) => {
            console.log(`${index + 1}. ${pool}`);
        });
    }

    console.log("=============================================");
    console.log(`Total Unique Pools: ${pools.size}`);
    console.log("=============================================");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
