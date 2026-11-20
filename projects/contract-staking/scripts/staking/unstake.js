const hre = require("hardhat");

async function main() {
    const [signer] = await hre.ethers.getSigners();
    const CONTRACT_ADDRESS = process.env.STAKING_CONTRACT_ADDRESS;
    const POOL_ADDRESS = process.env.POOL_ADDRESS;
    const AMOUNT = process.env.AMOUNT;

    if (!CONTRACT_ADDRESS) throw new Error("STAKING_CONTRACT_ADDRESS env var not set");
    if (!POOL_ADDRESS) throw new Error("POOL_ADDRESS env var not set");
    if (!AMOUNT) throw new Error("AMOUNT env var not set");

    console.log(`Unstaking ${AMOUNT} from pool ${POOL_ADDRESS} on contract ${CONTRACT_ADDRESS}...`);

    const StakingContract = await hre.ethers.getContractAt("StakingContract", CONTRACT_ADDRESS, signer);
    const amountWei = hre.ethers.parseEther(AMOUNT);

    const tx = await StakingContract.unstake(signer.address, amountWei, POOL_ADDRESS);
    console.log("Transaction sent:", tx.hash);

    await tx.wait();
    console.log("Unstake successful!");
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
