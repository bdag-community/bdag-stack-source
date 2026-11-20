const { expect } = require("chai");
const { ethers, upgrades } = require("hardhat");

describe("StakingPool - adminRescueDelegation (Multiple Inactive Pools)", function () {
    let stakingContract;
    let owner, staker1, staker2;
    let inactivePool1 = ethers.Wallet.createRandom().address;
    let inactivePool2 = ethers.Wallet.createRandom().address;
    let activePool1 = ethers.Wallet.createRandom().address;
    let activePool2 = ethers.Wallet.createRandom().address;

    beforeEach(async function () {
        [owner, staker1, staker2] = await ethers.getSigners();

        const StakingContract = await ethers.getContractFactory("StakingContract");
        stakingContract = await upgrades.deployProxy(StakingContract, [], {
            initializer: "initialize",
        });
        await stakingContract.waitForDeployment();
        
        // Stake 100 BDAG into inactivePool1 for staker1
        await stakingContract.connect(staker1).stake(
            staker1.address, 
            ethers.parseEther("100"), 
            inactivePool1, 
            { value: ethers.parseEther("100") }
        );

        // Stake 300 BDAG into inactivePool2 for staker1
        await stakingContract.connect(staker1).stake(
            staker1.address, 
            ethers.parseEther("300"), 
            inactivePool2, 
            { value: ethers.parseEther("300") }
        );
        
        // Stake 100 BDAG into inactivePool2 for staker2
        await stakingContract.connect(staker2).stake(
            staker2.address, 
            ethers.parseEther("100"), 
            inactivePool2, 
            { value: ethers.parseEther("100") }
        );
    });

    it("Should rescue delegation from multiple inactive pools evenly into active pools", async function () {
        // Run the admin rescue for both users across BOTH inactive pools
        await stakingContract.connect(owner).adminRescueDelegation(
            [staker1.address, staker2.address],
            [inactivePool1, inactivePool2],
            [activePool1, activePool2]
        );

        // Verify inactive pools have 0 for both users
        expect(await stakingContract.poolStakerShares(inactivePool1, staker1.address)).to.equal(0);
        expect(await stakingContract.poolStakerShares(inactivePool2, staker1.address)).to.equal(0);
        expect(await stakingContract.poolStakerShares(inactivePool2, staker2.address)).to.equal(0);

        // STAKER 1 Total = 400. Split into 2 active pools = 200 each.
        expect(await stakingContract.poolStakerAmounts(activePool1, staker1.address)).to.equal(ethers.parseEther("200"));
        expect(await stakingContract.poolStakerAmounts(activePool2, staker1.address)).to.equal(ethers.parseEther("200"));

        // STAKER 2 Total = 100. Split into 2 active pools = 50 each.
        expect(await stakingContract.poolStakerAmounts(activePool1, staker2.address)).to.equal(ethers.parseEther("50"));
        expect(await stakingContract.poolStakerAmounts(activePool2, staker2.address)).to.equal(ethers.parseEther("50"));
    });
});
