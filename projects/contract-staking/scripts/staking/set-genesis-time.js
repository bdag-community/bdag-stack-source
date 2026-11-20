const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const address = process.env.STAKING_CONTRACT_ADDRESS || process.env.STAKING_CONTRACT || process.env.PROXY_ADDRESS;

    if (!address) {
        console.error("❌ Please provide STAKING_CONTRACT_ADDRESS in your .env or export it.");
        process.exit(1);
    }

    // Get the genesis time from env
    let newGenesisTime = process.env.GENESIS_TIME;

    if (!newGenesisTime) {
        console.error("❌ Please provide the target Genesis Timestamp via GENESIS_TIME environment variable.");
        console.error("Usage: GENESIS_TIME=<TIMESTAMP> npx hardhat run scripts/staking/set-genesis-time.js --network devnet");
        console.error("Example: GENESIS_TIME=1530833717 npx hardhat run scripts/staking/set-genesis-time.js --network devnet");
        process.exit(1);
    }

    newGenesisTime = parseInt(newGenesisTime, 10);

    // Check if it's a valid timestamp (must be before or equal to current time)
    const currentTimestamp = Math.floor(Date.now() / 1000);
    if (newGenesisTime > currentTimestamp) {
        console.error(`❌ Invalid Genesis Time: ${newGenesisTime}. Genesis time cannot be in the future.`);
        process.exit(1);
    }

    const [signer] = await ethers.getSigners();
    console.log(`Using signer: ${signer.address}`);

    // Attach to the contract
    const StakingPool = await ethers.getContractFactory("StakingContract");
    const staking = StakingPool.attach(address);

    const oldGenesisTime = await staking.genesisTime();
    console.log(`\nCurrent Genesis Time : ${oldGenesisTime} (${new Date(Number(oldGenesisTime) * 1000).toLocaleString()})`);
    console.log(`Target Genesis Time  : ${newGenesisTime} (${new Date(newGenesisTime * 1000).toLocaleString()})`);
    console.log("-----------------------------------------");
    console.log("Sending transaction to set new Genesis Time...");

    try {
        const tx = await staking.setGenesisTime(newGenesisTime, { gasLimit: 200000 });
        console.log(`Sent tx: ${tx.hash}`);
        await tx.wait();
        console.log("✅ Successfully updated Genesis Time!");
    } catch (error) {
        console.error("\n❌ Transaction failed. Are you sure you are the owner?");
        console.error(error.message);
    }
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
