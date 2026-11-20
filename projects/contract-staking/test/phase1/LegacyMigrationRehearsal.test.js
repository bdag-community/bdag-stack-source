const { expect } = require('chai');
const { ethers, network } = require('hardhat');

const IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const READER = '0x1000000000000000000000000000000000000002';
const word = value => ethers.toBeHex(value, 32);

describe('Isolated mainnet-like legacy staking migration rehearsal', function () {
  let snapshot;
  beforeEach(async () => {
    snapshot = await network.provider.send('evm_snapshot');
    await network.provider.send('hardhat_setCode', [READER, '0x']);
  });
  afterEach(async () => { await network.provider.send('evm_revert', [snapshot]); });

  it('preserves a real V1 deposit and pending exit, rejects early claim, then permits legacy and V2 exits', async function () {
    const [oldOwner, alice, pool] = await ethers.getSigners();
    const legacyImplementation = await ethers.deployContract('StakingContract');
    const proxy = await ethers.deployContract('UUPSProxy', [legacyImplementation.target, legacyImplementation.interface.encodeFunctionData('initialize')]);
    const legacy = legacyImplementation.attach(proxy.target);
    const ten = ethers.parseEther('10'), one = ethers.parseEther('1');
    await legacy.connect(alice).stake(alice.address, ten, pool.address, { value: ten });
    const firstDeposit = await legacy.userFirstDepositTimestamp(alice.address);
    await network.provider.send('evm_setNextBlockTimestamp', [Number(firstDeposit + 4n * 7n * 86400n + 1n)]);
    await legacy.connect(alice).unstake(alice.address, one, [pool.address]);
    const pending = await legacy.unbondingRequests(alice.address, 0);
    const before = {
      balance: await ethers.provider.getBalance(proxy.target),
      shares: await legacy.stakerPoolShares(alice.address, pool.address),
      principal: await legacy.stakerPrincipalBDAG(alice.address, pool.address),
      assets: await legacy.poolTotalAssets(pool.address),
    };
    const next = await ethers.deployContract('StakingContractV2');
    const governor = await ethers.deployContract('GovernanceTargetFixture');
    const timelock = await ethers.deployContract('GovernanceTimelock', [3 * 86400, governor.target]);
    // Simulate the already separately tested native migration writes. No retired
    // owner signs an upgrade. This tests storage/entitlements, not native signalling.
    await network.provider.send('hardhat_setStorageAt', [proxy.target, IMPL, ethers.zeroPadValue(next.target, 32)]);
    await network.provider.send('hardhat_setStorageAt', [proxy.target, word(0), ethers.zeroPadValue(timelock.target, 32)]);
    await network.provider.send('hardhat_setStorageAt', [proxy.target, word(32), word(0x100)]);
    const staking = next.attach(proxy.target);
    expect(await staking.epochDuration()).to.equal(7 * 86400);
    expect(await staking.unbondingRequests(alice.address, 0)).to.deep.equal(pending);
    expect(await staking.userFirstDepositTimestamp(alice.address)).to.equal(firstDeposit);
    expect(await staking.stakerPoolShares(alice.address, pool.address)).to.equal(before.shares);
    expect(await staking.stakerPrincipalBDAG(alice.address, pool.address)).to.equal(before.principal);
    expect(await staking.poolTotalAssets(pool.address)).to.equal(before.assets);
    expect(await ethers.provider.getBalance(proxy.target)).to.equal(before.balance);
    await expect(staking.connect(oldOwner).setMinStake(1)).to.be.revertedWithCustomError(staking, 'NotOwner');
    await network.provider.send('evm_setNextBlockTimestamp', [Number(pending.releaseTime - 1n)]);
    await expect(staking.connect(alice).claimStake(alice.address, pool.address)).to.be.revertedWith('Total Lockdown: 3-Week Cooldown Active');
    await network.provider.send('evm_setNextBlockTimestamp', [Number(pending.releaseTime)]);
    await expect(staking.connect(alice).claimStake(alice.address, pool.address)).to.changeEtherBalances([staking, alice], [-one, one]);
    expect(await staking.getUnbondingRequests(alice.address)).to.have.length(0);
    await staking.connect(alice).unstake(alice.address, ten - one, [pool.address]);
    const second = await staking.unbondingRequests(alice.address, 0);
    await network.provider.send('evm_setNextBlockTimestamp', [Number(second.releaseTime)]);
    await expect(staking.connect(alice).claimStake(alice.address, pool.address)).to.changeEtherBalances([staking, alice], [one - ten, ten - one]);
    expect(await staking.stakerPoolShares(alice.address, pool.address)).to.equal(0);
    expect(await ethers.provider.getBalance(proxy.target)).to.equal(0);
  });
});
