const { ethers } = require("hardhat");
require("dotenv").config();

async function main() {
    const signers = await ethers.getSigners();
    if (signers.length === 0) {
        console.error("Error: No signer account found.");
        process.exit(1);
    }
    const [signer] = signers;
    const count = parseInt(process.env.NONCE_BUMP_COUNT || "5");

    console.log(`Bumping nonce for account: ${signer.address}`);
    console.log(`Sending ${count} dummy transactions...`);

    for (let i = 0; i < count; i++) {
        const tx = await signer.sendTransaction({
            to: signer.address,
            value: 0
        });
        await tx.wait();
        console.log(`- Transaction ${i + 1}/${count} confirmed (Hash: ${tx.hash})`);
    }

    const finalNonce = await signer.getNonce();
    console.log(`\nSuccessfully bumped nonce. New nonce: ${finalNonce}`);
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
