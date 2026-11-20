const { expect } = require('chai');
const { ethers, network, artifacts } = require('hardhat');

const SYSTEM_READER = '0x1000000000000000000000000000000000000002';
const amount = value => ethers.parseEther(String(value));
const coder = ethers.AbiCoder.defaultAbiCoder();
const rewardDomain = ethers.keccak256(ethers.toUtf8Bytes('bdag.reward.voting.v1'));

const mappingSlot = (key, slot, type = 'uint256') => ethers.keccak256(coder.encode([type, 'uint256'], [key, slot]));
const nestedSlot = (first, second, slot, firstType = 'address', secondType = 'uint256') =>
  ethers.keccak256(coder.encode([secondType, 'bytes32'], [second, mappingSlot(first, slot, firstType)]));
const miningSlot = (account, epoch) => {
  const base = ethers.keccak256(coder.encode(['bytes32', 'uint256'], [rewardDomain, epoch]));
  return ethers.keccak256(coder.encode(['address', 'bytes32'], [account, base]));
};
async function write(address, slot, value) {
  await network.provider.send('hardhat_setStorageAt', [address, typeof slot === 'string' ? slot : ethers.toBeHex(slot, 32), ethers.toBeHex(value, 32)]);
}

async function fixture() {
  const [owner, alice, bob, delegatee, pool, pool2] = await ethers.getSigners();
  // Each fixture starts before the native reader is installed. Hardhat keeps
  // code written to fixed system addresses between tests unless cleared.
  await network.provider.send('hardhat_setCode', [SYSTEM_READER, '0x']);
  const implementation = await ethers.deployContract('StakingContractV2Devnet');
  const proxy = await ethers.deployContract('UUPSProxy', [implementation.target, implementation.interface.encodeFunctionData('initialize')]);
  const staking = implementation.attach(proxy.target);
  await staking.setBondingPeriod(0);
  await staking.connect(alice).stake(alice.address, amount(10), pool.address, { value: amount(10) });
  await staking.connect(bob).stake(bob.address, amount(20), pool.address, { value: amount(20) });

  const installedReader = await ethers.deployContract('ChainTallyReader', [staking.target]);
  await network.provider.send('hardhat_setCode', [SYSTEM_READER, await ethers.provider.getCode(installedReader.target)]);
  await write(SYSTEM_READER, 4, 2);
  await write(SYSTEM_READER, 5, 1);
  await write(SYSTEM_READER, 6, 2);
  await write(SYSTEM_READER, mappingSlot(1, 7), amount(73));
  await write(SYSTEM_READER, miningSlot(alice.address, 1), amount(70));
  await write(SYSTEM_READER, miningSlot(pool.address, 1), amount(3));

  await write(staking.target, nestedSlot(pool.address, 1, 34), 5);
  await write(staking.target, mappingSlot(1, 36), amount(150));
  await write(staking.target, nestedSlot(pool.address, 1, 37), 0);

  const stakingCredential = await ethers.deployContract('EpochCredential', [staking.target, ethers.ZeroAddress, 'Staking Vote', 'sVOTE']);
  const miningCredential = await ethers.deployContract('EpochCredential', [staking.target, SYSTEM_READER, 'Mining Vote', 'mVOTE']);
  const genesis = await staking.genesisTime();
  await network.provider.send('evm_setNextBlockTimestamp', [Number(genesis + 120n)]);
  await network.provider.send('evm_mine');
  return { owner, alice, bob, delegatee, pool, pool2, staking, stakingCredential, miningCredential, genesis };
}

const delegated = (owners, pools) => coder.encode(['address[]', 'address[][]'], [owners, pools]);

