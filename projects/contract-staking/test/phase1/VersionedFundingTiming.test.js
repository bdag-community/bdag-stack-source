const { expect } = require('chai');
const { ethers, network, artifacts } = require('hardhat');
const { time } = require('@nomicfoundation/hardhat-network-helpers');

const word = value => ethers.toBeHex(value, 32);
const set = (address, slot, value) => network.provider.send('hardhat_setStorageAt', [address, ethers.toQuantity(slot), word(value)]);
const ceil = (value, numerator, denominator) => (value * numerator + denominator - 1n) / denominator;

describe('Candidate funding epoch policy', function () {
  let owner, developer, payout, alice, bob, reviewers, registry, clock, timing, genesis, base, legacyImplementation;
  beforeEach(async () => {
    [owner, developer, payout, alice, bob, ...reviewers] = await ethers.getSigners();
    const source = await ethers.deployContract('CredentialVotingSource');
    genesis = await source.genesisTime();
    clock = await ethers.deployContract('VersionedChainTallyReader', [source.target]);
    await set(clock.target, 8n, 1n);
    base = BigInt(ethers.keccak256(word(8)));
    for (const [i, value] of [0n, genesis, 1800n].entries()) await set(clock.target, base + BigInt(i), value);
    timing = await ethers.deployContract('VersionedGovernanceTiming', [owner.address, clock.target, 1800, 259200, 604800,
      { ordinaryNumerator: 3, ordinaryDenominator: 7, criticalEpochs: 1, graceEpochs: 2 }]);
    registry = await ethers.deployContract('ApprovedDevRegistry', [owner.address, reviewers.slice(0, 3).map(r => r.address)]);
    await registry.setApproval(developer.address, true, ethers.id('reviewed developer'));
    legacyImplementation = await ethers.deployContract('ProjectEscrow');
    await timing.record(1, (await clock.epochStatus()).epoch + 1n, true);
    await timing.beginExecution(1); // Isolated stand-in for the real Governor's atomic execution.
  });

  async function candidate() {
    return ethers.deployContract('VersionedProjectEscrowFactory', [owner.address, registry.target, clock.target, timing.target, legacyImplementation.target, 28 * 86400]);
  }
  async function create(factory, name = 'new', mode = 1, deadline) {
    const id = ethers.id(name);
    deadline ??= BigInt(await time.latest()) + 300n;
    await factory.createProject(id, developer.address, payout.address, 1000, deadline, mode,
      reviewers.slice(0, 3).map(r => r.address), mode ? [400, 600] : []);
    return ethers.getContractAt('VersionedProjectEscrow', await factory.projects(id));
  }
  async function approve(escrow) {
    await escrow.connect(developer).submitMilestone(ethers.id('delivery evidence'));
    await escrow.connect(reviewers[0]).approveMilestone();
    await escrow.connect(reviewers[1]).approveMilestone();
  }

  for (const duration of [60n, 600n, 1800n, 3600n, 604800n]) {
    it(`derives and freezes funding policy from a ${duration}-second epoch`, async () => {
      await set(clock.target, base + 2n, duration);
      const factory = await candidate(), escrow = await create(factory);
      expect(await escrow.policyEpochSeconds()).to.equal(duration);
      expect(await escrow.challengeSeconds()).to.equal(4n * duration);
      expect(await escrow.maximumFundingSeconds()).to.equal(ceil(duration, 90n, 7n));
      expect(await escrow.inactivitySeconds()).to.equal(ceil(duration, 180n, 7n));
      await set(clock.target, base + 2n, duration * 2n);
      expect(await escrow.policyEpochSeconds()).to.equal(duration);
      expect(await escrow.challengeSeconds()).to.equal(4n * duration);
      expect(await factory.challengeSeconds()).to.equal(8n * duration);
    });
  }

  it('retains old project addresses, runtime, balances and terms when replacing only the factory runtime', async () => {
    const old = await ethers.deployContract('ProjectEscrowFactory', [owner.address, registry.target, 28 * 86400]);
    const oldEscrow = await create(old, 'old');
    await oldEscrow.connect(alice).contribute({ value: 600 });
    const beforeCode = await ethers.provider.getCode(oldEscrow.target);
    const beforeDeadline = await oldEscrow.deadline();
    const newFactory = await candidate();
    await network.provider.send('hardhat_setCode', [old.target, await ethers.provider.getCode(newFactory.target)]);
    const upgraded = newFactory.attach(old.target), next = await create(upgraded);
    expect(await upgraded.projects(ethers.id('old'))).to.equal(oldEscrow.target);
    expect(await ethers.provider.getCode(oldEscrow.target)).to.equal(beforeCode);
    expect(await oldEscrow.deadline()).to.equal(beforeDeadline);
    expect(await oldEscrow.challengeSeconds()).to.equal(28 * 86400);
    expect(await oldEscrow.contributions(alice.address)).to.equal(600);
    expect(await ethers.provider.getBalance(oldEscrow.target)).to.equal(600);
    expect(await next.challengeSeconds()).to.equal(7200);
    await time.increaseTo(beforeDeadline);
    await expect(oldEscrow.refundFor(alice.address)).to.changeEtherBalance(alice, 600);
  });

  it('keeps the full review challenge, authorizes only governance veto, and starts a fresh challenge after resubmission', async () => {
    const factory = await candidate(), escrow = await create(factory);
    await escrow.connect(alice).contribute({ value: 1000 });
    await approve(escrow);
    await expect(escrow.releaseMilestone()).to.be.revertedWith('challenge');
    await expect(factory.connect(alice).vetoProject(ethers.id('new'))).to.be.reverted;
    await expect(escrow.connect(alice).vetoMilestone()).to.be.reverted;
    await factory.vetoProject(ethers.id('new'));
    await time.increase(7200);
    await expect(escrow.releaseMilestone()).to.be.revertedWith('challenge');
    await approve(escrow);
    const releaseAt = await escrow.approvedAt() + 7200n;
    await time.setNextBlockTimestamp(releaseAt - 1n);
    await expect(escrow.releaseMilestone()).to.be.revertedWith('challenge');
    await time.setNextBlockTimestamp(releaseAt);
    await expect(escrow.releaseMilestone()).to.changeEtherBalance(payout, 400);
  });

  it('uses frozen inactivity after partial release and returns only the remaining contributor funds', async () => {
    const escrow = await create(await candidate());
    await escrow.connect(alice).contribute({ value: 600 });
    await escrow.connect(bob).contribute({ value: 400 });
    await approve(escrow); await time.increase(7200); await escrow.releaseMilestone();
    const reclaimAt = await escrow.lastReleaseAt() + await escrow.inactivitySeconds();
    await set(clock.target, base + 2n, 60n);
    await time.setNextBlockTimestamp(reclaimAt - 1n);
    await expect(escrow.activateReclaim()).to.be.revertedWith('inactivity period');
    await time.setNextBlockTimestamp(reclaimAt); await escrow.activateReclaim();
    await expect(escrow.refundFor(alice.address)).to.changeEtherBalance(alice, 360);
    await expect(escrow.refundFor(bob.address)).to.changeEtherBalance(bob, 240);
    expect(await ethers.provider.getBalance(escrow.target)).to.equal(0);
  });

  it('rejects unauthorized creation, excessive deadlines, mismatched clocks and repeated initialization', async () => {
    const factory = await candidate();
    await expect(create(factory.connect(alice))).to.be.revertedWith('governance');
    await expect(create(factory, 'too long', 0, BigInt(await time.latest()) + 100000n)).to.be.revertedWith('funding');
    await expect(ethers.deployContract('VersionedProjectEscrowFactory', [owner.address, registry.target, registry.target, timing.target, legacyImplementation.target, 28 * 86400])).to.be.reverted;
    const escrow = await create(factory);
    await expect(escrow.initialize({ governance: owner.address, factory: factory.target, developer: developer.address,
      payout: payout.address, goal: 1, deadline: BigInt(await time.latest()) + 100n, mode: 0,
      committee: [ethers.ZeroAddress, ethers.ZeroAddress, ethers.ZeroAddress], amounts: [], challenge: 1 })).to.be.reverted;
  });

  it('extends the challenge to retain a full DAO response under a stronger critical policy', async () => {
    timing = await ethers.deployContract('VersionedGovernanceTiming', [owner.address, clock.target, 1800, 259200, 604800,
      { ordinaryNumerator: 3, ordinaryDenominator: 7, criticalEpochs: 3, graceEpochs: 2 }]);
    await timing.record(1, (await clock.epochStatus()).epoch + 1n, true); await timing.beginExecution(1);
    const escrow = await create(await candidate());
    expect(await escrow.challengeSeconds()).to.equal(6n * 1800n);
  });

  it('preserves the original implementation and challenge for a project approved before the upgrade', async () => {
    const factory = await candidate();
    await timing.endExecution(); await timing.beginExecution(999); // No new-policy record: existing proposal.
    const escrow = await create(factory, 'old approved');
    expect(await escrow.challengeSeconds()).to.equal(28 * 86400);
    await escrow.connect(alice).contribute({ value: 1000 }); await approve(escrow);
    await time.increase(7200);
    await expect(escrow.releaseMilestone()).to.be.revertedWith('challenge');
  });

  it('retains native-only upfront payment and failed-campaign refund protections', async () => {
    const factory = await candidate(), escrow = await create(factory, 'upfront', 0);
    await escrow.connect(alice).contribute({ value: 1000 });
    await expect(escrow.release()).to.changeEtherBalance(payout, 1000);
    await expect(escrow.release()).to.be.reverted;
    const failed = await create(factory, 'failed', 0);
    await failed.connect(alice).contribute({ value: 600 });
    await failed.connect(bob).contribute({ value: 300 });
    await expect(failed.refundFor(alice.address)).to.be.reverted;
    await time.increaseTo(await failed.deadline());
    await expect(failed.connect(bob).refundFor(alice.address)).to.changeEtherBalance(alice, 600);
    await expect(failed.connect(alice).refundFor(bob.address)).to.changeEtherBalance(bob, 300);
    await expect(failed.refundFor(alice.address)).to.be.reverted;
  });

  it('fails closed for malformed native clock state and rolls back project registration', async () => {
    const factory = await candidate();
    await set(clock.target, 8n, 0n);
    await expect(create(factory)).to.be.reverted;
    expect(await factory.projects(ethers.id('new'))).to.equal(ethers.ZeroAddress);
  });

  it('keeps the factory mapping and escrow storage prefixes and all existing function ABIs', async () => {
    for (const [oldName, newName] of [['ProjectEscrow', 'VersionedProjectEscrow'], ['ProjectEscrowFactory', 'VersionedProjectEscrowFactory']]) {
      const oldSource = `contracts/escrow/${oldName}.sol`, newSource = `contracts/escrow/${newName}.sol`;
      const oldBuild = await artifacts.getBuildInfo(`${oldSource}:${oldName}`);
      const newBuild = await artifacts.getBuildInfo(`${newSource}:${newName}`);
      const oldLayout = oldBuild.output.contracts[oldSource][oldName].storageLayout;
      const newLayout = newBuild.output.contracts[newSource][newName].storageLayout;
      expect(newLayout.storage.slice(0, oldLayout.storage.length).map(s => [s.label, s.slot, s.offset]))
        .to.deep.equal(oldLayout.storage.map(s => [s.label, s.slot, s.offset]));
      for (let i = 0; i < oldLayout.storage.length; i++) {
        expect(newLayout.types[newLayout.storage[i].type].label).to.equal(oldLayout.types[oldLayout.storage[i].type].label);
        expect(newLayout.types[newLayout.storage[i].type].numberOfBytes).to.equal(oldLayout.types[oldLayout.storage[i].type].numberOfBytes);
      }
      if (oldName.endsWith('Factory')) expect(newLayout.storage).to.have.length(1);
      const oldAbi = new ethers.Interface((await artifacts.readArtifact(oldName)).abi);
      const newAbi = new ethers.Interface((await artifacts.readArtifact(newName)).abi);
      for (const method of oldAbi.fragments.filter(f => f.type === 'function')) {
        const found = newAbi.getFunction(method.format('sighash'));
        expect(found).not.to.equal(null);
        expect(found.stateMutability).to.equal(method.stateMutability);
        expect(found.outputs.map(o => o.type)).to.deep.equal(method.outputs.map(o => o.type));
      }
    }
  });
});
