const { ethers } = require("hardhat");

async function main() {
    console.log("Testing Mainnet RPC connection...");
    try {
        const signers = await ethers.getSigners();
        console.log("Found signers:", signers.length);
        if (signers.length > 0) {
            console.log("Signer 0 address:", signers[0].address);
            const balance = await ethers.provider.getBalance(signers[0].address);
            console.log("Signer 0 balance:", ethers.formatEther(balance));
        }

        const network = await ethers.provider.getNetwork();
        console.log("Network:", network.chainId.toString());
        const blockNumber = await ethers.provider.getBlockNumber();
        console.log("Latest Block:", blockNumber);
    } catch (error) {
        console.error("Error details:");
        console.error(error);
        if (error.response) {
            console.error("Response body:", error.response.body);
        }
    }
}

main().catch(console.error);