describe('EpochCredential native earned-reward voting', function () {
  it('uses the previous complete reward epoch automatically without a claim or registration keeper', async function () {
    const f = await fixture();
    expect(await f.stakingCredential.votingReady()).to.equal(true);
    expect(await f.miningCredential.votingReady()).to.equal(true);
    expect(await f.stakingCredential.totalSupplyAt(2)).to.equal(amount(150));
    expect(await f.stakingCredential.rawWeightOf(f.alice.address, 2, [f.pool.address])).to.equal(amount(50));
    expect(await f.miningCredential.totalSupplyAt(2)).to.equal(amount(73));
    expect(await f.miningCredential.rawWeightOf(f.alice.address, 2, [])).to.equal(amount(70));
    expect(await f.stakingCredential['balanceOf(address,uint256)'](f.alice.address, 2)).to.equal(0);
  });

  it('does not cap earned weight and never derives votes from deposits alone', async function () {
    const f = await fixture();
    expect(await f.stakingCredential.getVotes(f.bob.address, 2, delegated([f.bob.address], [[f.pool.address]]))).to.equal(amount(100));
    await write(f.staking.target, nestedSlot(f.pool.address, 1, 34), 0);
    expect(await f.stakingCredential.rawWeightOf(f.alice.address, 2, [f.pool.address])).to.equal(0);
  });

  it('keeps one-hop delegation historical and counts each source once', async function () {
    const f = await fixture();
    await f.stakingCredential.connect(f.alice).delegate(f.delegatee.address);
    await write(SYSTEM_READER, 4, 3);
    await write(f.staking.target, nestedSlot(f.pool.address, 2, 34), 5);
    await write(f.staking.target, mappingSlot(2, 36), amount(150));
    await network.provider.send('evm_setNextBlockTimestamp', [Number(f.genesis + 180n)]);
    await network.provider.send('evm_mine');
    expect(await f.stakingCredential.getVotes(f.alice.address, 3, '0x')).to.equal(0);
    expect(await f.stakingCredential.getVotes(
      f.delegatee.address, 3, delegated([f.alice.address], [[f.pool.address]])
    )).to.equal(amount(50));
    await expect(f.stakingCredential.getVotes(
      f.delegatee.address, 3, delegated([f.alice.address, f.alice.address], [[f.pool.address], [f.pool.address]])
    )).to.be.revertedWithCustomError(f.stakingCredential, 'InvalidList');
  });

  it('issues optional soulbound visibility receipts that do not alter authority', async function () {
    const f = await fixture();
    await f.stakingCredential.claimFor(f.alice.address, 2, [f.pool.address]);
    expect(await f.stakingCredential['balanceOf(address,uint256)'](f.alice.address, 2)).to.equal(amount(50));
    expect(await f.stakingCredential.rawWeightOf(f.alice.address, 2, [f.pool.address])).to.equal(amount(50));
    await expect(f.stakingCredential.connect(f.alice).safeTransferFrom(
      f.alice.address, f.bob.address, 2, 1, '0x'
    )).to.be.revertedWithCustomError(f.stakingCredential, 'Soulbound');
    await expect(f.stakingCredential.claimFor(f.alice.address, 2, [f.pool.address]))
      .to.be.revertedWithCustomError(f.stakingCredential, 'NothingToClaim');
  });

  it('allows permissionless pool discovery, including operator self-pools, but rejects malformed lists', async function () {
    const f = await fixture();
    await f.stakingCredential.registerPools(f.alice.address, [f.pool.address]);
    await f.stakingCredential.registerPools(f.pool.address, [f.pool.address]);
    expect(await f.stakingCredential.poolsOf(f.alice.address)).to.deep.equal([f.pool.address]);
    await expect(f.stakingCredential.registerPools(f.alice.address, [f.pool2.address, f.pool.address]))
      .to.be.revertedWithCustomError(f.stakingCredential, 'InvalidList');
    await expect(f.miningCredential.registerPools(f.alice.address, [f.pool.address]))
      .to.be.revertedWithCustomError(f.miningCredential, 'InvalidPool');
  });

  it('fails closed for a partial bootstrap epoch and for a reward epoch that is still open', async function () {
    const f = await fixture();
    await write(SYSTEM_READER, 6, 3);
    expect(await f.stakingCredential.votingReady()).to.equal(false);
    expect(await f.miningCredential.votingReady()).to.equal(false);
    await expect(f.stakingCredential.totalSupplyAt(2)).to.be.revertedWithCustomError(f.stakingCredential, 'VotingUnavailable');
  });

  it('fits the London code limit without post-London PUSH0 or MCOPY opcodes', async function () {
    const artifact = await artifacts.readArtifact('EpochCredential');
    const code = ethers.getBytes(artifact.deployedBytecode);
    expect(code.length).to.be.lessThan(24577);
    for (let i = 0; i < code.length; i++) {
      const op = code[i]; if (op === 0xfe) break;
      expect(op).not.to.equal(0x5f); expect(op).not.to.equal(0x5e);
      if (op >= 0x60 && op <= 0x7f) i += op - 0x5f;
    }
  });
});
