const hre = require("hardhat");

async function main() {
    // Attempt to get the address from env, or from command line arguments
    const address = process.env.FUND_ADDRESS || process.argv.find(arg => arg.startsWith("0x"));
    // Default to sending 1 native token, configurable via env
    const amountStr = process.env.FUND_AMOUNT || "100000.0";

    if (!address) {
        console.error("❌ Please provide an address to fund.");
        console.error("Usage: FUND_ADDRESS=<address> npx hardhat run scripts/utils/fund.js [--network <network>]");
        process.exit(1);
    }

    const [signer] = await hre.ethers.getSigners();
    if (!signer) {
        console.error("❌ No signer found. Please ensure PRIVATE_KEY is set in your .env file.");
        process.exit(1);
    }

    const balance = await hre.ethers.provider.getBalance(signer.address);
    console.log(`Funding from account: ${signer.address}`);
    console.log(`Account Balance: ${hre.ethers.formatEther(balance)} tokens`);

    const amount = hre.ethers.parseEther(amountStr);

    if (balance < amount) {
        console.error(`❌ Insufficient balance. Have ${hre.ethers.formatEther(balance)}, want ${amountStr}`);
        process.exit(1);
    }

    console.log(`Sending ${amountStr} tokens to ${address}...`);

    const tx = await signer.sendTransaction({
        to: address,
        value: amount
    });

    console.log(`Transaction hash: ${tx.hash}`);

    // Wait for the transaction to be mined
    const receipt = await tx.wait();
    console.log(`✅ Successfully funded ${address} in block ${receipt.blockNumber}`);
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
