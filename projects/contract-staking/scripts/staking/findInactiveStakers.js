const { ethers } = require("hardhat");

async function main() {
  // --- CONFIGURATION ---
  // The address of your deployed StakingContract
  const CONTRACT_ADDRESS = process.env.STAKING_CONTRACT_ADDRESS;

  // Read from .env and split comma-separated addresses
  const INACTIVE_POOLS = process.env.INACTIVE_POOLS
    ? process.env.INACTIVE_POOLS.split(",").map(addr => addr.trim())
    : [];
  // ---------------------

  if (!CONTRACT_ADDRESS) {
    console.error("Please set STAKING_CONTRACT_ADDRESS in your environment.");
    process.exit(1);
  }

  if (INACTIVE_POOLS.length === 0) {
    console.warn("INACTIVE_POOLS array is empty. Please add pool addresses.");
    // For demonstration, we'll continue, but usually you'd exit.
  }

  const StakingContract = await ethers.getContractAt("StakingContract", CONTRACT_ADDRESS);

  console.log(`Scanning for stakers in ${INACTIVE_POOLS.length} inactive pools...\n`);

  let globalUniqueStakers = new Set();
  const poolResults = {};

  for (const pool of INACTIVE_POOLS) {
    const totalStakersCounter = await StakingContract.poolTotalStakers(pool);
    console.log(`Checking pool: ${pool}`);
    console.log(`  -> Contract internal counter (poolTotalStakers): ${totalStakersCounter.toString()}`);

    // 1. Get all Staked events for this pool
    // Note: Staked(address indexed staker, uint256 amount, address indexed pool)
    const filter = StakingContract.filters.Staked(null, null, pool);
    const events = await StakingContract.queryFilter(filter);

    // 2. Extract unique addresses that ever staked
    const potentialStakers = [...new Set(events.map(e => e.args.staker))];

    const activeInPool = [];
    for (const staker of potentialStakers) {
      // 3. Verify they still have shares in this specific pool
      // FIXED ORDER: stakerAddress, poolAddress
      const shares = await StakingContract.stakerPoolShares(staker, pool);
      if (shares > 0n) {
        activeInPool.push({
          address: staker,
          shares: ethers.formatEther(shares)
        });
        globalUniqueStakers.add(staker);
      }
    }

    poolResults[pool] = activeInPool;
    console.log(`  -> Found ${activeInPool.length} active stakers.\n`);
  }

  console.log("--- SUMMARY ---");
  console.log(`Total Global Unique Stakers: ${globalUniqueStakers.size}`);

  const outputData = {};
  for (const pool in poolResults) {
    outputData[pool] = poolResults[pool].map(item => item.address);
  }

  // Save to file with pool addresses as keys
  const fs = require("fs");
  fs.writeFileSync("rescued_stakers.json", JSON.stringify(outputData, null, 2));
  console.log("\nResults mapped by pool have been saved to 'rescued_stakers.json'");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });
