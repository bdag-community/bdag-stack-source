const { expect } = require('chai');
const hre = require('hardhat');
const { ethers, network, artifacts } = hre;

const V1 = 'contracts/staking/StakingPool.sol:StakingContract';
const V2 = 'contracts/staking/StakingPoolV2.sol:StakingContractV2';
const IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const READER = '0x1000000000000000000000000000000000000002';
const coder = ethers.AbiCoder.defaultAbiCoder();
const word = value => ethers.toBeHex(value, 32);
async function write(address, slot, value) {
  await network.provider.send('hardhat_setStorageAt', [address, typeof slot === 'string' ? slot : word(slot), word(value)]);
}
const map = (key, slot, type = 'uint256') => ethers.keccak256(coder.encode([type, 'uint256'], [key, slot]));
const nested = (first, second, slot) => ethers.keccak256(coder.encode(['uint256', 'bytes32'], [second, map(first, slot, 'address')]));

async function fixture(name = V2) {
  await network.provider.send('hardhat_setCode', [READER, '0x']);
  const [oldOwner, alice, bob, pool, pool2, retiredKey, legacyManager, daoAddress, nextDao] = await ethers.getSigners();
  const old = await (await ethers.getContractFactory(V1)).deploy();
  const proxy = await ethers.deployContract('UUPSProxy', [old.target, old.interface.encodeFunctionData('initialize')]);
  const v1 = await ethers.getContractAt(V1, proxy.target);
  await v1.addManagers([legacyManager.address]);
  const implementation = await (await ethers.getContractFactory(name)).deploy();
  await write(proxy.target, IMPL, BigInt(implementation.target));
  await write(proxy.target, 0, BigInt(daoAddress.address));
  await network.provider.send('hardhat_setCode', [daoAddress.address, '0x00']);
  await network.provider.send('hardhat_impersonateAccount', [daoAddress.address]);
  const dao = await ethers.getSigner(daoAddress.address);
  const staking = await ethers.getContractAt(name, proxy.target, dao);
  return { oldOwner, alice, bob, pool, pool2, retiredKey, legacyManager, dao, nextDao, staking, address: proxy.target };
}

async function installReader(f, current = 2n, firstComplete = 2n) {
  const implementation = await ethers.deployContract('ChainTallyReader', [f.address]);
  await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(implementation.target)]);
  await write(READER, 4, current);
  await write(READER, 5, 1);
  await write(READER, 6, firstComplete);
}

describe('Staking DAO authority and native earned-reward voting', function () {
  it('retires old owner, manager and emergency-key authority without a bootstrap entrypoint', async function () {
    const f = await fixture();
    await expect(f.staking.connect(f.oldOwner).setMinStake(1)).to.be.reverted;
    await expect(f.staking.connect(f.legacyManager).adminRescueDelegation(
      [f.alice.address], [f.pool.address], [f.pool2.address]
    )).to.be.reverted;
    for (const name of ['initializeDaoV2', 'seedVotingTotal', 'guardianBlock', 'pauseUntil', 'guardian']) {
      expect(f.staking.interface.hasFunction(name), name).to.equal(false);
    }
    await expect(f.staking.connect(f.retiredKey).pause()).to.be.reverted;
    await expect(f.staking.connect(f.retiredKey).setStakingBlockedUntil(f.alice.address, ethers.MaxUint256)).to.be.reverted;
  });

  it('uses two-step contract ownership and keeps governed staking restrictions separate from withdrawals', async function () {
    const f = await fixture();
    await network.provider.send('hardhat_setCode', [f.nextDao.address, '0x00']);
    await network.provider.send('hardhat_impersonateAccount', [f.nextDao.address]);
    await f.staking.transferOwnership(f.nextDao.address);
    await expect(f.staking.connect(f.alice).acceptOwnership()).to.be.reverted;
    await f.staking.connect(f.nextDao).acceptOwnership();
    expect(await f.staking.owner()).to.equal(f.nextDao.address);
    await expect(f.staking.pause()).to.be.reverted;
  });

  it('bounds DAO-controlled economic parameters and freezes legacy consensus setters', async function () {
    const f = await fixture();
    const before = await Promise.all([1, 2, 8].map(slot => ethers.provider.getStorage(f.address, slot)));
    await expect(f.staking.setMinStake(0)).to.be.reverted;
    await expect(f.staking.setMinStake(ethers.parseEther('1000') + 1n)).to.be.reverted;
    await f.staking.setMinStake(ethers.parseEther('0.01'));
    await expect(f.staking.setGenesisTime(1)).to.be.reverted;
    await expect(f.staking.setSplitRatios(60, 40)).to.be.reverted;
    expect(await Promise.all([1, 2, 8].map(slot => ethers.provider.getStorage(f.address, slot)))).to.deep.equal(before);
  });

  it('attributes only pool yield, includes explicit operator units and conserves the native total', async function () {
    const f = await fixture();
    await f.staking.setBondingPeriod(0);
    await f.staking.connect(f.alice).stake(f.alice.address, ethers.parseEther('10'), f.pool.address, { value: ethers.parseEther('10') });
    await f.staking.connect(f.pool).stake(f.pool.address, ethers.parseEther('5'), f.pool.address, { value: ethers.parseEther('5') });
    await installReader(f, 2, 2);
    await write(f.address, nested(f.pool.address, 1, 34), 2);
    await write(f.address, map(1, 36), ethers.parseEther('30'));
    await write(f.address, nested(f.pool.address, 1, 37), ethers.parseEther('10'));
    expect(await f.staking.votingWeightAt(f.alice.address, [f.pool.address], 1)).to.equal(ethers.parseEther('20'));
    expect(await f.staking.votingWeightAt(f.pool.address, [f.pool.address], 1)).to.equal(ethers.parseEther('10'));
    expect(await f.staking.totalVotingWeightAt(1)).to.equal(ethers.parseEther('30'));
    expect(await f.staking.votingWeightAt(f.bob.address, [f.pool.address], 1)).to.equal(0);
  });

  it('checkpoints every share change so later deposits cannot earn earlier yield', async function () {
    const f = await fixture();
    await f.staking.setBondingPeriod(0);
    await f.staking.connect(f.alice).stake(f.alice.address, ethers.parseEther('10'), f.pool.address, { value: ethers.parseEther('10') });
    await installReader(f, 1, 2); // reward epoch 1 is open
    await write(f.address, nested(f.pool.address, 1, 34), 2);
    await f.staking.connect(f.alice).stake(f.alice.address, ethers.parseEther('10'), f.pool.address, { value: ethers.parseEther('10') });
    await write(f.address, nested(f.pool.address, 1, 34), 5);
    await write(f.address, map(1, 36), ethers.parseEther('80'));
    await write(READER, 4, 2); // close epoch 1
    expect(await f.staking.votingWeightAt(f.alice.address, [f.pool.address], 1)).to.equal(ethers.parseEther('80'));
    expect(await f.staking.totalVotingWeightAt(1)).to.equal(ethers.parseEther('80'));
    await f.staking.connect(f.alice).unstake(f.alice.address, ethers.parseEther('1'), [f.pool.address]);
    expect(await f.staking.votingWeightAt(f.alice.address, [f.pool.address], 1)).to.equal(ethers.parseEther('80'));
  });

  it('fails closed for partial and open reward epochs', async function () {
    const f = await fixture();
    await installReader(f, 2, 3);
    expect(await f.staking.rewardEpochFinalized(1)).to.equal(false);
    expect(await f.staking.votingWeightAt(f.alice.address, [f.pool.address], 1)).to.equal(0);
    await expect(f.staking.totalVotingWeightAt(1)).to.be.reverted;
    await write(READER, 6, 2); await write(READER, 4, 1);
    expect(await f.staking.rewardEpochFinalized(1)).to.equal(false);
  });
});

