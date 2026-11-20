const { expect } = require('chai');
const { ethers, network } = require('hardhat');

const READER = '0x1000000000000000000000000000000000000002';
const word = value => ethers.toBeHex(value, 32);
const coder = ethers.AbiCoder.defaultAbiCoder();
const map = (type, key, slot) => ethers.keccak256(coder.encode([type, 'uint256'], [key, slot]));

describe('Testnet reward clock preserves native operator locks', function () {
  let snapshot;
  beforeEach(async () => { snapshot = await network.provider.send('evm_snapshot'); });
  afterEach(async () => { await network.provider.send('evm_revert', [snapshot]); });

  async function write(address, slot, value) {
    await network.provider.send('hardhat_setStorageAt', [address, typeof slot === 'string' ? slot : word(slot), word(value)]);
  }

  it('keeps 60-second votes without unlocking legacy native auto-stakes', async function () {
    const [, operator] = await ethers.getSigners();
    const implementation = await ethers.deployContract('StakingContractV2Testnet');
    const proxy = await ethers.deployContract('UUPSProxy', [implementation.target, implementation.interface.encodeFunctionData('initialize')]);
    const staking = implementation.attach(proxy.target);
    const reader = await ethers.deployContract('ChainTallyReader', [staking.target]);
    await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(reader.target)]);
    await write(READER, 0, 1); // RC2 block-based financial epoch.
    await write(READER, 4, 60); // Fast reward-voting epoch.
    const amount = ethers.parseEther('1');
    await write(staking.target, map('address', operator.address, 11), await staking.genesisTime());
    await network.provider.send('hardhat_setBalance', [staking.target, word(amount)]);
    await write(staking.target, map('address', operator.address, 5), amount);
    await write(staking.target, map('address', operator.address, 6), amount);
    const poolEpoch = map('address', operator.address, 7);
    await write(staking.target, map('uint256', 1, BigInt(poolEpoch)), amount);
    const lockedPool = map('address', operator.address, 12);
    await write(staking.target, map('address', operator.address, BigInt(lockedPool)), amount);
    await network.provider.send('evm_setNextBlockTimestamp', [Number(await staking.genesisTime() + 3600n)]);
    await network.provider.send('evm_mine');
    expect(await staking.epochDuration()).to.equal(60);
    expect(await staking.currentEpoch()).to.equal(60);
    expect(await staking.operatorEpoch()).to.equal(1);
    await expect(staking.connect(operator).claimStake(operator.address, operator.address)).to.be.revertedWithCustomError(staking, 'NothingToClaim');
    expect(await staking.stakerTotalLockedShares(operator.address, operator.address)).to.equal(amount);
    await write(READER, 0, 52);
    await expect(staking.connect(operator).claimStake(operator.address, operator.address)).to.be.revertedWithCustomError(staking, 'NothingToClaim');
    await write(READER, 0, 53);
    await staking.connect(operator).claimStake(operator.address, operator.address);
    expect(await staking.stakerTotalLockedShares(operator.address, operator.address)).to.equal(0);
    expect(await staking.poolTotalAssets(operator.address)).to.equal(0);
  });
});
