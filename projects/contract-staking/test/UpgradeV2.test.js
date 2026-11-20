const { expect } = require("chai");
const hre = require("hardhat");
const { ethers, upgrades } = hre;
const { time } = require("@nomicfoundation/hardhat-toolbox/network-helpers");
const { slot1, slot2, rawSlot, engineCreditPool, ERC1967_IMPL_SLOT, PERIODS } = require("./helpers");

// This retains the original local UUPS compatibility regression. The live
// compromised-owner transition is fork-only; no owner signature is assumed.
describe("Local UUPS compatibility V1 -> V2 preserves state", function () {
    let owner, s1, s2, manager, pool, stranger, proxy, proxyAddr, v1Impl, V2;
    const A = ethers.parseEther("100"), B = ethers.parseEther("40"), R = ethers.parseEther("7");

    async function snapshot() {
        const out = {};
        out.owner = await proxy.owner();
        out.minerSplit = await proxy.minerSplit();
        out.stakerSplit = await proxy.stakerSplit();
        out.genesisTime = await proxy.genesisTime();
        out.paused = await proxy.paused();
        out.isManager = await proxy.isManager(manager.address);
        out.poolTotalAssets = await proxy.poolTotalAssets(pool.address);
        out.poolTotalShares = await proxy.poolTotalShares(pool.address);
        out.poolTotalStake = await proxy.poolTotalStake(pool.address);
        out.poolTotalStakers = await proxy.poolTotalStakers(pool.address);
        for (const [k, s] of [["s1", s1], ["s2", s2]]) {
            out[k] = {
                shares: await proxy.stakerPoolShares(s.address, pool.address),
                principal: await proxy.stakerPrincipalBDAG(s.address, pool.address),
                unbonding: await proxy.stakerUnbondingBDAG(s.address, pool.address),
                amount: await proxy.poolStakerAmounts(pool.address, s.address),
                yield: await proxy.poolStakerYield(pool.address, s.address),
                firstDeposit: await proxy.userFirstDepositTimestamp(s.address),
                requests: (await proxy.getUnbondingRequests(s.address)).map((r) => [r.shares, r.releaseTime, r.pool, r.isClaimed, r.amount]),
            };
        }
        out.balance = await ethers.provider.getBalance(proxyAddr);
        out.raw = {};
        for (const n of [0, 1, 2, 8]) out.raw[n] = await rawSlot(proxyAddr, n);
        for (const n of [5, 6, 13, 15]) out.raw[`pool${n}`] = await rawSlot(proxyAddr, slot1(pool.address, n));
        for (const n of [3, 12, 14, 17]) out.raw[`s1_${n}`] = await rawSlot(proxyAddr, slot2(s1.address, pool.address, n));
        return out;
    }

    beforeEach(async function () {
        [owner, s1, s2, manager, pool, stranger] = await ethers.getSigners();
        const V1 = await ethers.getContractFactory("StakingContract");
        V2 = await ethers.getContractFactory("StakingContractV2");
        proxy = await upgrades.deployProxy(V1, [], { initializer: "initialize", kind: "uups" });
        await proxy.waitForDeployment();
        proxyAddr = await proxy.getAddress();
        v1Impl = await upgrades.erc1967.getImplementationAddress(proxyAddr);
        // seed realistic state
        await proxy.connect(s1).stake(s1.address, A, pool.address, { value: A });
        await proxy.connect(s2).stake(s2.address, B, pool.address, { value: B });
        await engineCreditPool(proxyAddr, pool.address, R);
        await proxy.connect(s2).unstake(s2.address, ethers.parseEther("10"), [pool.address]);
        await proxy.addManagers([manager.address]);
        await proxy.setSplitRatios(65, 35);
    });

    it("validateUpgrade + upgradeProxy(call initializeV2): every view and raw slot identical; V2 API live", async function () {
        const before = await snapshot();
        const V1 = await ethers.getContractFactory("StakingContract");
        await upgrades.validateUpgrade(V1, V2, { kind: "uups" });
        const upgraded = await upgrades.upgradeProxy(proxyAddr, V2, { kind: "uups", call: { fn: "initializeV2" } });
        proxy = upgraded; // same address, V2 ABI
        expect(await proxy.getAddress()).to.equal(proxyAddr);
        const v2Impl = await upgrades.erc1967.getImplementationAddress(proxyAddr);
        expect(v2Impl).to.not.equal(v1Impl);
        expect(ethers.getAddress("0x" + (await ethers.provider.getStorage(proxyAddr, ERC1967_IMPL_SLOT)).slice(26))).to.equal(v2Impl);

        const after = await snapshot();
        expect(after).to.deep.equal(before);

        expect(await proxy.version()).to.equal("2.0.0");
        expect(await proxy.MIN_STAKE()).to.equal(ethers.parseEther("0.001"));
        expect(await proxy.minStake()).to.equal(ethers.parseEther("0.001"));
        expect(await proxy.v2ActivatedAt()).to.be.gt(0n);
        expect(await rawSlot(proxyAddr, 18)).to.equal(await proxy.v2ActivatedAt());
    });

    it("upgradeToAndCall(initializeV2) emits Upgraded, Initialized(2), StakingVersion(2); reinit is one-shot", async function () {
        const impl = await V2.deploy();
        await impl.waitForDeployment();
        const implAddr = await impl.getAddress();
        const data = V2.interface.encodeFunctionData("initializeV2");
        const tx = await proxy.upgradeToAndCall(implAddr, data);
        const v2 = V2.attach(proxyAddr);
        await expect(tx).to.emit(proxy, "Upgraded").withArgs(implAddr);
        await expect(tx).to.emit(v2, "Initialized").withArgs(2);
        await expect(tx).to.emit(v2, "StakingVersion").withArgs(2);
        await expect(v2.initializeV2()).to.be.revertedWithCustomError(v2, "InvalidInitialization");
        // proxiableUUID: via impl returns the slot; via proxy must revert (notDelegated)
        expect(await impl.proxiableUUID()).to.equal(ERC1967_IMPL_SLOT);
        await expect(v2.proxiableUUID()).to.be.revertedWithCustomError(v2, "UUPSUnauthorizedCallContext");
    });

    it("restores the V1 default minimum and enforces a governed override without changing the fallback", async function () {
        const amt = ethers.parseEther("0.0015");
        await expect(proxy.connect(stranger).stake(stranger.address, amt, pool.address, { value: amt })).to.not.be.reverted;
        proxy = await upgrades.upgradeProxy(proxyAddr, V2, { kind: "uups", call: { fn: "initializeV2" } });
        await expect(proxy.connect(stranger).stake(stranger.address, amt, pool.address, { value: amt })).not.to.be.reverted;
        await proxy.setMinStake(ethers.parseEther("0.002"));
        await expect(proxy.connect(stranger).stake(stranger.address, amt, pool.address, { value: amt }))
            .to.be.revertedWithCustomError(proxy, "BelowMinStake");
        const ok = ethers.parseEther("0.002");
        await expect(proxy.connect(stranger).stake(stranger.address, ok, pool.address, { value: ok })).to.emit(proxy, "Staked");
        expect(await proxy.MIN_STAKE()).to.equal(ethers.parseEther("0.001"));
        expect(await proxy.minStake()).to.equal(ok);
    });

    it("access control: non-owner cannot upgrade; bare V2 impl rejects initialize() and upgradeToAndCall()", async function () {
        const impl = await V2.deploy();
        await impl.waitForDeployment();
        await expect(proxy.connect(stranger).upgradeToAndCall(await impl.getAddress(), "0x")).to.be.revertedWithCustomError(proxy, "NotOwner");
        await expect(impl.initialize()).to.be.revertedWithCustomError(impl, "InvalidInitialization");
        await expect(impl.initializeV2()).to.be.revertedWithCustomError(impl, "InvalidInitialization");
        await expect(impl.upgradeToAndCall(await impl.getAddress(), "0x")).to.be.revertedWithCustomError(impl, "UUPSUnauthorizedCallContext");
    });

    it("pre-upgrade unbonding request still claims correctly on V2 after the gates elapse", async function () {
        proxy = await upgrades.upgradeProxy(proxyAddr, V2, { kind: "uups", call: { fn: "initializeV2" } });
        await time.increase(PERIODS.INITIAL_BONDING + PERIODS.UNBONDING + PERIODS.EPOCH + 2);
        const before = await ethers.provider.getBalance(s2.address);
        const tx = await proxy.connect(s2).claimStake(s2.address, pool.address);
        const rc = await tx.wait();
        await expect(tx).to.emit(proxy, "StakeClaimed");
        const after = await ethers.provider.getBalance(s2.address);
        expect(after - before + rc.gasUsed * rc.gasPrice).to.equal(ethers.parseEther("10"));
        expect(await proxy.stakerUnbondingBDAG(s2.address, pool.address)).to.equal(0n);
    });

    it("post-upgrade stake/unstake keep the engine-visible slots consistent", async function () {
        proxy = await upgrades.upgradeProxy(proxyAddr, V2, { kind: "uups", call: { fn: "initializeV2" } });
        const sharesBefore = await proxy.poolTotalShares(pool.address);
        await proxy.connect(s1).stake(s1.address, ethers.parseEther("1"), pool.address, { value: ethers.parseEther("1") });
        expect(await rawSlot(proxyAddr, slot1(pool.address, 6))).to.equal(await proxy.poolTotalShares(pool.address));
        expect(await proxy.poolTotalShares(pool.address)).to.be.gt(sharesBefore);
        expect(await rawSlot(proxyAddr, slot2(s1.address, pool.address, 3))).to.equal(await proxy.stakerPoolShares(s1.address, pool.address));
        await proxy.connect(s1).unstake(s1.address, ethers.MaxUint256, [pool.address]);
        expect(await proxy.stakerPoolShares(s1.address, pool.address)).to.equal(0n);
        expect(await rawSlot(proxyAddr, slot2(s1.address, pool.address, 17))).to.equal(await proxy.stakerUnbondingBDAG(s1.address, pool.address));
    });
});
