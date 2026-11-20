const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const address = "0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7";
    const code = await ethers.provider.getCode(address);
    console.log(`Address: ${address}`);
    console.log(`Bytecode Length: ${code.length}`);
    if (code === "0x") {
        console.log("Result: NOT DEPLOYED");
    } else {
        console.log("Result: DEPLOYED");
        console.log(`Snippet: ${code.substring(0, 66)}...`);
    }
}

main().catch(console.error);
