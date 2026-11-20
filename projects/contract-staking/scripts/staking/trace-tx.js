const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const txHash = "0x5ebf01087adaca12f242cf3c5528f9ca317d6ea18425b0b3508e59ec0e489a49";

    console.log(`Fetching details for transaction (DEVNET): ${txHash}`);
    console.log(`Using RPC: ${process.env.DEVNET_RPC_URL || "http://18.229.29.107:18545"}\n`);

    try {
        const tx = await ethers.provider.getTransaction(txHash);
        if (!tx) {
            console.error("Transaction not found on Devnet.");
            process.exit(1);
        }

        const receipt = await ethers.provider.getTransactionReceipt(txHash);

        console.log("--- TRANSACTION INFO ---");
        console.log(`From: ${tx.from}`);
        console.log(`To: ${tx.to}`);
        console.log(`Value: ${ethers.formatEther(tx.value)} BDAG`);
        console.log(`Gas Price: ${ethers.formatUnits(tx.gasPrice, 'gwei')} gwei`);

        console.log("\n--- RECEIPT INFO ---");
        console.log(`Status: ${receipt.status === 1 ? 'Success' : 'Failure'}`);
        console.log(`Gas Used: ${receipt.gasUsed.toString()}`);
        console.log(`Block Number: ${receipt.blockNumber}`);

        if (receipt.logs.length > 0) {
            console.log("\n--- LOGS ---");
            const StakingFactory = await ethers.getContractFactory("StakingContract");
            const contract = StakingFactory.attach(receipt.to);

            for (const log of receipt.logs) {
                try {
                    const parsedLog = StakingFactory.interface.parseLog(log);
                    if (parsedLog) {
                        console.log(`Event: ${parsedLog.name}`);
                        const args = parsedLog.args.toObject();
                        console.log(`Args: ${JSON.stringify(args, (key, value) => typeof value === 'bigint' ? value.toString() : value, 2)}`);

                        if (parsedLog.name === "Unstaked" || parsedLog.name === "Staked") {
                            const poolAddr = args.pool;
                            const blockNum = receipt.blockNumber;

                            // Query state at the block BEFORE the transaction
                            const poolAssets = await contract.poolTotalAssets(poolAddr, { blockTag: blockNum - 1 });
                            const poolShares = await contract.poolTotalShares(poolAddr, { blockTag: blockNum - 1 });

                            if (poolShares > 0n) {
                                const shares = args.shares ? BigInt(args.shares) : 0n;
                                const amount = (shares * poolAssets) / poolShares;
                                console.log(`\n>>> CALCULATION (at block ${blockNum - 1}) <<<`);
                                console.log(`Pool Assets: ${ethers.formatEther(poolAssets)} BDAG`);
                                console.log(`Pool Shares: ${ethers.formatEther(poolShares)} Shares`);
                                console.log(`Amount: ${ethers.formatEther(amount)} BDAG`);
                            } else {
                                console.log(`\n>>> CALCULATION <<<`);
                                console.log(`First operation or empty pool: 1:1 conversion.`);
                                console.log(`Amount: ${ethers.formatEther(args.shares || 0)} BDAG`);
                            }
                        }
                    }
                } catch (e) {
                    console.log(`Unparsed Log at address: ${log.address}`);
                }
            }
        }
    } catch (error) {
        console.error("Error communicating with RPC:", error.message);
        process.exit(1);
    }
}

main().catch(console.error);
