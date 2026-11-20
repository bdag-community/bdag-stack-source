const { expect } = require("chai");
const { ethers, upgrades } = require("hardhat");

describe("Staker Counting Logic Verification", function () {
    let stakingContract;
    let owner, staker1, staker2, staker3;
    let inactivePool1 = ethers.Wallet.createRandom().address;
    let inactivePool2 = ethers.Wallet.createRandom().address;

    beforeEach(async function () {
        [owner, staker1, staker2, staker3] = await ethers.getSigners();

        const StakingContract = await ethers.getContractFactory("StakingContract");
        stakingContract = await upgrades.deployProxy(StakingContract, [], {
            initializer: "initialize",
        });
        await stakingContract.waitForDeployment();

        // Staker 1 in both pools
        await stakingContract.connect(staker1).stake(staker1.address, ethers.parseEther("10"), inactivePool1, { value: ethers.parseEther("10") });
        await stakingContract.connect(staker1).stake(staker1.address, ethers.parseEther("10"), inactivePool2, { value: ethers.parseEther("10") });

        // Staker 2 only in pool 1
        await stakingContract.connect(staker2).stake(staker2.address, ethers.parseEther("20"), inactivePool1, { value: ethers.parseEther("20") });

        // Staker 3 only in pool 2
        await stakingContract.connect(staker3).stake(staker3.address, ethers.parseEther("30"), inactivePool2, { value: ethers.parseEther("30") });
    });

    it("Should correctly identify unique stakers across inactive pools", async function () {
        const inactivePools = [inactivePool1, inactivePool2];
        const globalUniqueStakers = new Set();

        for (const pool of inactivePools) {
            const filter = stakingContract.filters.Staked(null, null, pool);
            const events = await stakingContract.queryFilter(filter);
            const potentialStakers = [...new Set(events.map(e => e.args.staker))];

            for (const staker of potentialStakers) {
                const shares = await stakingContract.stakerPoolShares(staker, pool);
                if (shares > 0n) {
                    globalUniqueStakers.add(staker);
                }
            }
        }

        // Staker 1, 2, and 3 should be in the global set
        expect(globalUniqueStakers.size).to.equal(3);
        expect(globalUniqueStakers.has(staker1.address)).to.be.true;
        expect(globalUniqueStakers.has(staker2.address)).to.be.true;
        expect(globalUniqueStakers.has(staker3.address)).to.be.true;
    });
});
