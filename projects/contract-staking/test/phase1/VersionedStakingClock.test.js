const { expect } = require('chai');
const { ethers, network, artifacts } = require('hardhat');
const { time } = require('@nomicfoundation/hardhat-network-helpers');

const READER = '0x1000000000000000000000000000000000000002';
const IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const SOURCE = 'contracts/staking/StakingPoolV2.sol';
const CANDIDATE = 'contracts/staking/VersionedTestnetStaking.sol';
const word = value => ethers.toBeHex(value, 32);
const map = (key, slot, type = 'address') => ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode([type, 'uint256'], [key, slot]));
const set = (address, slot, value) => network.provider.send('hardhat_setStorageAt', [address, ethers.toQuantity(slot), word(value)]);

describe('Candidate testnet staking clock transition', function () {
  let snapshot, owner, alice, bob, pool, staking, candidate, genesis, execution;
  beforeEach(async () => {
    snapshot = await network.provider.send('evm_snapshot');
    [owner, alice, bob, pool] = await ethers.getSigners();
    await network.provider.send('hardhat_setCode', [READER, '0x']);
    const legacy = await ethers.deployContract('StakingContractV2Testnet');
    const proxy = await ethers.deployContract('UUPSProxy', [legacy.target, legacy.interface.encodeFunctionData('initialize')]);
    staking = legacy.attach(proxy.target);
    genesis = await staking.genesisTime();
    execution = await ethers.deployContract('TimingExecutionFixture');
    candidate = await ethers.deployContract('VersionedTestnetStaking', [execution.target]);
  });
  afterEach(async () => { await network.provider.send('evm_revert', [snapshot]); });

  async function install() {
    // Isolated simulation of the future atomic native transition only. There is
    // deliberately no schedule setter or migration transaction in the candidate.
    const reader = await ethers.deployContract('VersionedChainTallyReader', [staking.target]);
    await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(reader.target)]);
    await set(READER, 8n, 2n);
    const base = BigInt(ethers.keccak256(word(8)));
    for (const [i, value] of [0n,genesis,60n,10n,genesis+600n,1800n].entries()) {
      await set(READER, base + BigInt(i), value);
    }
    await set(staking.target, BigInt(IMPL), BigInt(candidate.target));
    staking = candidate.attach(staking.target);
  }
  async function deposit(account, amount = ethers.parseEther('10')) {
    const receipt = await (await staking.connect(account).stake(account.address, amount, pool.address, { value: amount })).wait();
    return BigInt((await ethers.provider.getBlock(receipt.blockNumber)).timestamp);
  }

  it('changes only future reward epoch boundaries and exposes their real countdown', async () => {
    await install();
    expect(await staking.epochDuration()).to.equal(60n);
    await time.increaseTo(genesis + 599n);
    expect(await staking.currentEpoch()).to.equal(9n);
    expect(await staking.timeToNextEpoch()).to.equal(1n);
    await time.increaseTo(genesis + 600n);
    expect(await staking.currentEpoch()).to.equal(10n);
    expect(await staking.epochDuration()).to.equal(1800n);
    expect(await staking.timeToNextEpoch()).to.equal(1800n);
    await time.increaseTo(genesis + 2400n);
    expect(await staking.currentEpoch()).to.equal(11n);
  });

  it('preserves stored locks, old fallback locks, pending withdrawals, balances and restrictions', async () => {
    await time.increaseTo(genesis + 550n);
    await deposit(alice);
    const bobFirst = await deposit(bob);
    await set(staking.target, BigInt(map(bob.address, 33)), 0); // Older position without a frozen deadline.
    await staking.connect(alice).unstake(alice.address, ethers.parseEther('1'), [pool.address]);
    await staking.setStakingBlockedUntil(bob.address, ethers.MaxUint256);
    const before = {
      slots: await Promise.all(Array.from({length:41}, (_, i) => ethers.provider.getStorage(staking.target, i))),
      balance: await ethers.provider.getBalance(staking.target),
      aliceEnd: await staking.bondingEndsAt(alice.address),
      bobEnd: await staking.bondingEndsAt(bob.address),
      pending: await staking.getUnbondingRequests(alice.address),
      shares: await staking.stakerPoolShares(alice.address, pool.address),
      principal: await staking.stakerPrincipalBDAG(alice.address, pool.address),
    };
    await install();
    await time.increaseTo(genesis + 601n);
    expect(await Promise.all(Array.from({length:41}, (_, i) => ethers.provider.getStorage(staking.target, i)))).to.deep.equal(before.slots);
    expect(await ethers.provider.getBalance(staking.target)).to.equal(before.balance);
    expect(await staking.bondingEndsAt(alice.address)).to.equal(before.aliceEnd);
    expect(await staking.bondingEndsAt(bob.address)).to.equal(before.bobEnd).and.equal(bobFirst + 240n);
    expect(await staking.getUnbondingRequests(alice.address)).to.deep.equal(before.pending);
    expect(await staking.stakerPoolShares(alice.address, pool.address)).to.equal(before.shares);
    expect(await staking.stakerPrincipalBDAG(alice.address, pool.address)).to.equal(before.principal);
    expect(await staking.stakingBlockedUntil(bob.address)).to.equal(ethers.MaxUint256);
    expect(await staking.owner()).to.equal(owner.address);
  });

  it('uses the new bonding period for new positions and aligns new exits with the new epoch anchor', async () => {
    await install();
    await time.increaseTo(genesis + 601n);
    const first = await deposit(alice);
    expect(await staking.bondingEndsAt(alice.address)).to.equal(first + 7200n);
    await staking.connect(alice).unstake(alice.address, ethers.parseEther('1'), [pool.address]);
    expect((await staking.unbondingRequests(alice.address, 0)).releaseTime).to.equal(genesis + 2400n + 5400n);
    // Native-created positions may have a timestamp but no fixed end slot.
    await set(staking.target, BigInt(map(bob.address, 11)), first);
    expect(await staking.bondingEndsAt(bob.address)).to.equal(first + 7200n);
  });

  it('does not let a top-up shorten an existing withdrawal deadline', async () => {
    await deposit(alice);
    await staking.connect(alice).unstake(alice.address, ethers.parseEther('1'), [pool.address]);
    const requests = BigInt(map(alice.address, 10));
    const requestBase = BigInt(ethers.keccak256(word(requests)));
    const preservedDeadline = genesis + 20000n;
    await set(staking.target, requestBase + 1n, preservedDeadline);
    await install();
    await time.increaseTo(genesis + 601n);
    await deposit(alice, ethers.parseEther('1'));
    expect((await staking.unbondingRequests(alice.address, 0)).releaseTime).to.equal(preservedDeadline);
  });

  it('scales new DAO period overrides with epochs without changing existing lock or exit dates', async () => {
    await install();
    await staking.setBondingPeriod(120);await staking.setUnbondingPeriod(120);
    const first=await deposit(alice);
    await staking.connect(alice).unstake(alice.address,ethers.parseEther('1'),[pool.address]);
    const end=await staking.bondingEndsAt(alice.address);
    const release=(await staking.unbondingRequests(alice.address,0)).releaseTime;
    expect(end).to.equal(first+120n);
    await time.increaseTo(genesis+601n);
    expect(await staking.bondingPeriod()).to.equal(3600n);
    expect(await staking.unbondingPeriod()).to.equal(3600n);
    expect(await staking.bondingEndsAt(alice.address)).to.equal(end);
    expect((await staking.unbondingRequests(alice.address,0)).releaseTime).to.equal(release);
    const bobFirst=await deposit(bob);
    expect(await staking.bondingEndsAt(bob.address)).to.equal(bobFirst+3600n);
  });

  it('keeps pre-migration fixed overrides until a new DAO policy explicitly replaces them',async()=>{
    await staking.setBondingPeriod(120);await staking.setUnbondingPeriod(120);
    await install();await time.increaseTo(genesis+601n);
    expect(await staking.bondingPeriod()).to.equal(120n);
    expect(await staking.unbondingPeriod()).to.equal(120n);
    await staking.setBondingPeriod(3600);await staking.setUnbondingPeriod(5400);
    expect([...(await staking.bondingEpochPolicy())]).to.deep.equal([3600n,1800n]);
    expect([...(await staking.unbondingEpochPolicy())]).to.deep.equal([5400n,1800n]);
    expect(BigInt(await ethers.provider.getStorage(staking.target,25))).to.equal(121n);
    expect(BigInt(await ethers.provider.getStorage(staking.target,26))).to.equal(120n);
  });

  it('honors an already-approved legacy period action using its original bounds and fixed terms', async () => {
    await install(); await time.increaseTo(genesis + 601n);
    await staking.setBondingPeriod(3600); await staking.setUnbondingPeriod(3600);
    await execution.setLegacy(true);
    await staking.setBondingPeriod(120); await staking.setUnbondingPeriod(120);
    expect(await staking.bondingPeriod()).to.equal(120);
    expect(await staking.unbondingPeriod()).to.equal(120);
    expect((await staking.bondingEpochPolicy()).denominator).to.equal(0);
    expect((await staking.unbondingEpochPolicy()).denominator).to.equal(0);
    await expect(staking.setUnbondingPeriod(3600)).to.be.revertedWithCustomError(staking, 'InvalidParameter');
  });

  it('bounds epoch overrides, rejects unauthorized calls and rounds fractional periods upward',async()=>{
    await install();
    await expect(staking.connect(alice).setBondingPeriod(120)).to.be.revertedWithCustomError(staking,'NotOwner');
    await expect(staking.connect(alice).setUnbondingPeriod(120)).to.be.revertedWithCustomError(staking,'NotOwner');
    await expect(staking.setBondingPeriod(481)).to.be.revertedWithCustomError(staking,'InvalidParameter');
    await expect(staking.setUnbondingPeriod(59)).to.be.revertedWithCustomError(staking,'InvalidParameter');
    await expect(staking.setUnbondingPeriod(361)).to.be.revertedWithCustomError(staking,'InvalidParameter');
    await staking.setBondingPeriod(1);await staking.setUnbondingPeriod(61);
    const base=BigInt(ethers.keccak256(word(8)));
    await set(READER,base+5n,1801n);
    await time.increaseTo(genesis+601n);
    expect(await staking.bondingPeriod()).to.equal(31n);
    expect(await staking.unbondingPeriod()).to.equal(1832n);
    await staking.setBondingPeriod(0);expect(await staking.bondingPeriod()).to.equal(0n);
  });

  it('lets an existing withdrawal complete at its original deadline after the clock transition', async () => {
    await time.increaseTo(genesis + 550n);
    await deposit(alice);
    await staking.connect(alice).unstake(alice.address, ethers.parseEther('1'), [pool.address]);
    const bondingEnd = await staking.bondingEndsAt(alice.address);
    const withdrawalEnd = (await staking.unbondingRequests(alice.address, 0)).releaseTime;
    await install();
    await time.increaseTo(genesis + 601n);
    await expect(staking.connect(alice).claimStake(alice.address, pool.address)).to.be.reverted;
    await time.increaseTo(bondingEnd > withdrawalEnd ? bondingEnd : withdrawalEnd);
    await staking.connect(alice).claimStake(alice.address, pool.address);
    expect(await staking.getUnbondingRequests(alice.address)).to.have.length(0);
    expect(await ethers.provider.getBalance(staking.target)).to.equal(ethers.parseEther('9'));
  });

  it('retains the native financial clock independently of the slower reward clock', async () => {
    await install();
    await set(READER, 0n, 1n);
    await set(READER, 4n, 60n);
    expect(await staking.currentEpoch()).to.equal(60n);
    expect(await staking.operatorEpoch()).to.equal(1n);
    await set(READER, 0n, 53n);
    expect(await staking.operatorEpoch()).to.equal(53n);
    expect(await staking.currentEpoch()).to.equal(60n);
  });

  it('freezes a newly initialized rescue lock using the active clock even with a backward timestamp', async () => {
    await deposit(alice);
    await set(staking.target, BigInt(map(alice.address, 11)), 0);
    await set(staking.target, BigInt(map(alice.address, 33)), 0);
    await install();
    // A persisted native epoch can be ahead of the incoming block timestamp.
    await set(READER, 4n, 10n);
    await staking.adminRescueDelegation([alice.address], [pool.address], [bob.address]);
    const first = await staking.userFirstDepositTimestamp(alice.address);
    expect(first).to.be.lessThan(genesis + 600n);
    expect(await staking.bondingEndsAt(alice.address)).to.equal(first + 7200n);
    expect(BigInt(await ethers.provider.getStorage(staking.target, map(alice.address, 33)))).to.equal(first + 7200n);
  });

  it('rejects missing or mismatched schedule data instead of guessing a genesis-based clock', async () => {
    await set(staking.target, BigInt(IMPL), BigInt(candidate.target));
    staking = candidate.attach(staking.target);
    await expect(staking.currentEpoch()).to.be.reverted;
    await install();
    await set(staking.target, 8n, genesis + 1n);
    await expect(staking.currentEpoch()).to.be.revertedWithCustomError(staking, 'InvalidGenesisTime');
  });

  it('rejects a reader pinned to a different staking source even with the same genesis', async () => {
    await install();
    const other = await ethers.deployContract('StakingContractV2Testnet');
    const wrongReader = await ethers.deployContract('VersionedChainTallyReader', [other.target]);
    await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(wrongReader.target)]);
    await expect(staking.currentEpoch()).to.be.reverted;
  });

  it('preserves the entire staking layout and existing ABI and fits London size limits', async () => {
    const old = await artifacts.getBuildInfo(`${SOURCE}:StakingContractV2Testnet`);
    const fresh = await artifacts.getBuildInfo(`${CANDIDATE}:VersionedTestnetStaking`);
    const a = old.output.contracts[SOURCE].StakingContractV2Testnet.storageLayout;
    const b = fresh.output.contracts[CANDIDATE].VersionedTestnetStaking.storageLayout;
    expect(b.storage.slice(0,a.storage.length).map(s => [s.label,s.slot,s.offset])).to.deep.equal(a.storage.map(s => [s.label,s.slot,s.offset]));
    expect(b.storage.slice(a.storage.length).map(s=>[s.label,s.slot,s.offset])).to.deep.equal([
      ['bondingEpochPolicy','41',0],['unbondingEpochPolicy','42',0]
    ]);
    for (let i = 0; i < a.storage.length; i++) {
      expect(b.types[b.storage[i].type].label).to.equal(a.types[a.storage[i].type].label);
      expect(b.types[b.storage[i].type].numberOfBytes).to.equal(a.types[a.storage[i].type].numberOfBytes);
    }
    const previous = await artifacts.readArtifact(`${SOURCE}:StakingContractV2Testnet`);
    for (const fragment of new ethers.Interface(previous.abi).fragments.filter(f => f.type === 'function')) {
      const found = candidate.interface.getFunction(fragment.format('sighash'));
      expect(found, fragment.format('sighash')).not.to.equal(null);
      // RC.2 specialized epochDuration to pure because it was a constant. The
      // versioned candidate must read native storage, while remaining read-only.
      expect(found.stateMutability).to.equal(fragment.format('sighash') === 'epochDuration()' ? 'view' : fragment.stateMutability);
    }
    const current = await artifacts.readArtifact(`${CANDIDATE}:VersionedTestnetStaking`);
    const code = ethers.getBytes(current.deployedBytecode);
    expect(code.length).to.be.lessThan(24577);
    for (let i = 0; i < code.length; i++) {
      const op = code[i]; if (op === 0xfe) break;
      expect(op).not.to.equal(0x5f); expect(op).not.to.equal(0x5e);
      if (op >= 0x60 && op <= 0x7f) i += op - 0x5f;
    }
  });
});
