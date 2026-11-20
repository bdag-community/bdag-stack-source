const { expect } = require("chai");
const hre = require("hardhat");
const { ethers, upgrades } = hre;
const { time } = require("@nomicfoundation/hardhat-toolbox/network-helpers");
const { slot1, slot2, rawSlot, engineCreditPool, PERIODS } = require("./helpers");

describe("StakingContract V1 lifecycle (stake -> engine reward -> unstake -> claim)", function () {
    let owner, staker, pool, proxy, proxyAddr;
    const STAKE = ethers.parseEther("100");
    const REWARD = ethers.parseEther("10");

    beforeEach(async function () {
        [owner, staker, pool] = await ethers.getSigners();
        const V1 = await ethers.getContractFactory("StakingContract");
        proxy = await upgrades.deployProxy(V1, [], { initializer: "initialize", kind: "uups" });
        await proxy.waitForDeployment();
        proxyAddr = await proxy.getAddress();
    });

    it("initialize() sets owner, 70/30 splits and genesisTime", async function () {
        expect(await proxy.owner()).to.equal(owner.address);
        expect(await proxy.minerSplit()).to.equal(70n);
        expect(await proxy.stakerSplit()).to.equal(30n);
        expect(await proxy.genesisTime()).to.be.gt(0n);
        expect(await rawSlot(proxyAddr, 1)).to.equal(70n);
        expect(await rawSlot(proxyAddr, 2)).to.equal(30n);
    });

    it("stake mints 1:1 shares on an empty pool and updates the engine-visible slots", async function () {
        await expect(proxy.connect(staker).stake(staker.address, STAKE, pool.address, { value: STAKE }))
            .to.emit(proxy, "Staked").withArgs(staker.address, STAKE, pool.address);
        expect(await proxy.stakerPoolShares(staker.address, pool.address)).to.equal(STAKE);
        expect(await proxy.poolTotalShares(pool.address)).to.equal(STAKE);
        expect(await proxy.poolTotalAssets(pool.address)).to.equal(STAKE);
        expect(await proxy.poolTotalStakers(pool.address)).to.equal(1n);
        // engine view
        expect(await rawSlot(proxyAddr, slot2(staker.address, pool.address, 3))).to.equal(STAKE);
        expect(await rawSlot(proxyAddr, slot1(pool.address, 5))).to.equal(STAKE);
        expect(await rawSlot(proxyAddr, slot1(pool.address, 6))).to.equal(STAKE);
        expect(await rawSlot(proxyAddr, slot1(pool.address, 13))).to.equal(1n);
        expect(await rawSlot(proxyAddr, slot2(staker.address, pool.address, 14))).to.equal(STAKE);
        expect(await rawSlot(proxyAddr, slot1(pool.address, 15))).to.equal(STAKE);
    });

    it("rejects stakes below MIN_STAKE (0.001) and msg.value mismatches", async function () {
        const tiny = ethers.parseEther("0.0005");
        await expect(proxy.connect(staker).stake(staker.address, tiny, pool.address, { value: tiny }))
            .to.be.revertedWithCustomError(proxy, "BelowMinStake");
        await expect(proxy.connect(staker).stake(staker.address, STAKE, pool.address, { value: STAKE - 1n }))
            .to.be.revertedWithCustomError(proxy, "AmountMismatch");
        const ok = ethers.parseEther("0.0015");
        await expect(proxy.connect(staker).stake(staker.address, ok, pool.address, { value: ok })).to.not.be.reverted;
    });

    it("engine reward (slot 5 credit) appreciates share price; yield = value - principal", async function () {
        await proxy.connect(staker).stake(staker.address, STAKE, pool.address, { value: STAKE });
        await engineCreditPool(proxyAddr, pool.address, REWARD);
        expect(await proxy.poolStakerAmounts(pool.address, staker.address)).to.equal(STAKE + REWARD);
        expect(await proxy.poolStakerYield(pool.address, staker.address)).to.equal(REWARD);
        expect(await proxy.poolStakerShares(pool.address, staker.address)).to.equal(STAKE);
    });

    it("unstake burns shares, price-locks BDAG into slot 17, profit-first principal, epoch-aligned release", async function () {
        await proxy.connect(staker).stake(staker.address, STAKE, pool.address, { value: STAKE });
        await engineCreditPool(proxyAddr, pool.address, REWARD);
        const half = ethers.parseEther("55"); // 50 principal-equivalent + 5 of the 10 profit
        const tx = await proxy.connect(staker).unstake(staker.address, half, [pool.address]);
        await expect(tx).to.emit(proxy, "Unstaked");
        const reqs = await proxy.getUnbondingRequests(staker.address);
        expect(reqs.length).to.equal(1);
        expect(reqs[0].amount).to.equal(half);
        expect(reqs[0].pool).to.equal(pool.address);
        expect(reqs[0].isClaimed).to.equal(false);
        const now = BigInt(await time.latest());
        const genesis = await proxy.genesisTime();
        const expectedRelease = now + BigInt(PERIODS.UNBONDING) + (BigInt(PERIODS.EPOCH) - ((now - genesis) % BigInt(PERIODS.EPOCH)));
        expect(reqs[0].releaseTime).to.equal(expectedRelease);
        expect(await proxy.stakerUnbondingBDAG(staker.address, pool.address)).to.equal(half);
        expect(await rawSlot(proxyAddr, slot2(staker.address, pool.address, 17))).to.equal(half);
        // profit-first: 10 profit absorbed first, so principal drops by 45 only
        expect(await proxy.stakerPrincipalBDAG(staker.address, pool.address)).to.equal(STAKE - (half - REWARD));
        expect(await proxy.poolTotalAssets(pool.address)).to.equal(STAKE + REWARD - half);
    });

    it("claimStake: 4-week gate, then matured payout, then 3-week lockdown on a fresh request", async function () {
        await proxy.connect(staker).stake(staker.address, STAKE, pool.address, { value: STAKE });
        await proxy.connect(staker).unstake(staker.address, ethers.parseEther("40"), [pool.address]);
        await expect(proxy.connect(staker).claimStake(staker.address, pool.address)).to.be.revertedWith("4-Week Gate Active");
        // 3 weeks + epoch alignment (<= 1 week) always matures within the 4-week gate
        await time.increase(PERIODS.INITIAL_BONDING + 1);
        let before = await ethers.provider.getBalance(staker.address);
        let tx = await proxy.connect(staker).claimStake(staker.address, pool.address);
        let rc = await tx.wait();
        await expect(tx).to.emit(proxy, "StakeClaimed").withArgs(staker.address, ethers.parseEther("40"), ethers.parseEther("40"));
        let after = await ethers.provider.getBalance(staker.address);
        expect(after - before + rc.gasUsed * rc.gasPrice).to.equal(ethers.parseEther("40"));
        expect((await proxy.getUnbondingRequests(staker.address)).length).to.equal(0);
        expect(await proxy.stakerUnbondingBDAG(staker.address, pool.address)).to.equal(0n);
        // a new request after the gate: total lockdown until it matures
        await proxy.connect(staker).unstake(staker.address, ethers.parseEther("10"), [pool.address]);
        await expect(proxy.connect(staker).claimStake(staker.address, pool.address)).to.be.revertedWith("Total Lockdown: 3-Week Cooldown Active");
        await time.increase(PERIODS.UNBONDING + PERIODS.EPOCH + 1);
        before = await ethers.provider.getBalance(staker.address);
        tx = await proxy.connect(staker).claimStake(staker.address, pool.address);
        rc = await tx.wait();
        after = await ethers.provider.getBalance(staker.address);
        expect(after - before + rc.gasUsed * rc.gasPrice).to.equal(ethers.parseEther("10"));
    });

    it("a new stake resets pending unbonding timers (anti-gaming)", async function () {
        await proxy.connect(staker).stake(staker.address, STAKE, pool.address, { value: STAKE });
        await proxy.connect(staker).unstake(staker.address, ethers.parseEther("10"), [pool.address]);
        const r0 = (await proxy.getUnbondingRequests(staker.address))[0].releaseTime;
        await time.increase(PERIODS.EPOCH * 2);
        await proxy.connect(staker).stake(staker.address, ethers.parseEther("1"), pool.address, { value: ethers.parseEther("1") });
        const r1 = (await proxy.getUnbondingRequests(staker.address))[0].releaseTime;
        expect(r1).to.be.gt(r0);
    });

    it("governance: only owner; ownership cannot go to a contract; splits must sum to 100", async function () {
        await expect(proxy.connect(staker).setSplitRatios(60, 40)).to.be.revertedWithCustomError(proxy, "NotOwner");
        await expect(proxy.setSplitRatios(60, 50)).to.be.revertedWithCustomError(proxy, "SumMustBe100");
        await expect(proxy.setSplitRatios(60, 40)).to.emit(proxy, "SplitRatiosUpdated").withArgs(60, 40);
        expect(await rawSlot(proxyAddr, 1)).to.equal(60n);
        await expect(proxy.transferOwnership(proxyAddr)).to.be.revertedWithCustomError(proxy, "CannotBeContract");
    });
});
