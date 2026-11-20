const { expect } = require('chai');
const { ethers, network } = require('hardhat');
const { time } = require('@nomicfoundation/hardhat-network-helpers');
const coder = ethers.AbiCoder.defaultAbiCoder();
const { canonicalProxyCreation } = require('./fixtures/canonical-proxy.cjs');
const LEGACY_PROXY='0x1000000000000000000000000000000000000005';
const IMPL_SLOT='0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const ADMIN_SLOT='0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103';
const MINING = 1, STAKING = 2, CORE = 3, PARAM = 1, CRITICAL = 2;
const params = (chamber) => coder.encode(['uint8','bytes'], [chamber,'0x']);

describe('Phase 1 governance controls', function () {
  async function fixture(legacyHub=false) {
    const people = await ethers.getSigners();
    expect(people.length).to.be.at.least(60);
    const now = await time.latest();
    const start = now - 600, epochSeconds = 60;
    const Credential = await ethers.getContractFactory('GovernanceCredentialFixture');
    const mining = await Credential.deploy(start, epochSeconds);
    const staking = await Credential.deploy(start, epochSeconds);
    const Target = await ethers.getContractFactory('GovernanceTargetFixture');
    const target = await Target.deploy();
    const stakingTarget = await Target.deploy();
    const deployer = people[0];
    const nonce = await ethers.provider.getTransactionCount(deployer.address);
    const predictedGovernor = ethers.getCreateAddress({ from: deployer.address, nonce: nonce + 3 });
    const predictedHub = ethers.getCreateAddress({ from: deployer.address, nonce: nonce + 4 });
    const predictedRegistry = ethers.getCreateAddress({ from: deployer.address, nonce: nonce + 5 });
    const Lock = await ethers.getContractFactory('GovernanceTimelock');
    const lock = await Lock.deploy(5, predictedGovernor);
    const stakingLock = await Lock.deploy(5, predictedGovernor);
    const Policy = await ethers.getContractFactory('GovernancePolicyModule');
    const policy = await Policy.deploy(predictedGovernor, lock.target);
    const route = (name, domain, tier) => ({ target: name.startsWith('staking') ? stakingTarget.target : target.target, selector: target.interface.getFunction(name).selector, domain, tier, ownerDomain: name.startsWith('staking') ? STAKING : MINING });
    const Governor = await ethers.getContractFactory('EpochGovernor');
    const governor = await Governor.deploy(mining.target, staking.target, lock.target, stakingLock.target, policy.target, { epochSeconds, ordinaryDelay:5, criticalDelay:15, proposalBond:1000 }, [route('mining', MINING, PARAM), route('staking', STAKING, PARAM), route('coreOrdinary', CORE, PARAM), route('core', CORE, CRITICAL), route('upgradeToAndCall', STAKING, PARAM), route('stakingCritical', STAKING, CRITICAL), route('setBlockedBatch', MINING, PARAM), {target:policy.target, selector:policy.interface.getFunction('schedulePolicy').selector, domain:CORE, tier:CRITICAL, ownerDomain:MINING}, {target:legacyHub?LEGACY_PROXY:predictedHub, selector:ethers.id('announceRelease(uint256,string,string)').slice(0,10), domain:MINING, tier:PARAM, ownerDomain:MINING}, {target:legacyHub?LEGACY_PROXY:predictedHub, selector:ethers.id('upgradeToAndCall(address,bytes)').slice(0,10), domain:MINING, tier:PARAM, ownerDomain:MINING}, {target:predictedRegistry, selector:ethers.id('setPolicy(bytes32,string,bytes32)').slice(0,10), domain:MINING, tier:PARAM, ownerDomain:MINING}]);
    const Hub=await ethers.getContractFactory('GovernanceHubV2');
    const hubImplementation=await Hub.deploy(lock.target,governor.target);
    let hub=hubImplementation;
    if(legacyHub) {
      const creation=ethers.getBytes(canonicalProxyCreation());
      const delimiter=creation.findIndex((byte,i)=>byte===0xf3&&creation[i+1]===0xfe);
      expect(ethers.keccak256(creation)).to.equal('0xe26ae18ca3524ca816bba4a77eb909af5b1566bfcf8b4c0f0a663512f8a86399');
      expect(delimiter+2).to.equal(1543);
      expect(ethers.keccak256(creation.slice(delimiter+2))).to.equal('0x75498dc0fa24902bbd116019b098f2e3c8474c5c1f26213d56594a6457e3a7ed');
      await network.provider.send('hardhat_setCode',[LEGACY_PROXY,ethers.hexlify(creation.slice(delimiter+2))]);
      await network.provider.send('hardhat_setStorageAt',[LEGACY_PROXY,IMPL_SLOT,ethers.zeroPadValue(hubImplementation.target,32)]);
      await network.provider.send('hardhat_setStorageAt',[LEGACY_PROXY,ADMIN_SLOT,ethers.zeroPadValue(lock.target,32)]);
      hub=Hub.attach(LEGACY_PROXY);
    }
    const registry=await ethers.deployContract('MiningPolicyRegistry',[lock.target]);
    await target.setOwner(lock.target);
    await stakingTarget.setOwner(stakingLock.target);
    for (let i = 1; i <= 10; i++) await mining.setWeight(people[i].address, 100);
    for (let i = 11; i <= 35; i++) await staking.setWeight(people[i].address, 40);
    return { people, start, mining, staking, target, stakingTarget, lock, stakingLock, policy, governor, hub, hubImplementation, registry };
  }
  async function proposal(f, name='core', description='proposal') {
    const data = f.target.interface.encodeFunctionData(name, name === 'upgradeToAndCall' ? [f.people[58].address,'0x'] : [42]);
    const args = [[name.startsWith('staking') ? f.stakingTarget.target : f.target.target],[0],[data],description];
    await f.governor.connect(f.people[name.startsWith('staking') ? 11 : 1]).proposeWithDeposit(...args, { value: 1000 });
    const id = await f.governor.hashProposal(args[0],args[1],args[2],ethers.id(description));
    return { id, args, execution: [...args.slice(0,3), ethers.id(description)] };
  }
  async function nextEpoch(f, n=1) {
    const epoch = await f.mining.currentEpoch();
    await time.increaseTo(f.start + Number(epoch + BigInt(n)) * 60);
  }
  async function voteCore(f,p,minerSupport=1,stakerSupport=1) {
    for (let i=1;i<=10;i++) await f.governor.connect(f.people[i]).castVoteWithReasonAndParams(p.id,minerSupport,'',params(MINING));
    for (let i=11;i<=35;i++) await f.governor.connect(f.people[i]).castVoteWithReasonAndParams(p.id,stakerSupport,'',params(STAKING));
  }
  it('votes throughout exactly the next full epoch and executes dual-consent CORE through a real timelock', async function () {
    const f=await fixture(), p=await proposal(f);
    expect(await f.governor.state(p.id)).to.equal(0);
    await expect(f.governor.connect(f.people[1]).castVoteWithReasonAndParams(p.id,1,'',params(MINING))).to.be.reverted;
    await nextEpoch(f);
    expect(await f.governor.state(p.id)).to.equal(1);
    await voteCore(f,p,1,1);
    const epoch=await f.mining.currentEpoch();
    await time.increaseTo(f.start + Number(epoch+1n)*60-1);
    expect(await f.governor.state(p.id)).to.equal(1);
    await time.increase(1);
    expect(await f.governor.state(p.id)).to.equal(4);
    await f.governor.queue(...p.execution);
    const eta=await f.governor.proposalEta(p.id);
    await expect(f.governor.execute(...p.execution)).to.be.reverted;
    await time.increaseTo(eta);
    await f.governor.execute(...p.execution);
    expect(await f.target.value()).to.equal(42);
    await expect(f.governor.execute(...p.execution)).to.be.reverted;
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(p.id)).to.changeEtherBalance(f.people[1], 1000);
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(p.id)).to.be.reverted;
  });
  it('rejects 30% CORE, absent chambers, unknown selectors, relays, and role or upgrade bypasses', async function () {
    const f=await fixture(), p=await proposal(f);
    const escalated=await proposal(f,'upgradeToAndCall','upgrade');
    expect((await f.governor.proposalPolicy(escalated.id)).domain).to.equal(CORE);
    expect((await f.governor.proposalPolicy(escalated.id)).tier).to.equal(CRITICAL);
    await expect(f.governor.connect(f.people[1]).proposeWithDeposit([f.target.target],[0],['0x12345678'],'unknown',{value:1000})).to.be.reverted;
    await expect(f.lock.grantRole(await f.lock.PROPOSER_ROLE(),f.people[1].address)).to.be.reverted;
    await expect(f.lock.connect(f.people[59]).schedule(f.target.target,0,p.args[2][0],ethers.ZeroHash,ethers.ZeroHash,5)).to.be.reverted;
    await expect(f.governor.relay(f.target.target,0,p.args[2][0])).to.be.reverted;
    await nextEpoch(f); await voteCore(f,p,0,1); await nextEpoch(f);
    expect(await f.governor.state(p.id)).to.equal(3);
    expect(await f.governor.state(escalated.id)).to.equal(3);
    await expect(f.governor.queue(...p.execution)).to.be.reverted;
  });
  it('requires support in both CORE chambers even when the 70/30 score would otherwise pass', async function () {
    const f=await fixture(), p=await proposal(f);
    await nextEpoch(f); await voteCore(f,p,1,0); await nextEpoch(f);
    expect(await f.governor.state(p.id)).to.equal(3);
  });
  it('passes ordinary CORE with the agreed turnout and support rules and no address-count floor', async function () {
    const f=await fixture(), p=await proposal(f,'coreOrdinary');
    await nextEpoch(f);
    for(let i=1;i<=2;i++) await f.governor.connect(f.people[i]).castVoteWithReasonAndParams(p.id,1,'',params(MINING));
    for(let i=11;i<=13;i++) await f.governor.connect(f.people[i]).castVoteWithReasonAndParams(p.id,1,'',params(STAKING));
    await nextEpoch(f);
    expect(await f.governor.state(p.id)).to.equal(4);
    await f.governor.queue(...p.execution);
    const eta=await f.governor.proposalEta(p.id);
    expect(eta-BigInt(await time.latest())).to.equal(5);
  });
  it('forces every blocklist mutation selector through critical CORE policy', async function () {
    const f=await fixture();
    const data=f.target.interface.encodeFunctionData('setBlockedBatch',[[f.people[58].address],true]);
    const args=[[f.target.target],[0],[data],'blocklist change'];
    await f.governor.connect(f.people[1]).proposeWithDeposit(...args,{value:1000});
    const id=await f.governor.hashProposal(...args.slice(0,3),ethers.id(args[3]));
    const policy=await f.governor.proposalPolicy(id);
    expect(policy.domain).to.equal(CORE); expect(policy.tier).to.equal(CRITICAL);
  });
  it('snapshots bounded policy versions so future amendments cannot rewrite open votes', async function () {
    const f=await fixture();
    const rules={miningOrdinaryTurnout:30,miningCriticalTurnout:30,stakingOrdinaryTurnout:20,stakingCriticalTurnout:20,ordinaryCoreChamberSupport:45,ordinaryCoreWeightedSupport:55,criticalCoreChamberSupport:55,criticalCoreWeightedSupport:65,specialistOrdinarySupport:55,specialistCriticalSupport:65};
    const effective=(await f.mining.currentEpoch())+4n;
    const amendData=f.policy.interface.encodeFunctionData('schedulePolicy',[rules,effective]);
    const amendArgs=[[f.policy.target],[0],[amendData],'strengthen policy'];
    await f.governor.connect(f.people[1]).proposeWithDeposit(...amendArgs,{value:1000});
    const amendId=await f.governor.hashProposal(...amendArgs.slice(0,3),ethers.id(amendArgs[3]));
    expect(await f.governor.proposalPolicyVersion(amendId)).to.equal(1);
    await nextEpoch(f); await voteCore(f,{id:amendId}); await nextEpoch(f);
    const execution=[...amendArgs.slice(0,3),ethers.id(amendArgs[3])];
    await f.governor.queue(...execution); await time.increaseTo(await f.governor.proposalEta(amendId));
    await f.governor.execute(...execution);
    expect(await f.policy.versionAt(effective-1n)).to.equal(1);
    expect(await f.policy.versionAt(effective)).to.equal(2);

    const before=await proposal(f,'coreOrdinary','old policy snapshot');
    expect(await f.governor.proposalPolicyVersion(before.id)).to.equal(1);
    await time.increaseTo(f.start+Number(effective)*60);
    const after=await proposal(f,'coreOrdinary','new policy snapshot');
    expect(await f.governor.proposalPolicyVersion(after.id)).to.equal(2);
  });
  it('allows vote changes and counts every earned unit without wallet or delegate caps', async function () {
    const f=await fixture();
    await f.mining.setWeight(f.people[1].address,5000);
    const p=await proposal(f,'mining'); await nextEpoch(f);
    await f.governor.connect(f.people[1]).castVoteWithReasonAndParams(p.id,0,'',params(MINING));
    await f.governor.connect(f.people[1]).castVoteWithReasonAndParams(p.id,1,'',params(MINING));
    let tally=await f.governor.chamberVotes(p.id,MINING);
    expect(tally.againstVotes).to.equal(0); expect(tally.forVotes).to.equal(5000); expect(tally.voters).to.equal(1);
    await expect(f.governor.connect(f.people[11]).castVoteWithReasonAndParams(p.id,1,'',params(STAKING))).to.be.reverted;
    await nextEpoch(f);
    expect(await f.governor.state(p.id)).to.equal(4);
    await f.governor.queue(...p.execution); const eta=await f.governor.proposalEta(p.id);
    expect(eta-BigInt(await time.latest())).to.equal(5);
    await time.increaseTo(eta); await f.governor.execute(...p.execution);
  });
  it('exposes no Guardian cancellation or execution authority', async function () {
    const f=await fixture(), p=await proposal(f);
    expect(f.governor.interface.hasFunction('guardianCancel')).to.equal(false);
    expect(f.governor.interface.hasFunction('guardian')).to.equal(false);
    expect(f.lock.interface.hasFunction('bootstrap')).to.equal(false);
    expect(f.lock.interface.hasFunction('finishBootstrap')).to.equal(false);
    await nextEpoch(f); await voteCore(f,p,1,1); await nextEpoch(f); await f.governor.queue(...p.execution);
    const ids=await f.governor.operationIds(p.id);
    await expect(f.lock.connect(f.people[59]).cancel(ids.mining)).to.be.reverted;
    await expect(f.lock.connect(f.people[59]).executeBatch(p.args[0],p.args[1],p.args[2],ethers.ZeroHash,ethers.ZeroHash)).to.be.reverted;
  });
  it('partitions a mixed batch to both owner timelocks and rolls back both on failure', async function () {
    const f=await fixture();
    const dataM=f.target.interface.encodeFunctionData('mining',[88]);
    const dataS=f.stakingTarget.interface.encodeFunctionData('staking',[99]);
    const args=[[f.target.target,f.stakingTarget.target],[0,0],[dataM,dataS],'mixed'];
    await f.governor.connect(f.people[1]).proposeWithDeposit(...args,{value:1000});
    const id=await f.governor.hashProposal(...args.slice(0,3),ethers.id(args[3]));
    const execution=[...args.slice(0,3),ethers.id(args[3])];
    expect((await f.governor.proposalPolicy(id)).domain).to.equal(CORE);
    await nextEpoch(f); await voteCore(f,{id},1,1); await nextEpoch(f);
    await f.governor.queue(...execution);
    const ids=await f.governor.operationIds(id), eta=await f.governor.proposalEta(id);
    expect(await f.lock.getTimestamp(ids.mining)).to.equal(eta);
    expect(await f.stakingLock.getTimestamp(ids.staking)).to.equal(eta);
    await expect(f.lock.executeBatch([f.target.target],[0],[dataM],ethers.ZeroHash,ethers.ZeroHash)).to.be.reverted;
    await f.stakingTarget.setFail(true); await time.increaseTo(eta);
    await expect(f.governor.execute(...execution)).to.be.revertedWith('fixture failure');
    expect(await f.target.value()).to.equal(0); expect(await f.stakingTarget.value()).to.equal(0);
    expect(await f.lock.isOperationPending(ids.mining)).to.equal(true);
    expect(await f.stakingLock.isOperationPending(ids.staking)).to.equal(true);
    expect(await f.governor.executingCoreProposal()).to.equal(0);
    // Once queued, neither the proposer nor a retired emergency key may
    // cancel. A later critical CORE proposal is the privileged route.
    await expect(f.governor.connect(f.people[1]).cancel(...execution)).to.be.reverted;
  });
  it('counts abstain in CRITICAL support and uses no distinct-address floor', async function () {
    const f=await fixture(), p=await proposal(f,'stakingCritical');
    await nextEpoch(f);
    for(let i=11;i<=25;i++) await f.governor.connect(f.people[i]).castVoteWithReasonAndParams(p.id,i<21?1:2,'',params(STAKING));
    await nextEpoch(f);
    expect(await f.governor.state(p.id)).to.equal(4); // 10/15 = 66.67%, above the 60% critical specialist floor
    await f.governor.queue(...p.execution);
    await time.increaseTo(await f.governor.proposalEta(p.id));
    await f.governor.execute(...p.execution);
    await expect(f.governor.connect(f.people[11]).withdrawProposalBond(p.id)).to.changeEtherBalance(f.people[11],1000);
  });
  for(const legacy of [false,true]) it(`emits decodable release strings through ${legacy?'the exact genesis proxy':'a direct hub'} only in CORE execution`, async function () {
    const f=await fixture(legacy), digest='a'.repeat(64), url='https://example.org/release';
    await expect(f.hub.announceRelease(42,url,digest)).to.be.reverted;
    const call=f.hub.interface.encodeFunctionData('announceRelease',[42,url,digest]);
    const args=[[f.hub.target],[0],[call],'release'];
    await f.governor.connect(f.people[1]).proposeWithDeposit(...args,{value:1000});
    const id=await f.governor.hashProposal(...args.slice(0,3),ethers.id(args[3]));
    expect((await f.governor.proposalPolicy(id)).domain).to.equal(CORE);
    await nextEpoch(f); await voteCore(f,{id}); await nextEpoch(f);
    const execution=[...args.slice(0,3),ethers.id(args[3])];
    await f.governor.queue(...execution); await time.increaseTo(await f.governor.proposalEta(id));
    const receipt=await (await f.governor.execute(...execution)).wait();
    const log=receipt.logs.find(x=>x.address.toLowerCase()===f.hub.target.toLowerCase());
    expect(log.topics.length).to.equal(2);
    const decoded=coder.decode(['string','string'],log.data);
    expect(decoded[0]).to.equal(url); expect(decoded[1]).to.equal(digest);
  });
  it('expires queued execution and refunds its bond; unseeded or zero supply cannot propose', async function () {
    const f=await fixture(), p=await proposal(f,'mining');
    await nextEpoch(f);
    for(let i=1;i<=5;i++) await f.governor.connect(f.people[i]).castVote(p.id,1);
    await nextEpoch(f); await f.governor.queue(...p.execution);
    await time.increaseTo((await f.governor.proposalEta(p.id))+121n);
    expect(await f.governor.state(p.id)).to.equal(6);
    await expect(f.governor.execute(...p.execution)).to.be.reverted;
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(p.id)).to.changeEtherBalance(f.people[1],1000);
    await f.mining.setReady(false);
    await expect(proposal(f,'mining','unseeded')).to.be.reverted;
  });

  it('preserves approved batch order by rejecting staking-then-mining interleaving', async function () {
    const f=await fixture();
    await expect(f.governor.connect(f.people[1]).proposeWithDeposit(
      [f.stakingTarget.target,f.target.target],[0,0],
      [f.stakingTarget.interface.encodeFunctionData('staking',[1]),f.target.interface.encodeFunctionData('mining',[2])],
      'wrong execution order',{value:1000})).to.be.reverted;
  });
  it('binds signed ballots to chain, contract and nonce while permitting a newly signed vote change', async function () {
    const f=await fixture(), p=await proposal(f,'mining'); await nextEpoch(f);
    const domain={name:await f.governor.name(),version:await f.governor.version(),chainId:8008,verifyingContract:f.governor.target};
    const types={Ballot:[{name:'proposalId',type:'uint256'},{name:'support',type:'uint8'},{name:'voter',type:'address'},{name:'nonce',type:'uint256'}]};
    const ballot={proposalId:p.id,support:0,voter:f.people[1].address,nonce:0};
    const signature=await f.people[1].signTypedData(domain,types,ballot);
    const wrong=await f.people[1].signTypedData({...domain,chainId:1404},types,ballot);
    await expect(f.governor.castVoteBySig(p.id,0,ballot.voter,wrong)).to.be.reverted;
    await f.governor.castVoteBySig(p.id,0,ballot.voter,signature);
    await expect(f.governor.castVoteBySig(p.id,0,ballot.voter,signature)).to.be.reverted;
    const replacement=await f.people[1].signTypedData(domain,types,{...ballot,nonce:1,support:1});
    await f.governor.castVoteBySig(p.id,1,ballot.voter,replacement);
    const tally=await f.governor.chamberVotes(p.id,MINING);
    expect(tally.againstVotes).to.equal(0); expect(tally.forVotes).to.equal(100); expect(tally.voters).to.equal(1);
  });

  it('upgrades the hub through the exact genesis proxy only after CORE approval; compromised admin metadata cannot authorize it', async function () {
    const f=await fixture(true);
    const candidate=await ethers.deployContract('GovernanceHubUpgradeFixture',[f.lock.target,f.governor.target]);
    const upgradeInterface=new ethers.Interface(['function upgradeToAndCall(address implementation,bytes data) payable']);
    const call=upgradeInterface.encodeFunctionData('upgradeToAndCall',[candidate.target,'0x']);
    const legacyUpgrade=new ethers.Contract(LEGACY_PROXY,upgradeInterface,f.people[0]);
    await expect(legacyUpgrade.upgradeToAndCall(candidate.target,'0x')).to.be.reverted;
    await expect(legacyUpgrade.connect(f.people[59]).upgradeToAndCall(candidate.target,'0x')).to.be.reverted;
    await expect(new ethers.Contract(f.hubImplementation.target,upgradeInterface,f.people[0]).upgradeToAndCall(candidate.target,'0x')).to.be.reverted;
    const args=[[LEGACY_PROXY],[0],[call],'upgrade actual hub'];
    await f.governor.connect(f.people[1]).proposeWithDeposit(...args,{value:1000});
    const id=await f.governor.hashProposal(...args.slice(0,3),ethers.id(args[3]));
    expect((await f.governor.proposalPolicy(id)).domain).to.equal(CORE);
    // Slot is metadata for this forwarding proxy; even substituting the old key gives no authority.
    await network.provider.send('hardhat_setStorageAt',[LEGACY_PROXY,ADMIN_SLOT,ethers.zeroPadValue(f.people[0].address,32)]);
    await expect(legacyUpgrade.upgradeToAndCall(candidate.target,'0x')).to.be.reverted;
    await network.provider.send('hardhat_setStorageAt',[LEGACY_PROXY,ADMIN_SLOT,ethers.zeroPadValue(f.lock.target,32)]);
    await nextEpoch(f);await voteCore(f,{id});await nextEpoch(f);
    const execution=[...args.slice(0,3),ethers.id(args[3])];
    await f.governor.queue(...execution);await time.increaseTo(await f.governor.proposalEta(id));
    await f.governor.execute(...execution);
    expect(await ethers.provider.getStorage(LEGACY_PROXY,IMPL_SLOT)).to.equal(ethers.zeroPadValue(candidate.target,32));
    expect(await candidate.attach(LEGACY_PROXY).marker()).to.equal(2);
  });

  it('keeps staking-only proposal readiness independent of an unsealed mining epoch',async function(){
    const f=await fixture();
    await f.mining.setReady(false);
    const p=await proposal(f,'staking');
    expect(await f.governor.state(p.id)).to.equal(0);
    await expect(proposal(f,'core','not-ready-core')).to.be.reverted;
  });

  it('records advisory mining policy through an actual miner-only ballot and MiningTimelock',async function(){
    const f=await fixture(),topic=ethers.id('mining-support-guidelines'),uri='ipfs://local-reviewed-document',hash=ethers.id('advisory policy');
    await expect(f.registry.setPolicy(topic,uri,hash)).to.be.reverted;
    await expect(f.registry.connect(f.people[59]).setPolicy(topic,uri,hash)).to.be.reverted;
    const data=f.registry.interface.encodeFunctionData('setPolicy',[topic,uri,hash]);
    const args=[[f.registry.target],[0],[data],'advisory mining policy'];
    await f.governor.connect(f.people[1]).proposeWithDeposit(...args,{value:1000});
    const id=await f.governor.hashProposal(...args.slice(0,3),ethers.id(args[3]));
    expect((await f.governor.proposalPolicy(id)).domain).to.equal(MINING);
    await nextEpoch(f);for(let i=1;i<=5;i++)await f.governor.connect(f.people[i]).castVote(id,1);
    await nextEpoch(f);const execution=[...args.slice(0,3),ethers.id(args[3])];
    await f.governor.queue(...execution);await time.increaseTo(await f.governor.proposalEta(id));
    await expect(f.governor.execute(...execution)).to.emit(f.registry,'PolicyRecorded').withArgs(topic,uri,hash);
    const policy=await f.registry.policies(topic);expect(policy.contentUri).to.equal(uri);expect(policy.contentHash).to.equal(hash);
  });
  it('bounds mining policy URIs and rejects zero topics/hashes even for its configured owner',async function(){
    const [owner]=await ethers.getSigners();const registry=await ethers.deployContract('MiningPolicyRegistry',[owner.address]);
    await expect(registry.setPolicy(ethers.ZeroHash,'ipfs://x',ethers.id('x'))).to.be.reverted;
    await expect(registry.setPolicy(ethers.id('x'),'ipfs://x',ethers.ZeroHash)).to.be.reverted;
    await expect(registry.setPolicy(ethers.id('x'),'',ethers.id('x'))).to.be.reverted;
    await expect(registry.setPolicy(ethers.id('x'),'a'.repeat(2049),ethers.id('x'))).to.be.reverted;
  });
  it('supports a120-bit denominator with overflow-safe maximum cross-products and rejects larger supplies',async function(){
    const f=await fixture(),limit=(1n<<120n)-1n;
    const perMiner=limit/10n;
    for(let i=1;i<=10;i++)await f.mining.setWeight(f.people[i].address,perMiner);
    const perStaker=limit/25n;
    for(let i=11;i<=35;i++)await f.staking.setWeight(f.people[i].address,perStaker);
    const p=await proposal(f,'core','maximum arithmetic');await nextEpoch(f);
    await voteCore(f,p);await nextEpoch(f);
    // Both sources approach2^120 and actualCOREsuccess evaluates the full normalized products.
    expect(await f.governor.state(p.id)).to.equal(4);
    const mining=await f.governor.chamberVotes(p.id,MINING),staking=await f.governor.chamberVotes(p.id,STAKING);
    expect(mining.total).to.equal(perMiner*10n);expect(staking.total).to.equal(perStaker*25n);
    await f.mining.setWeight(f.people[1].address,limit+1n);
    await expect(proposal(f,'mining','too large')).to.be.reverted;
  });

  it('returns canceled and defeated proposal bonds only to their proposer and exactly once', async function () {
    const f=await fixture(), canceled=await proposal(f,'core','cancel-refund');
    await expect(f.governor.connect(f.people[2]).withdrawProposalBond(canceled.id)).to.be.reverted;
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(canceled.id)).to.be.reverted;
    await f.governor.connect(f.people[1]).cancel(...canceled.execution);
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(canceled.id)).to.changeEtherBalance(f.people[1],1000);
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(canceled.id)).to.be.reverted;
    const defeated=await proposal(f,'core','defeat-refund');
    await nextEpoch(f,2);
    expect(await f.governor.state(defeated.id)).to.equal(3);
    await expect(f.governor.connect(f.people[2]).withdrawProposalBond(defeated.id)).to.be.reverted;
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(defeated.id)).to.changeEtherBalance(f.people[1],1000);
    await expect(f.governor.connect(f.people[1]).withdrawProposalBond(defeated.id)).to.be.reverted;
  });

});
