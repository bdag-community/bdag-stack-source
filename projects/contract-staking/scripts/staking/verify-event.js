const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const CONTRACT_ADDRESS = "0xd807D8c52f65a2b8e51c3c999Dd78f6F54462692";
    const POOL_ADDRESS = "0x39db56146c34d752bf6967970a69e913db6a7472"; // One of the active pools

    const StakingContract = await ethers.getContractAt("StakingContract", CONTRACT_ADDRESS);
    const [owner] = await ethers.getSigners();

    console.log(`Checking shares for ${owner.address} in pool ${POOL_ADDRESS}...`);
    const shares = await StakingContract.stakerPoolShares(owner.address, POOL_ADDRESS);
    console.log(`Current Shares: ${ethers.formatEther(shares)}`);

    if (shares === 0n) {
        console.log("No shares to unstake. Performing a small stake first...");
        const stakeTx = await StakingContract.stake(owner.address, ethers.parseEther("10"), POOL_ADDRESS, { value: ethers.parseEther("10") });
        await stakeTx.wait();
        console.log("Stake successful.");
    }

    console.log("Performing unstake of 1 BDAG...");
    const unstakeTx = await StakingContract.unstake(owner.address, ethers.parseEther("1"), POOL_ADDRESS);
    const receipt = await unstakeTx.wait();

    console.log("\n--- EVENT LOGS ---");
    for (const log of receipt.logs) {
        try {
            const parsedLog = StakingContract.interface.parseLog(log);
            if (parsedLog && parsedLog.name === "Unstaked") {
                console.log(`Event Found: ${parsedLog.name}`);
                const args = parsedLog.args.toObject();
                console.log(`Args: ${JSON.stringify(args, (key, value) => typeof value === 'bigint' ? value.toString() : value, 2)}`);

                if (args.amount !== undefined) {
                    console.log(`\n✅ SUCCESS: Amount field found in event: ${ethers.formatEther(args.amount)} BDAG`);
                } else {
                    console.log("\n❌ FAILURE: Amount field missing from event!");
                }
            }
        } catch (e) {
            // Ignored
        }
    }
}

main().catch(console.error);
