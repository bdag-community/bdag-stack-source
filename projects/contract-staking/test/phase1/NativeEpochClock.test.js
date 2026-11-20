const { expect } = require('chai');
const { ethers, network } = require('hardhat');

const READER = '0x1000000000000000000000000000000000000002';
const coder = ethers.AbiCoder.defaultAbiCoder();
const word = value => ethers.toBeHex(value, 32);
const map = (key, slot, type = 'uint256') => ethers.keccak256(coder.encode([type, 'uint256'], [key, slot]));
const nested = (first, second, slot) => ethers.keccak256(coder.encode(['uint256', 'bytes32'], [second, map(first, slot, 'address')]));

describe('Native epoch floor across staking, credentials and governance', function () {
  let snapshot;
  beforeEach(async () => { snapshot = await network.provider.send('evm_snapshot'); });
  afterEach(async () => { await network.provider.send('evm_revert', [snapshot]); });

  async function write(address, slot, value) {
    await network.provider.send('hardhat_setStorageAt', [address, typeof slot === 'string' ? slot : word(slot), word(value)]);
  }
  async function marker(epoch) { await write(READER, 4, epoch); }

  async function fixture() {
    await network.provider.send('hardhat_setCode', [READER, '0x']);
    const people = await ethers.getSigners();
    const [owner, alice, bob, carol, pool] = people;
    const implementation = await ethers.deployContract('StakingContractV2Devnet');
    const proxy = await ethers.deployContract('UUPSProxy', [implementation.target, implementation.interface.encodeFunctionData('initialize')]);
    const staking = implementation.attach(proxy.target);
    await staking.setBondingPeriod(0);
    const amount = ethers.parseEther('10');
    await staking.connect(alice).stake(alice.address, amount, pool.address, { value: amount });
    const readerImplementation = await ethers.deployContract('ChainTallyReader', [proxy.target]);
    await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(readerImplementation.target)]);
    await write(READER, 0, 0); await marker(2); await write(READER, 5, 1); await write(READER, 6, 2);
    await write(staking.target, nested(pool.address, 4, 34), 2);
    await write(staking.target, map(4, 36), 2n * amount);
    const reader = await ethers.getContractAt('ChainTallyReader', READER);
    const mining = await ethers.deployContract('EpochCredential', [proxy.target, READER, 'Mining', 'mVOTE']);
    const stakers = await ethers.deployContract('EpochCredential', [proxy.target, ethers.ZeroAddress, 'Staking', 'sVOTE']);
    await stakers.registerPools(alice.address, [pool.address]);
    await network.provider.send('evm_setNextBlockTimestamp', [Number(await staking.genesisTime() + 120n)]);
    await network.provider.send('evm_mine');
    return { people, owner, alice, bob, carol, pool, staking, reader, mining, stakers, amount };
  }

  it('uses the monotonic engine floor while still allowing forward timestamp progress', async function () {
    const f = await fixture();
    expect(await f.staking.currentEpoch()).to.equal(2);
    await marker(5);
    for (const source of [f.staking, f.reader, f.mining, f.stakers]) expect(await source.currentEpoch()).to.equal(5);
    await network.provider.send('evm_setNextBlockTimestamp', [Number(await f.staking.genesisTime() + 360n)]);
    await network.provider.send('evm_mine');
    for (const source of [f.staking, f.reader, f.mining, f.stakers]) expect(await source.currentEpoch()).to.equal(6);
  });

  it('does not confuse the historical financial epoch with the reward-voting epoch', async function () {
    const f = await fixture();
    await write(READER, 0, 1000);
    for (const source of [f.staking, f.reader, f.mining, f.stakers]) expect(await source.currentEpoch()).to.equal(2);
    await marker(5);
    for (const source of [f.staking, f.reader, f.mining, f.stakers]) expect(await source.currentEpoch()).to.equal(5);
  });

  it('does not let share changes rewrite a finalized reward epoch when timestamp trails the engine', async function () {
    const f = await fixture(); await marker(5);
    expect(await f.staking.votingWeightAt(f.alice.address, [f.pool.address], 4)).to.equal(2n * f.amount);
    await f.staking.connect(f.alice).stake(f.alice.address, f.amount, f.pool.address, { value: f.amount });
    expect(await f.staking.votingWeightAt(f.alice.address, [f.pool.address], 4)).to.equal(2n * f.amount);
    expect(await f.staking.totalVotingWeightAt(4)).to.equal(2n * f.amount);
  });

  it('schedules delegation after the native epoch without rewriting prior delegation', async function () {
    const f = await fixture(); await marker(5);
    await f.stakers.connect(f.alice).delegate(f.bob.address);
    expect(await f.stakers.delegateAt(f.alice.address, 5)).to.equal(f.alice.address);
    expect(await f.stakers.delegateAt(f.alice.address, 6)).to.equal(f.bob.address);
    await marker(6);
    await f.stakers.connect(f.alice).delegate(f.carol.address);
    expect(await f.stakers.delegateAt(f.alice.address, 6)).to.equal(f.bob.address);
    expect(await f.stakers.delegateAt(f.alice.address, 7)).to.equal(f.carol.address);
  });

  it('advances a real Governor proposal by native epoch even while wall-clock time trails it', async function () {
    const f = await fixture();
    const nonce = await ethers.provider.getTransactionCount(f.owner.address);
    const governorAddress = ethers.getCreateAddress({ from: f.owner.address, nonce: nonce + 3 });
    const miningLock = await ethers.deployContract('GovernanceTimelock', [5, governorAddress]);
    const stakingLock = await ethers.deployContract('GovernanceTimelock', [5, governorAddress]);
    const policy = await ethers.deployContract('GovernancePolicyModule', [governorAddress, miningLock.target]);
    const governor = await ethers.deployContract('EpochGovernor', [
      f.mining.target, f.stakers.target, miningLock.target, stakingLock.target, policy.target,
      { epochSeconds: 60, ordinaryDelay: 5, criticalDelay: 15, proposalBond: 1000 },
      [{ target: f.staking.target, selector: f.staking.interface.getFunction('setMinStake').selector,
        domain: 2, tier: 1, ownerDomain: 2 }],
    ]);
    await marker(5);
    const args = [[f.staking.target], [0], [f.staking.interface.encodeFunctionData('setMinStake', [ethers.parseEther('0.02')])], 'native clock proposal'];
    await governor.connect(f.alice).proposeWithDeposit(...args, { value: 1000 });
    const id = await governor.hashProposal(...args.slice(0, 3), ethers.id(args[3]));
    expect(await governor.state(id)).to.equal(0);
    await marker(6); expect(await governor.state(id)).to.equal(1);
    await marker(7); expect(await governor.state(id)).to.equal(3);
    expect(await governor.clock()).to.equal(8);
    expect((BigInt((await ethers.provider.getBlock('latest')).timestamp) - await f.staking.genesisTime()) / 60n).to.equal(2);
  });
});
