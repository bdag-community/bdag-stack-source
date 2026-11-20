const { expect } = require('chai');
const { ethers, network, artifacts } = require('hardhat');
const { time } = require('@nomicfoundation/hardhat-network-helpers');
const word = v => ethers.toBeHex(v, 32);
const set = (address, slot, value) => network.provider.send('hardhat_setStorageAt', [address, ethers.toQuantity(slot), word(value)]);
const coder = ethers.AbiCoder.defaultAbiCoder();
const params = chamber => coder.encode(['uint8', 'bytes'], [chamber, '0x']);
const epochPolicy = {ordinaryNumerator:3,ordinaryDenominator:7,criticalEpochs:1,graceEpochs:2};

describe('Candidate versioned Governor timing integration', function () {
  let snapshot, people, genesis, reader, timing, governor, target, otherTarget, mining, staking, lock, secondLock;
  beforeEach(async () => {
    snapshot = await network.provider.send('evm_snapshot');
    people = await ethers.getSigners(); genesis = BigInt(await time.latest());
    const source = await ethers.deployContract('GovernanceCredentialFixture', [genesis, 60]);
    reader = await ethers.deployContract('VersionedChainTallyReader', [source.target]);
    await set(reader.target, 8n, 2n);
    const base = BigInt(ethers.keccak256(word(8)));
    for (const [i,v] of [0n,genesis,60n,10n,genesis+600n,1800n].entries()) await set(reader.target, base+BigInt(i), v);
    mining = await ethers.deployContract('VersionedGovernanceCredentialFixture', [reader.target]);
    staking = await ethers.deployContract('VersionedGovernanceCredentialFixture', [reader.target]);
    target = await ethers.deployContract('GovernanceTargetFixture');
    otherTarget = await ethers.deployContract('GovernanceTargetFixture');
    const nonce = await ethers.provider.getTransactionCount(people[0].address);
    const address = ethers.getCreateAddress({from:people[0].address, nonce:nonce+4});
    timing = await ethers.deployContract('VersionedGovernanceTiming', [address, reader.target, 60, 5, 15, epochPolicy]);
    lock = await ethers.deployContract('VersionedGovernanceTimelock', [timing.target]);
    secondLock = await ethers.deployContract('VersionedGovernanceTimelock', [timing.target]);
    const policy = await ethers.deployContract('GovernancePolicyModule', [address, lock.target]);
    governor = await ethers.deployContract('VersionedEpochGovernor', [mining.target, staking.target, lock.target, secondLock.target, policy.target,
      {epochSeconds:60,ordinaryDelay:26,criticalDelay:60,proposalBond:1000},
      [...['coreOrdinary','core'].map((name,i)=>({target:target.target,selector:target.interface.getFunction(name).selector,domain:3,tier:i+1,ownerDomain:1})),
        {target:target.target,selector:target.interface.getFunction('inspectTiming').selector,domain:3,tier:2,ownerDomain:1},
        {target:otherTarget.target,selector:otherTarget.interface.getFunction('staking').selector,domain:3,tier:2,ownerDomain:2}], timing.target]);
    await target.setOwner(lock.target);
    await otherTarget.setOwner(secondLock.target);
    await mining.setWeight(people[1].address, 100);
    await staking.setWeight(people[2].address, 100);
  });
  afterEach(async()=>{await network.provider.send('evm_revert',[snapshot]);});
  async function propose(name='coreOrdinary', description='candidate') {
    const args=[[target.target],[0],[target.interface.encodeFunctionData(name,[42])],description];
    await governor.connect(people[1]).proposeWithDeposit(...args,{value:1000});
    const id=await governor.hashProposal(...args.slice(0,3),ethers.id(description));
    return {id,args,execution:[...args.slice(0,3),ethers.id(description)]};
  }
  async function vote(p, stakingYes=true) {
    await governor.connect(people[1]).castVoteWithReasonAndParams(p.id,1,'',params(1));
    await governor.connect(people[2]).castVoteWithReasonAndParams(p.id,stakingYes?1:0,'',params(2));
  }
  async function markAsExistingProposal(p) {
    // Old Governor proposals already occupy the unchanged Governor layout but
    // have no entry in the new timing module. Simulate exactly that absence.
    const base=BigInt(ethers.keccak256(coder.encode(['uint256','uint256'],[p.id,0])));
    for(let i=0n;i<6n;i++)await set(timing.target,base+i,0n);
  }
  it('preserves old vote closure, queue delay and expiry while the new epoch is still active',async()=>{
    await time.increaseTo(genesis+550n); const p=await propose(); await markAsExistingProposal(p);
    await time.increaseTo(genesis+600n); await vote(p);
    await time.increaseTo(genesis+660n);
    expect(await reader.currentEpoch()).to.equal(10n);
    expect(await governor.state(p.id)).to.equal(4);
    await expect(governor.connect(people[1]).castVoteWithReasonAndParams(p.id,1,'',params(1))).to.be.reverted;
    await governor.queue(...p.execution);
    const eta=await governor.proposalEta(p.id); expect(eta-BigInt(await time.latest())).to.equal(5n);
    await time.increaseTo(eta+121n); expect(await governor.state(p.id)).to.equal(6);
    await expect(governor.execute(...p.execution)).to.be.reverted;
  });
  it('freezes epoch-based voting, delay and grace and executes through the owner timelock',async()=>{
    await time.increaseTo(genesis+601n); const p=await propose();
    expect([...(await governor.proposalTiming(p.id))]).to.deep.equal([genesis+2400n,genesis+4200n,772n,3600n]);
    expect(await governor.state(p.id)).to.equal(0);
    await time.increaseTo(genesis+2400n); await vote(p);
    await time.increaseTo(genesis+4200n); expect(await governor.state(p.id)).to.equal(4);
    await governor.queue(...p.execution);const eta=await governor.proposalEta(p.id);
    expect(eta-BigInt(await time.latest())).to.equal(772n);
    await expect(governor.execute(...p.execution)).to.be.reverted;
    await time.increaseTo(eta); await governor.execute(...p.execution);
    expect(await target.value()).to.equal(42);expect(await governor.state(p.id)).to.equal(7);
    await expect(governor.execute(...p.execution)).to.be.reverted;
    await expect(governor.connect(people[1]).withdrawProposalBond(p.id)).to.changeEtherBalance(people[1],1000);
  });
  it('requires both chambers for new critical proposals and retains cancellation authority limits',async()=>{
    await time.increaseTo(genesis+601n);const p=await propose('core');
    expect((await governor.proposalTiming(p.id)).delay).to.equal(1800n);
    await expect(governor.connect(people[3]).cancel(...p.execution)).to.be.reverted;
    await time.increaseTo(genesis+2400n);await vote(p,false);
    await time.increaseTo(genesis+4200n);expect(await governor.state(p.id)).to.equal(3);
    await expect(governor.queue(...p.execution)).to.be.reverted;
  });
  it('uses proposal epoch terms even when the retained timelock storage contains the old three-day floor',async()=>{
    const build=await artifacts.getBuildInfo('contracts/governance/phase1/GovernanceTimelock.sol:GovernanceTimelock');
    const layout=build.output.contracts['contracts/governance/phase1/GovernanceTimelock.sol'].GovernanceTimelock.storageLayout;
    const slot=layout.storage.find(x=>x.label==='_minDelay').slot;
    await set(lock.target,BigInt(slot),259200n);
    await time.increaseTo(genesis+601n);const p=await propose();
    await time.increaseTo(genesis+2400n);await vote(p);
    await time.increaseTo(genesis+4200n);
    await expect(governor.queue(...p.execution)).not.to.be.reverted;
    expect(await governor.proposalEta(p.id)-BigInt(await time.latest())).to.equal(772n);
    expect(BigInt(await ethers.provider.getStorage(lock.target,slot))).to.equal(259200n);
    expect(await timing.activeQueueDelay()).to.equal(0n);
  });
  it('keeps queue context private to the Governor and retains frozen authority',async()=>{
    await expect(timing.beginQueue(1,1,false)).to.be.revertedWithCustomError(timing,'Unauthorized');
    await expect(timing.endQueue()).to.be.revertedWithCustomError(timing,'Unauthorized');
    await expect(lock.schedule(target.target,0,'0x',ethers.ZeroHash,ethers.ZeroHash,999999)).to.be.reverted;
    await expect(lock.scheduleBatch([target.target],[0],['0x'],ethers.ZeroHash,ethers.ZeroHash,999999)).to.be.reverted;
    await expect(lock.updateDelay(1)).to.be.revertedWithCustomError(lock,'AuthorityFrozen');
    await expect(lock.grantRole(await lock.EXECUTOR_ROLE(),people[0].address)).to.be.revertedWithCustomError(lock,'AuthorityFrozen');
    await expect(lock.executeBatch([target.target],[0],['0x'],ethers.ZeroHash,ethers.ZeroHash)).to.be.reverted;
    // Even the bound Governor cannot schedule outside its atomic queue path.
    await network.provider.send('hardhat_setBalance',[governor.target,word(ethers.parseEther('1'))]);
    await network.provider.send('hardhat_impersonateAccount',[governor.target]);
    const caller=await ethers.getSigner(governor.target);
    await expect(lock.connect(caller).scheduleBatch([target.target],[0],['0x'],ethers.ZeroHash,ethers.ZeroHash,999999)).to.be.reverted;
    await network.provider.send('hardhat_stopImpersonatingAccount',[governor.target]);
  });
  it('clears queue context on rejected proposals and cannot leave an authorization behind',async()=>{
    await time.increaseTo(genesis+601n);const p=await propose();
    await expect(governor.queue(...p.execution)).to.be.reverted;
    expect(await timing.activeQueueDelay()).to.equal(0n);
    expect(await governor.proposalEta(p.id)).to.equal(0n);
    expect(await governor.state(p.id)).to.equal(0n);
  });
  it('keeps execution-policy context Governor-only and clears it even after a rejected execution', async () => {
    await expect(timing.beginExecution(1)).to.be.revertedWithCustomError(timing, 'Unauthorized');
    await expect(timing.endExecution()).to.be.revertedWithCustomError(timing, 'Unauthorized');
    await time.increaseTo(genesis + 601n); const p = await propose();
    await expect(governor.execute(...p.execution)).to.be.reverted;
    expect(await timing.activeExecutionId()).to.equal(0);
    expect(await timing.legacyExecution()).to.equal(false);
    await time.increaseTo(genesis + 2400n); await vote(p);
    await time.increaseTo(genesis + 4200n); await governor.queue(...p.execution);
    await time.increaseTo(await governor.proposalEta(p.id)); await governor.execute(...p.execution);
    expect(await timing.activeExecutionId()).to.equal(0);
  });
  for (const legacy of [false, true]) it(`propagates ${legacy ? 'legacy' : 'new'} policy provenance through real two-chamber approval and timelock execution`, async () => {
    await time.increaseTo(genesis + 550n);
    const args = [[target.target], [0], [target.interface.encodeFunctionData('inspectTiming', [timing.target])], 'provenance'];
    await governor.connect(people[1]).proposeWithDeposit(...args, { value: 1000 });
    const execution = [...args.slice(0, 3), ethers.id(args[3])];
    const id = await governor.hashProposal(...execution);
    if (legacy) await markAsExistingProposal({ id });
    const terms = await governor.proposalTiming(id);
    await time.increaseTo(terms.start); await vote({ id });
    await time.increaseTo(terms.end); await governor.queue(...execution);
    await time.increaseTo(await governor.proposalEta(id)); await governor.execute(...execution);
    expect(await target.observedExecution()).to.equal(id);
    expect(await target.observedLegacy()).to.equal(legacy);
    expect(await timing.activeExecutionId()).to.equal(0);
    expect(await timing.legacyExecution()).to.equal(false);
  });
  it('enforces one critical epoch in both owner timelocks and rolls back mixed execution atomically',async()=>{
    await time.increaseTo(genesis+601n);
    const args=[[target.target,otherTarget.target],[0,0],[target.interface.encodeFunctionData('core',[42]),otherTarget.interface.encodeFunctionData('staking',[43])],'both owners'];
    await governor.connect(people[1]).proposeWithDeposit(...args,{value:1000});
    const execution=[...args.slice(0,3),ethers.id(args[3])],id=await governor.hashProposal(...execution);
    await time.increaseTo(genesis+2400n);await vote({id});
    await time.increaseTo(genesis+4200n);await governor.queue(...execution);
    const op=await governor.operationIds(id),eta=await governor.proposalEta(id);
    expect(eta-BigInt(await time.latest())).to.equal(1800n);
    expect(await lock.getTimestamp(op.mining)).to.equal(eta);
    expect(await secondLock.getTimestamp(op.staking)).to.equal(eta);
    expect(await timing.activeQueueDelay()).to.equal(0n);
    await expect(governor.execute(...execution)).to.be.reverted;
    await otherTarget.setFail(true);await time.increaseTo(eta);
    await expect(governor.execute(...execution)).to.be.reverted;
    expect(await target.value()).to.equal(0n);
    expect(await lock.isOperationDone(op.mining)).to.equal(false);
    await otherTarget.setFail(false);await governor.execute(...execution);
    expect(await target.value()).to.equal(42n);expect(await otherTarget.value()).to.equal(43n);
    expect(await governor.state(id)).to.equal(7n);
  });
  it('preserves queued timestamps through later epoch-length changes',async()=>{
    await time.increaseTo(genesis+601n);const p=await propose('core');
    await time.increaseTo(genesis+2400n);await vote(p);
    await time.increaseTo(genesis+4200n);await governor.queue(...p.execution);
    const eta=await governor.proposalEta(p.id),op=(await governor.operationIds(p.id)).mining;
    expect(await lock.getTimestamp(op)).to.equal(eta);
    const base=BigInt(ethers.keccak256(word(8)));
    await set(reader.target,8n,3n);
    for(const [i,v]of[13n,genesis+6000n,60n].entries())await set(reader.target,base+6n+BigInt(i),v);
    expect(await lock.getTimestamp(op)).to.equal(eta);
    await expect(governor.execute(...p.execution)).to.be.reverted;
    await time.increaseTo(eta);await governor.execute(...p.execution);
    expect(await governor.state(p.id)).to.equal(7);
  });
  it('queues a frozen shorter proposal after a longer epoch starts without replacing its agreed delay',async()=>{
    await time.increaseTo(genesis+601n);const p=await propose();
    const base=BigInt(ethers.keccak256(word(8)));
    await set(reader.target,8n,3n);
    for(const [i,v]of[12n,genesis+4200n,3600n].entries())await set(reader.target,base+6n+BigInt(i),v);
    await time.increaseTo(genesis+2400n);await vote(p);
    await time.increaseTo(genesis+4200n);
    expect(await lock.getMinDelay()).to.equal(1543n);
    await governor.queue(...p.execution);
    expect(await governor.proposalEta(p.id)-BigInt(await time.latest())).to.equal(772n);
    expect(await timing.activeQueueDelay()).to.equal(0n);
    expect(await lock.getMinDelay()).to.equal(1543n);
  });
  it('cannot reopen votes or an expired action when the native timestamp falls backwards',async()=>{
    await time.increaseTo(genesis+550n);const p=await propose();await markAsExistingProposal(p);
    await time.increaseTo(genesis+600n);await vote(p);
    await set(reader.target,9n,genesis+781n);
    expect(await governor.state(p.id)).to.equal(6);
    await expect(governor.queue(...p.execution)).to.be.reverted;
  });
  it('does not shorten a new queue delay when the timestamp is below the persisted governance clock',async()=>{
    await time.increaseTo(genesis+601n);const p=await propose('core');
    await time.increaseTo(genesis+2400n);await vote(p);
    await set(reader.target,9n,genesis+4200n);
    expect(await governor.state(p.id)).to.equal(4);
    await governor.queue(...p.execution);
    expect(await governor.proposalEta(p.id)).to.equal(genesis+4200n+1800n);
    await expect(governor.execute(...p.execution)).to.be.reverted;
  });
  it('can be constructed against the new active clock while pinning legacy terms independently',async()=>{
    await time.increaseTo(genesis+601n);
    const nonce=await ethers.provider.getTransactionCount(people[0].address);
    const predicted=ethers.getCreateAddress({from:people[0].address,nonce:nonce+4});
    const t=await ethers.deployContract('VersionedGovernanceTiming',[predicted,reader.target,60,5,15,epochPolicy]);
    const a=await ethers.deployContract('VersionedGovernanceTimelock',[t.target]);
    const b=await ethers.deployContract('VersionedGovernanceTimelock',[t.target]);
    const p=await ethers.deployContract('GovernancePolicyModule',[predicted,a.target]);
    const g=await ethers.deployContract('VersionedEpochGovernor',[mining.target,staking.target,a.target,b.target,p.target,
      {epochSeconds:1800,ordinaryDelay:772,criticalDelay:1800,proposalBond:1000},
      [{target:target.target,selector:target.interface.getFunction('core').selector,domain:3,tier:2,ownerDomain:1}],t.target]);
    expect(await g.epochSeconds()).to.equal(1800n);
    expect(await g.ordinaryDelay()).to.equal(772n);
    expect(await t.legacyDuration()).to.equal(60n);
    expect((await t.terms(42,10,true)).delay).to.equal(15n);
  });
  it('retains proposer cancellation and its refundable bond across epoch changes',async()=>{
    await time.increaseTo(genesis+550n);const p=await propose();await markAsExistingProposal(p);
    await governor.connect(people[1]).cancel(...p.execution);
    await time.increaseTo(genesis+2400n);
    expect(await governor.state(p.id)).to.equal(2);
    await expect(governor.queue(...p.execution)).to.be.reverted;
    await expect(governor.connect(people[1]).withdrawProposalBond(p.id)).to.changeEtherBalance(people[1],1000);
  });
  it('preserves every Governor storage slot and stays within the real EIP-170 limit',async()=>{
    const old=await artifacts.getBuildInfo('contracts/governance/phase1/EpochGovernor.sol:EpochGovernor');
    const fresh=await artifacts.getBuildInfo('contracts/governance/phase1/VersionedEpochGovernor.sol:VersionedEpochGovernor');
    const a=old.output.contracts['contracts/governance/phase1/EpochGovernor.sol'].EpochGovernor.storageLayout;
    const b=fresh.output.contracts['contracts/governance/phase1/VersionedEpochGovernor.sol'].VersionedEpochGovernor.storageLayout;
    expect(b.storage.map(x=>[x.label,x.slot,x.offset,b.types[x.type].label,b.types[x.type].numberOfBytes])).to.deep.equal(a.storage.map(x=>[x.label,x.slot,x.offset,a.types[x.type].label,a.types[x.type].numberOfBytes]));
    expect(ethers.getBytes(await ethers.provider.getCode(governor.target)).length).to.be.at.most(24576);
    const oldLock=await artifacts.getBuildInfo('contracts/governance/phase1/GovernanceTimelock.sol:GovernanceTimelock');
    const newLock=await artifacts.getBuildInfo('contracts/governance/phase1/VersionedGovernanceTimelock.sol:VersionedGovernanceTimelock');
    const c=oldLock.output.contracts['contracts/governance/phase1/GovernanceTimelock.sol'].GovernanceTimelock.storageLayout;
    const d=newLock.output.contracts['contracts/governance/phase1/VersionedGovernanceTimelock.sol'].VersionedGovernanceTimelock.storageLayout;
    expect(d.storage.map(x=>[x.label,x.slot,x.offset,d.types[x.type].label,d.types[x.type].numberOfBytes])).to.deep.equal(c.storage.map(x=>[x.label,x.slot,x.offset,c.types[x.type].label,c.types[x.type].numberOfBytes]));
    expect(ethers.getBytes(await ethers.provider.getCode(lock.target)).length).to.be.at.most(24576);
  });
});
