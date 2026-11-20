const { ethers } = require("hardhat");

require("dotenv").config();

async function main() {
    const address = process.env.STAKING_CONTRACT_ADDRESS || process.env.STAKING_CONTRACT || process.env.PROXY_ADDRESS;

    if (!address) {
        console.error("❌ Please provide STAKING_CONTRACT address in your .env or export it.");
        console.error("Example: export STAKING_CONTRACT=0x...");
        process.exit(1);
    }

    const [signer] = await ethers.getSigners();
    console.log(`Using signer: ${signer.address}`);

    // Attach to the contract
    const StakingPool = await ethers.getContractFactory("StakingContract");
    const staking = StakingPool.attach(address);

    // Fetch epoch details using the new view functions
    const genesisTime = await staking.genesisTime();
    const currentEpoch = await staking.currentEpoch();
    const secondsRemaining = await staking.timeToNextEpoch();

    // Format the time remaining
    const seconds = Number(secondsRemaining);
    const d = Math.floor(seconds / (3600 * 24));
    const h = Math.floor((seconds % (3600 * 24)) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = Math.floor(seconds % 60);

    console.log("\n=============================================");
    console.log("                 EPOCH STATUS                ");
    console.log("=============================================");
    console.log(`Staking Contract Genesis: ${new Date(Number(genesisTime) * 1000).toLocaleString()}`);
    console.log(`Current Epoch           : ${currentEpoch}`);
    console.log(`Time Until Next Epoch   : ${d} Days, ${h} Hours, ${m} Minutes, ${s} Seconds`);
    console.log("=============================================\n");
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