describe('Staking V3 build and storage invariants', function () {
  it('preserves all legacy slots, appends the native ledger and fits London/EIP-170', async function () {
    const old = await artifacts.getBuildInfo(V1);
    const fresh = await artifacts.getBuildInfo(V2);
    const a = old.output.contracts['contracts/staking/StakingPool.sol'].StakingContract.storageLayout.storage;
    const b = fresh.output.contracts['contracts/staking/StakingPoolV2.sol'].StakingContractV2.storageLayout.storage;
    for (let i = 0; i < 18; i++) {
      expect(b[i].slot).to.equal(a[i].slot); expect(b[i].offset).to.equal(a[i].offset); expect(b[i].label).to.equal(a[i].label);
      expect(b[i].type.replace(/\)\d+_storage/g, ')_storage')).to.equal(a[i].type.replace(/\)\d+_storage/g, ')_storage'));
    }
    const slots = {
      _votingCheckpoints: 19, _totalVotingCheckpoints: 20, stakingBlockedUntil: 21,
      _retiredGuardian: 22, _retiredGuardianExpiry: 23, _minStakeOverride: 24,
      _bondingOverridePlusOne: 25, _unbondingOverride: 26, pausedUntil: 27,
      pendingOwner: 28, _reservedRepairRoot: 29, _reservedRepaired: 30,
      _retiredVotingSeedEpoch: 31, _bondingEndsAt: 33, poolEpochRewardPerShare: 34,
      poolRewardRemainder: 35, stakingEpochVotingTotal: 36, operatorEpochVoting: 37,
      _rewardSettled: 38, _rewardAccCheckpoint: 39, _rewardShares: 40,
    };
    for (const [label, slot] of Object.entries(slots)) expect(b.find(item => item.label === label).slot, label).to.equal(String(slot));
    expect(b.find(item => item.label === '_retiredVotingSeeded').slot).to.equal('32');
    expect(b.find(item => item.label === 'daoInitialized').slot).to.equal('32');
    expect(b.find(item => item.label === 'daoInitialized').offset).to.equal(1);
    const previous = await artifacts.readArtifact(V1);
    for (const fqn of [V2, 'contracts/staking/StakingPoolV2.sol:StakingContractV2Devnet']) {
      const current = await artifacts.readArtifact(fqn);
      const currentInterface = new ethers.Interface(current.abi);
      for (const fragment of new ethers.Interface(previous.abi).fragments.filter(item => item.type === 'function')) {
        const found = currentInterface.getFunction(fragment.format('sighash'));
        expect(found, fragment.format('sighash')).not.to.equal(null);
        expect(found.stateMutability).to.equal(fragment.stateMutability);
      }
      const code = ethers.getBytes(current.deployedBytecode);
      expect(code.length).to.be.lessThan(24577);
      for (let i = 0; i < code.length; i++) {
        const op = code[i]; if (op === 0xfe) break;
        expect(op).not.to.equal(0x5f); expect(op).not.to.equal(0x5e);
        if (op >= 0x60 && op <= 0x7f) i += op - 0x5f;
      }
    }
  });

  it('exposes consistent private-devnet epoch and lock getters', async function () {
    const f = await fixture('contracts/staking/StakingPoolV2.sol:StakingContractV2Devnet');
    expect(await f.staking.epochDuration()).to.equal(60);
    expect(await f.staking.EPOCH_DURATION()).to.equal(60);
    expect(await f.staking.INITIAL_BONDING_PERIOD()).to.equal(240);
    expect(await f.staking.UNBONDING_PERIOD()).to.equal(180);
  });
});
