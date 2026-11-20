const { expect } = require('chai');
const hre = require('hardhat');
const { ethers, network } = hre;
const { buildVersionedRuntimeBundle, loadBaseline } = require('../../scripts/phase1/versioned-runtime-bundle.cjs');

describe('Canonical versioned runtime candidate bundle', function () {
  let snapshot;
  beforeEach(async () => { snapshot = await network.provider.send('evm_snapshot'); });
  afterEach(async () => { await network.provider.send('evm_revert', [snapshot]); });
  it('binds every runtime deterministically and retains the complete historical code set', async () => {
    const first = await buildVersionedRuntimeBundle(hre);
    expect(await buildVersionedRuntimeBundle(hre)).to.deep.equal(first);
    expect(first.installationAuthorized).to.equal(false);
    expect(first.chainId).to.equal(1043);
    expect(first.runtimes).to.have.length(10);
    expect(first.runtimes.filter(x => x.mode === 'replace')).to.have.length(8);
    expect(first.runtimes.filter(x => x.mode === 'create')).to.have.length(2);
    expect(first.preservedCode).to.have.length(11);
    const baseline = loadBaseline();
    for (const row of first.runtimes) {
      expect(ethers.keccak256(row.code)).to.equal(row.codeHash);
      expect(ethers.getBytes(row.code).length).to.be.at.most(24576);
      if (row.mode === 'replace') expect(row.previousCodeHash).to.equal(ethers.keccak256(baseline[row.address].code));
    }
  });
  it('executes bound views at canonical addresses without overwriting old constructor state', async () => {
    const baseline = loadBaseline();
    for (const [address, account] of Object.entries(baseline)) {
      await network.provider.send('hardhat_setCode', [address, account.code]);
      for (const [slot, value] of Object.entries(account.storage)) await network.provider.send('hardhat_setStorageAt', [address, slot, value]);
    }
    const bundle = await buildVersionedRuntimeBundle(hre);
    const [,alice,bob]=await ethers.getSigners();
    const previous=await ethers.getContractAt('EpochCredential',bundle.addresses.miningCredential);
    const domainBefore=await previous.eip712Domain();
    const deadline=BigInt((await ethers.provider.getBlock('latest')).timestamp)+86400n;
    const signed=await alice.signTypedData({name:'BlockDAG Mining Vote',version:'1',chainId:(await ethers.provider.getNetwork()).chainId,verifyingContract:previous.target},
      {Delegation:[{name:'account',type:'address'},{name:'delegatee',type:'address'},{name:'nonce',type:'uint256'},{name:'deadline',type:'uint256'}]},
      {account:alice.address,delegatee:bob.address,nonce:0,deadline});
    for (const row of bundle.runtimes) {
      await network.provider.send('hardhat_setCode', [row.address, row.code]);
      for (const [slot, value] of Object.entries(row.newStorage)) await network.provider.send('hardhat_setStorageAt', [row.address, slot, value]);
    }
    const a = bundle.addresses, genesis = BigInt(bundle.genesisTime);
    const now = BigInt((await ethers.provider.getBlock('latest')).timestamp);
    const first = (now - genesis) / 60n + 1n;
    const word = v => ethers.toBeHex(v, 32);
    await network.provider.send('hardhat_setStorageAt', [a.reader, word(8), word(2)]);
    const base = BigInt(ethers.keccak256(word(8)));
    for (const [i, v] of [0n, genesis, 60n, first, genesis + first*60n, 1800n].entries()) {
      await network.provider.send('hardhat_setStorageAt', [a.reader, word(base+BigInt(i)), word(v)]);
    }
    const reader = await ethers.getContractAt('VersionedChainTallyReader', a.reader);
    const staking = await ethers.getContractAt('VersionedTestnetStaking', a.stakingProxy);
    const timing = await ethers.getContractAt('VersionedGovernanceTiming', a.timing);
    const governor = await ethers.getContractAt('VersionedEpochGovernor', a.governor);
    const mining = await ethers.getContractAt('VersionedEpochCredential', a.miningCredential);
    const factory = await ethers.getContractAt('VersionedProjectEscrowFactory', a.factory);
    expect(await reader.clockSource()).to.equal(a.stakingProxy);
    expect(await staking.timingModule()).to.equal(a.timing);
    expect(await staking.owner()).to.equal(a.stakingTimelock);
    expect(await staking.epochDuration()).to.equal(60);
    expect(await mining.epochDuration()).to.equal(60);
    expect(await mining.genesisTime()).to.equal(genesis);
    expect(await mining.name()).to.equal('BlockDAG Mining Vote');
    expect(await governor.timingModule()).to.equal(a.timing);
    expect(await governor.miningCredential()).to.equal(a.miningCredential);
    expect(await governor.proposalBond()).to.equal(ethers.parseEther('0.001'));
    expect(await timing.governor()).to.equal(a.governor);
    expect(await timing.clock()).to.equal(a.reader);
    expect(await timing.legacyCriticalDelay()).to.equal(604800);
    expect(await factory.legacyImplementation()).to.equal(a.legacyEscrow);
    expect(await factory.implementation()).to.equal(a.escrow);
    expect(await factory.governance()).to.equal(a.stakingTimelock);
    expect(await factory.challengeSeconds()).to.equal(240);
    for (const lockAddress of [a.miningTimelock,a.stakingTimelock]) {
      const lock = await ethers.getContractAt('VersionedGovernanceTimelock', lockAddress);
      expect(await lock.governor()).to.equal(a.governor);
      expect(await lock.timingModule()).to.equal(a.timing);
      expect(await lock.hasRole(await lock.PROPOSER_ROLE(),a.governor)).to.equal(true);
      expect(await lock.getMinDelay()).to.equal(26);
    }
    for (const [address, account] of Object.entries(baseline)) {
      for (const [slot, value] of Object.entries(account.storage)) expect(await ethers.provider.getStorage(address,slot)).to.equal(value);
    }
    for (const row of bundle.preservedCode) expect(ethers.keccak256(await ethers.provider.getCode(row.address))).to.equal(row.codeHash);
    expect(await mining.eip712Domain()).to.deep.equal(domainBefore);
    await mining.delegateBySig(alice.address,bob.address,0,deadline,signed);
    expect(await mining.nonces(alice.address)).to.equal(1);
    await expect(mining.delegateBySig(alice.address,bob.address,0,deadline,signed)).to.be.revertedWithCustomError(mining,'InvalidNonce');
    await network.provider.send('evm_setNextBlockTimestamp',[Number(genesis+first*60n)]); await network.provider.send('evm_mine');
    expect(await staking.epochDuration()).to.equal(1800);
    expect(await mining.epochDuration()).to.equal(1800);
    expect(await factory.challengeSeconds()).to.equal(7200);
  });
});
