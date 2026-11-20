const { ethers } = require("hardhat");
const fs = require("fs");

async function main() {
  // --- CONFIGURATION ---
  const CONTRACT_ADDRESS = process.env.STAKING_CONTRACT_ADDRESS;
  const ACTIVE_POOLS = process.env.ACTIVE_POOLS 
    ? process.env.ACTIVE_POOLS.split(",").map(addr => addr.trim()) 
    : [];
  const BATCH_SIZE = 50; // Recommended for gas safety
  const DATA_FILE = "rescued_stakers.json";
  // ---------------------

  if (!CONTRACT_ADDRESS || ACTIVE_POOLS.length === 0) {
    console.error("Missing CONTRACT_ADDRESS or ACTIVE_POOLS in .env");
    process.exit(1);
  }

  if (!fs.existsSync(DATA_FILE)) {
    console.error(`Data file ${DATA_FILE} not found. Run findInactiveStakers.js first.`);
    process.exit(1);
  }

  const rescuedData = JSON.parse(fs.readFileSync(DATA_FILE, "utf8"));
  const StakingContract = await ethers.getContractAt("StakingContract", CONTRACT_ADDRESS);

  // Get signer to check authorization
  const [signer] = await ethers.getSigners();
  console.log(`Executing rescue with account: ${signer.address}`);

  // 1. Collect all operations
  // We want to process pool by pool to keep transactions organized
  for (const inactivePool in rescuedData) {
    const stakers = rescuedData[inactivePool];
    if (stakers.length === 0) continue;

    console.log(`\nRescuing ${stakers.length} stakers from pool: ${inactivePool}`);
    
    // 2. Process in batches
    for (let i = 0; i < stakers.length; i += BATCH_SIZE) {
      const batch = stakers.slice(i, i + BATCH_SIZE);
      const batchNum = Math.floor(i / BATCH_SIZE) + 1;
      const totalBatches = Math.ceil(stakers.length / BATCH_SIZE);

      console.log(`  -> Sending Batch ${batchNum}/${totalBatches} (${batch.length} users)...`);

      try {
        const tx = await StakingContract.adminRescueDelegation(
          batch,
          [inactivePool],
          ACTIVE_POOLS
        );
        
        console.log(`     Transaction sent: ${tx.hash}`);
        const receipt = await tx.wait();
        console.log(`     ✔ Batch ${batchNum} confirmed. Gas used: ${receipt.gasUsed.toString()}`);
      } catch (error) {
        console.error(`     ✖ Batch ${batchNum} FAILED:`, error.message);
        // You might want to stop or log and continue
      }
    }
  }

  console.log("\n--- RESCUE MISSION COMPLETE ---");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });
