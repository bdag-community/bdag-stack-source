const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const [signer] = await ethers.getSigners();
    const contractAddress = process.env.STAKING_CONTRACT_ADDRESS;

    if (!contractAddress) {
        console.error("Error: STAKING_CONTRACT_ADDRESS not found in .env");
        process.exit(1);
    }

    const abi = [
        "function stake(address stakerAddress, uint256 amount, address poolAddress) payable",
        "function unstake(address stakerAddress, uint256 totalAmount, address[] calldata pools) external",
        "function claimStake(address stakerAddress, address poolAddress) external",
        "function getUnbondingRequests(address _staker) view returns (tuple(uint256 shares, uint256 amount, uint256 releaseTime, address pool, bool isClaimed)[])",
        "function poolStakerAmounts(address _pool, address _staker) view returns (uint256)",
        "function stakerPrincipalBDAG(address _staker, address _pool) view returns (uint256)",
        "function poolTotalAssets(address pool) view returns (uint256)",
        "function poolTotalStake(address pool) view returns (uint256)",
        "function poolTotalShares(address pool) view returns (uint256)",
        "function poolStakerYield(address _pool, address _staker) view returns (uint256)",
        "function stakerUnbondingBDAG(address _staker, address _pool) view returns (uint256)",
        "function stakerPoolShares(address _staker, address _pool) view returns (uint256)",
        "event Staked(address indexed staker, uint256 amount, address indexed pool)",
        "event Unstaked(address indexed staker, uint256 shares, uint256 amount, address indexed pool)",
        "event StakeClaimed(address indexed staker, uint256 totalPayout, uint256 unbondingPayout)"
    ];

    const StakingContract = new ethers.Contract(contractAddress, abi, signer);
    const args = process.argv.slice(2);
    const command = args[0] || process.env.CMD;
    const cmdArgs = args.length > 0 ? args.slice(1) : (process.env.ARGS ? process.env.ARGS.split(" ") : []);

    if (!command || command === "help") {
        printHelp();
        return;
    }

    switch (command) {
        case "stake":
            await handleStake(StakingContract, signer, cmdArgs);
            break;
        case "unstake":
            await handleUnstake(StakingContract, signer, cmdArgs);
            break;
        case "claim":
            await handleClaim(StakingContract, signer, cmdArgs);
            break;
        case "status":
            await handleStatus(StakingContract, cmdArgs[0] || signer.address);
            break;
        case "pool":
            await handlePoolStatus(StakingContract, cmdArgs[0]);
            break;
        case "mystake":
            await handleMyStake(StakingContract, cmdArgs[0] || signer.address);
            break;
        case "unbondings":
            await handleUnbondings(StakingContract, cmdArgs[0] || signer.address);
            break;
        case "history":
            await handleHistory(StakingContract, cmdArgs[0] || signer.address);
            break;
        default:
            console.error(`Unknown command: ${command}`);
            printHelp();
    }
}

function printHelp() {
    console.log(`
Staking CLI Tool
----------------
Usage:
  npx hardhat run scripts/staking/staking-cli.js --network localhost -- <command> [args]

Commands:
  stake <pool> <amount_bdag>     Stake BDAG into a pool
  unstake <amount_bdag> <pool1,pool2...> Unstake from one or more pools
  claim <pool>                   Claim all matured rewards
  status [address]               View user staking status and unbonding requests
  mystake [address]              View ONLY personal principal and profit
  unbondings [address]           View ONLY unbonding requests and release times
  history [address]              View full audit log of all interactions
  pool <pool_address>            View global stats for a specific pool
    `);
}

async function handleStake(contract, signer, args) {
    if (args.length < 2) {
        console.error("Usage: stake <pool_address> <amount_bdag>");
        return;
    }
    const pool = args[0];
    const amount = ethers.parseEther(args[1]);

    console.log(`Staking ${args[1]} BDAG into ${pool}...`);
    try {
        const tx = await contract.stake(signer.address, amount, pool, { value: amount });
        await tx.wait();
        console.log("✅ Stake successful!");
    } catch (err) {
        console.error("❌ Stake failed:", err.message);
    }
}

async function handleUnstake(contract, signer, args) {
    if (args.length < 2) {
        console.error("Usage: unstake <amount_bdag> <pool1,pool2...>");
        return;
    }
    const amount = ethers.parseEther(args[0]);
    const pools = args[1].split(",");

    console.log(`Unstaking ${args[0]} BDAG from ${pools.length} pools...`);
    try {
        const tx = await contract.unstake(signer.address, amount, pools);
        await tx.wait();
        console.log("✅ Unstake requested. Check status for release time.");
    } catch (err) {
        console.error("❌ Unstake failed:", err.message);
    }
}

async function handleClaim(contract, signer, args) {
    if (args.length < 1) {
        console.error("Usage: claim <pool_address>");
        return;
    }
    const pool = args[0];

    console.log(`Claiming rewards for ${pool}...`);
    try {
        const tx = await contract.claimStake(signer.address, pool);
        await tx.wait();
        console.log("✅ Claim successful!");
    } catch (err) {
        console.error("❌ Claim failed:", err.message);
    }
}

async function handleStatus(contract, address) {
    const [signer] = await ethers.getSigners();
    console.log(`\n--- Staking Status ---`);
    console.log(`Active Wallet:    ${signer.address}`);
    if (address !== signer.address) {
        console.log(`Checking Address: ${address}`);
    }
    console.log("----------------------");

    try {
        const filter = contract.filters.Staked(address);
        const currentBlock = await ethers.provider.getBlockNumber();
        const fromBlock = Math.max(0, currentBlock - 20000);
        const events = await contract.queryFilter(filter, fromBlock);

        // Discovery: 1. From events + 2. From .env ACTIVE_POOLS + 3. Known multisig pool
        const envPools = process.env.ACTIVE_POOLS ? process.env.ACTIVE_POOLS.split(",").map(p => p.trim()) : [];
        const knownPools = ["0x4F1536FC181C541f3eF766D227373f55d03CE0bA"];
        const pools = [...new Set([...events.map(e => e.args.pool), ...envPools, ...knownPools])];

        let totalPrincipal = 0n;
        let totalValue = 0n;
        let totalUnbonding = 0n;

        console.log("\nActive Pools:");
        console.log("-------------");

        for (const pool of pools) {
            const principal = await contract.stakerPrincipalBDAG(address, pool);
            const value = await contract.poolStakerAmounts(pool, address);
            const profit = await contract.poolStakerYield(pool, address);
            
            const liquidShares = await contract.stakerPoolShares(address, pool);
            const unbondingBDAG = await contract.stakerUnbondingBDAG(address, pool);
            const totalShares = await contract.poolTotalShares(pool);
            const totalAssets = await contract.poolTotalAssets(pool);

            const liquidVal = totalShares > 0n ? (liquidShares * totalAssets / totalShares) : 0n;

            if (principal > 0n || value > 0n || unbondingBDAG > 0n) {
                totalPrincipal += principal;
                totalValue += (value + unbondingBDAG);
                
                const poolPrincipal = await contract.poolTotalStake(pool);
                const poolAssets = await contract.poolTotalAssets(pool);

                console.log(`Pool: ${pool}`);
                console.log(`  Principal:  ${ethers.formatEther(principal)} BDAG (Your initial deposit)`);
                console.log(`  Liquid:     ${ethers.formatEther(liquidVal)} BDAG (Available to Unstake)`);
                console.log(`  Unbonding:  ${ethers.formatEther(unbondingBDAG)} BDAG (Fixed - No more rewards)`);
                console.log(`  Profit:     ${ethers.formatEther(profit)} BDAG 🚀`);
                console.log(`  Total Val:  ${ethers.formatEther(value + unbondingBDAG)} BDAG`);
                console.log("");
            }
        }

        const overallProfit = totalValue > totalPrincipal ? totalValue - totalPrincipal : 0n;
        console.log("-------------");
        console.log(`OVERALL TOTAL PRINCIPAL: ${ethers.formatEther(totalPrincipal)} BDAG`);
        console.log(`OVERALL TOTAL PROFIT:    ${ethers.formatEther(overallProfit)} BDAG`);
        console.log(`OVERALL TOTAL VALUE:     ${ethers.formatEther(totalValue)} BDAG`);

        // 2. Fetch Unbonding Requests
        const requests = await contract.getUnbondingRequests(address);
        console.log("\nUnbonding Requests:");
        console.log("-------------------");

        if (requests.length === 0) {
            console.log("No unbonding requests.");
        } else {
            const now = Math.floor(Date.now() / 1000);
            requests.forEach((req, i) => {
                const release = Number(req.releaseTime);
                const status = req.isClaimed ? "✅ CLAIMED" : (now >= release ? "🔓 READY" : `⏳ LOCKED (Wait ${Math.round((release - now) / 60)} mins)`);
                console.log(`[${i}] ${ethers.formatEther(req.amount)} BDAG from ${req.pool.substring(0, 8)}...`);
                console.log(`    Status: ${status} [Release: ${new Date(release * 1000).toLocaleString()}]`);
            });
        }

    } catch (err) {
        console.error("❌ Error fetching status:", err.message);
    }
}

async function handleMyStake(contract, address) {
    const [signer] = await ethers.getSigners();
    console.log(`\n--- My Stake Summary ---`);
    console.log(`Active Wallet:    ${signer.address}`);
    if (address !== signer.address) {
        console.log(`Checking Address: ${address}`);
    }
    console.log("------------------------");

    try {
        const filter = contract.filters.Staked(address);
        const currentBlock = await ethers.provider.getBlockNumber();
        const fromBlock = Math.max(0, currentBlock - 20000);
        const events = await contract.queryFilter(filter, fromBlock);

        // Discovery: 1. From events + 2. From .env ACTIVE_POOLS + 3. Known multisig pool
        const envPools = process.env.ACTIVE_POOLS ? process.env.ACTIVE_POOLS.split(",").map(p => p.trim()) : [];
        const knownPools = ["0x4F1536FC181C541f3eF766D227373f55d03CE0bA"];
        const pools = [...new Set([...events.map(e => e.args.pool), ...envPools, ...knownPools])];

        let totalPrincipal = 0n;
        let totalValue = 0n;

        for (const pool of pools) {
            const principal = await contract.stakerPrincipalBDAG(address, pool);
            const value = await contract.poolStakerAmounts(pool, address);
            const profit = await contract.poolStakerYield(pool, address);
            
            const liquidShares = await contract.stakerPoolShares(address, pool);
            const unbondingBDAG = await contract.stakerUnbondingBDAG(address, pool);
            const totalShares = await contract.poolTotalShares(pool);
            const totalAssets = await contract.poolTotalAssets(pool);

            const liquidVal = totalShares > 0n ? (liquidShares * totalAssets / totalShares) : 0n;

            if (principal > 0n || value > 0n || unbondingBDAG > 0n) {
                totalPrincipal += principal;
                totalValue += (value + unbondingBDAG);

                console.log(`Pool: ${pool}`);
                console.log(`  Principal:  ${ethers.formatEther(principal)} BDAG`);
                console.log(`  Liquid:     ${ethers.formatEther(liquidVal)} BDAG`);
                console.log(`  Unbonding:  ${ethers.formatEther(unbondingBDAG)} BDAG (Fixed)`);
                console.log(`  Profit:     ${ethers.formatEther(profit)} BDAG 🚀`);
                console.log(`  Total Val:  ${ethers.formatEther(value + unbondingBDAG)} BDAG`);
                console.log("");
            }
        }

        const overallProfit = totalValue > totalPrincipal ? totalValue - totalPrincipal : 0n;
        console.log("-------------");
        console.log(`TOTAL PRINCIPAL: ${ethers.formatEther(totalPrincipal)} BDAG`);
        console.log(`TOTAL PROFIT:    ${ethers.formatEther(overallProfit)} BDAG`);
        console.log(`TOTAL VALUE:     ${ethers.formatEther(totalValue)} BDAG`);

    } catch (err) {
        console.error("❌ Error fetching stake:", err.message);
    }
}

async function handlePoolStatus(contract, pool) {
    if (!pool) {
        console.error("Usage: pool <pool_address>");
        return;
    }

    console.log(`\n--- Global Status for Pool: ${pool} ---`);
    try {
        const principal = await contract.poolTotalStake(pool);
        const assets = await contract.poolTotalAssets(pool);
        const shares = await contract.poolTotalShares(pool);
        const profit = assets > principal ? assets - principal : 0n;

        console.log(`Total Staked (Principal): ${ethers.formatEther(principal)} BDAG`);
        console.log(`Total Profit (Rewards):   ${ethers.formatEther(profit)} BDAG`);
        console.log(`Total Assets (Liquidity): ${ethers.formatEther(assets)} BDAG`);
        console.log(`Total Shares:            ${ethers.formatEther(shares)} Shares`);

        const price = shares > 0n ? (assets * 10n ** 18n / shares) : 10n ** 18n;
        console.log(`Current Share Price:     ${ethers.formatUnits(price, 18)} BDAG/Share`);

    } catch (err) {
        console.error("❌ Error fetching pool status:", err.message);
    }
}

async function handleUnbondings(contract, address) {
    const [signer] = await ethers.getSigners();
    console.log(`\n--- Unbonding Requests ---`);
    console.log(`Active Wallet:    ${signer.address}`);
    if (address !== signer.address) {
        console.log(`Checking Address: ${address}`);
    }
    console.log("--------------------------");
    try {
        const requests = await contract.getUnbondingRequests(address);
        if (requests.length === 0) {
            console.log("No unbonding requests found.");
            return;
        }

        const now = Math.floor(Date.now() / 1000);
        requests.forEach((req, i) => {
            const release = Number(req.releaseTime);
            const status = req.isClaimed ? "✅ CLAIMED" : (now >= release ? "🔓 READY" : `⏳ LOCKED (Wait ${Math.round((release - now) / 60)} mins)`);
            console.log(`[${i}] ${ethers.formatEther(req.amount)} BDAG from ${req.pool}`);
            console.log(`    Status: ${status}`);
            console.log(`    Release: ${new Date(release * 1000).toLocaleString()}`);
            console.log("");
        });
    } catch (err) {
        console.error("❌ Error fetching unbondings:", err.message);
    }
}

async function handleHistory(contract, address) {
    console.log(`\n--- Interaction History for: ${address} ---`);
    try {
        const currentBlock = await ethers.provider.getBlockNumber();
        const fromBlock = Math.max(0, currentBlock - 20000); // Scan last 20k blocks

        // 1. Fetch Events
        const stakeFilter = contract.filters.Staked(address);
        const unstakeFilter = contract.filters.Unstaked(address);
        const claimFilter = contract.filters.StakeClaimed(address);

        const [stakeEvents, unstakeEvents, claimEvents] = await Promise.all([
            contract.queryFilter(stakeFilter, fromBlock),
            contract.queryFilter(unstakeFilter, fromBlock),
            contract.queryFilter(claimFilter, fromBlock)
        ]);

        // 2. Combine and Sort
        const history = [
            ...stakeEvents.map(e => ({ type: "STAKE", amount: e.args.amount, pool: e.args.pool, block: e.blockNumber })),
            ...unstakeEvents.map(e => ({ type: "UNSTAKE", amount: e.args.amount, pool: e.args.pool, block: e.blockNumber })),
            ...claimEvents.map(e => ({ type: "CLAIM", amount: e.args.totalPayout, block: e.blockNumber }))
        ].sort((a, b) => a.block - b.block);

        if (history.length === 0) {
            console.log("No interactions found in the recorded history.");
            return;
        }

        let totalStaked = 0n;
        let totalUnstaked = 0n;
        let totalClaimed = 0n;

        console.log("--------------------------------------------------");
        console.log("Action    | Amount (BDAG) | Pool / Info");
        console.log("--------------------------------------------------");

        for (const item of history) {
            const typeStr = item.type.padEnd(8);
            const amountStr = ethers.formatEther(item.amount).substring(0, 10).padEnd(13);
            const poolStr = item.pool ? `Pool: ${item.pool.substring(0, 10)}...` : "Global Claim";

            console.log(`${typeStr} | ${amountStr} | ${poolStr}`);

            if (item.type === "STAKE") totalStaked += item.amount;
            if (item.type === "UNSTAKE") totalUnstaked += item.amount;
            if (item.type === "CLAIM") totalClaimed += item.amount;
        }

        console.log("--------------------------------------------------");
        console.log(`TOTAL STAKED:   ${ethers.formatEther(totalStaked)} BDAG`);
        console.log(`TOTAL UNSTAKED: ${ethers.formatEther(totalUnstaked)} BDAG`);
        console.log(`TOTAL CLAIMED:  ${ethers.formatEther(totalClaimed)} BDAG`);
        console.log("--------------------------------------------------");

    } catch (err) {
        console.error("❌ Error fetching history:", err.message);
    }
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
