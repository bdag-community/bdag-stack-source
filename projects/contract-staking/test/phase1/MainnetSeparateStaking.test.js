const {expect}=require('chai');
const {ethers,network}=require('hardhat');
const {time}=require('@nomicfoundation/hardhat-network-helpers');
const READER='0x1000000000000000000000000000000000000002';
const IMPL='0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const word=n=>ethers.toBeHex(n,32),WEEK=604800n,UNIT=ethers.parseEther('1');
const set=(address,slot,value)=>network.provider.send('hardhat_setStorageAt',[address,ethers.toQuantity(slot),typeof value==='string'?value:word(value)]);
describe('Mainnet seven-day distinct-address staking contract candidate (isolated only)',function(){
 let snapshot,owner,alice,pool,manager,legacy,fresh,oldFirst,pending,lock;
 beforeEach(async()=>{
  snapshot=await network.provider.send('evm_snapshot');
  [owner,alice,pool,manager]=await ethers.getSigners();
  await network.provider.send('hardhat_setCode',[READER,'0x']);
  const old=await ethers.deployContract('StakingContract');
  const proxy=await ethers.deployContract('UUPSProxy',[old.target,old.interface.encodeFunctionData('initialize')]);legacy=old.attach(proxy.target);
  await legacy.addManagers([manager.address]);
  await legacy.connect(alice).stake(alice.address,10n*UNIT,pool.address,{value:10n*UNIT});oldFirst=await legacy.userFirstDepositTimestamp(alice.address);
  await time.increaseTo(oldFirst+4n*WEEK+1n);
  await legacy.connect(alice).unstake(alice.address,UNIT,[pool.address]);pending=await legacy.getUnbondingRequests(alice.address);
  const governor=await ethers.deployContract('GovernanceTargetFixture');
  lock=await ethers.deployContract('GovernanceTimelock',[3*86400,governor.target]);
  const safe=await ethers.deployContract('MainnetLegacyStaking');
  await set(proxy.target,BigInt(IMPL),ethers.zeroPadValue(safe.target,32));
  await set(proxy.target,0n,ethers.zeroPadValue(lock.target,32));legacy=safe.attach(proxy.target);
  const genesis=await legacy.genesisTime();
  const reader=await ethers.deployContract('SeparatedChainTallyReader',[proxy.target]);
  await network.provider.send('hardhat_setCode',[READER,await ethers.provider.getCode(reader.target)]);
  await set(READER,8n,1n);const base=BigInt(ethers.keccak256(word(8n)));
  for(const [i,v]of [0n,genesis,WEEK].entries())await set(READER,base+BigInt(i),v);
  const impl=await ethers.deployContract('MainnetStakingV2',[proxy.target]);
  const next=await ethers.deployContract('UUPSProxy',[impl.target,impl.interface.encodeFunctionData('initialize')]);fresh=impl.attach(next.target);
  await set(next.target,0n,ethers.zeroPadValue(lock.target,32));
  await set(next.target,8n,genesis);await set(READER,10n,ethers.zeroPadValue(next.target,32));await set(READER,11n,1n);
 });
 afterEach(async()=>{await network.provider.send('evm_revert',[snapshot]);});
 it('preserves original V1 pending exits, principal and old four-week bond independently of fresh V2',async()=>{
  expect(await legacy.owner()).to.equal(lock.target);expect(await fresh.owner()).to.equal(lock.target);
  expect(await legacy.getUnbondingRequests(alice.address)).to.deep.equal(pending);expect(await legacy.userFirstDepositTimestamp(alice.address)).to.equal(oldFirst);
  expect(await legacy.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(9n*UNIT);expect(await ethers.provider.getBalance(legacy.target)).to.equal(10n*UNIT);
  await fresh.connect(alice).stake(alice.address,2n*UNIT,pool.address,{value:2n*UNIT});
  const first=await fresh.userFirstDepositTimestamp(alice.address);expect(await fresh.bondingEndsAt(alice.address)).to.equal(first+4n*WEEK);
  expect(await fresh.epochDuration()).to.equal(WEEK);expect(await legacy.getUnbondingRequests(alice.address)).to.deep.equal(pending);
 });
 it('retired owner and pre-existing manager cannot change policy or rescue positions',async()=>{
  expect(await legacy.isManager(manager.address)).to.equal(true); // history retained; authority retired
  await expect(legacy.connect(owner).setGenesisTime(1)).to.be.revertedWithCustomError(legacy,'NotOwner');
  await expect(legacy.connect(manager).adminRescueDelegation([alice.address],[pool.address],[manager.address])).to.be.revertedWithCustomError(legacy,'NotAuthorized');
 });
 it('reads epoch-based policy overrides without changing existing bond and exit dates',async()=>{
  await fresh.connect(alice).stake(alice.address,2n*UNIT,pool.address,{value:2n*UNIT});
  const end=await fresh.bondingEndsAt(alice.address);
  await network.provider.send('hardhat_setBalance',[lock.target,ethers.toQuantity(UNIT)]);
  await network.provider.send('hardhat_impersonateAccount',[lock.target]);
  try {
   const governed=await ethers.getSigner(lock.target);
   await fresh.connect(governed).setBondingPeriod(2n*WEEK);
   expect(await fresh.bondingPeriod()).to.equal(2n*WEEK);
   const policy=await fresh.bondingEpochPolicy();expect(policy.numerator).to.equal(2n*WEEK);expect(policy.denominator).to.equal(WEEK);
   expect(await fresh.bondingEndsAt(alice.address)).to.equal(end);
   expect(await legacy.getUnbondingRequests(alice.address)).to.deep.equal(pending);
  } finally {await network.provider.send('hardhat_stopImpersonatingAccount',[lock.target]);}
 });

 it('credits and returns new V2 rewards without calling the pool or spending Legacy backing',async()=>{
  await fresh.connect(alice).stake(alice.address,2n*UNIT,pool.address,{value:2n*UNIT});
  const slot=ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(['address','uint256'],[pool.address,5n]));
  await set(fresh.target,BigInt(slot),3n*UNIT);
  await network.provider.send('hardhat_setBalance',[fresh.target,ethers.toQuantity(3n*UNIT)]);
  expect(await fresh.poolStakerYield(pool.address,alice.address)).to.equal(UNIT);
  const before=await ethers.provider.getBalance(legacy.target);
  await fresh.connect(alice).unstake(alice.address,3n*UNIT,[pool.address]);
  const request=(await fresh.getUnbondingRequests(alice.address))[0];const end=await fresh.bondingEndsAt(alice.address);
  await time.increaseTo(request.releaseTime>end?request.releaseTime:end);
  await expect(fresh.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,3n*UNIT);
  expect(await ethers.provider.getBalance(fresh.target)).to.equal(0n);expect(await ethers.provider.getBalance(legacy.target)).to.equal(before);
 });
 it('allows old claim while V2 is freshly bonded, then new rewards/unstake/claim without moving legacy backing',async()=>{
  const oldBalance=await ethers.provider.getBalance(legacy.target);
  await fresh.connect(alice).stake(alice.address,2n*UNIT,pool.address,{value:2n*UNIT});
  const end=await fresh.bondingEndsAt(alice.address);
  await fresh.connect(alice).unstake(alice.address,UNIT,[pool.address]);
  await time.increaseTo(pending[0].releaseTime);
  await expect(legacy.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,UNIT);
  expect(await ethers.provider.getBalance(legacy.target)).to.equal(oldBalance-UNIT);
  if(BigInt(await time.latest())<end)await expect(fresh.connect(alice).claimStake(alice.address,pool.address)).to.be.revertedWith('4-Week Gate Active');
  const requests=await fresh.getUnbondingRequests(alice.address);const release=requests[0].releaseTime>end?requests[0].releaseTime:end;
  await time.increaseTo(release);
  await expect(fresh.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,UNIT);
  expect(await legacy.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(9n*UNIT);
 });
});

describe('Mainnet legacy protection storage/ABI and runtime compatibility',function(){
 it('retains every original V1 storage field/type and public financial ABI, and both new runtimes fit EIP170',async()=>{
  const {artifacts}=require('hardhat');
  const layout=async(file,name)=>{const info=await artifacts.getBuildInfo(file+':'+name);return info.output.contracts[file][name].storageLayout;};
  const old=await layout('contracts/staking/StakingPool.sol','StakingContract');
  const next=await layout('contracts/staking/MainnetLegacyStaking.sol','MainnetLegacyStaking');
  const normalized=l=>{
   const type=id=>{const t=l.types[id];return{encoding:t.encoding,label:t.label.replaceAll('MainnetLegacyStaking.','StakingContract.'),bytes:t.numberOfBytes,...(t.key?{key:type(t.key)}:{}),...(t.value?{value:type(t.value)}:{}),...(t.base?{base:type(t.base)}:{}),...(t.members?{members:t.members.map(m=>({label:m.label,slot:m.slot,offset:m.offset,type:type(m.type)}))}: {})};};
   return l.storage.map(v=>({label:v.label,slot:v.slot,offset:v.offset,type:type(v.type)}));
  };
  expect(normalized(next)).to.deep.equal(normalized(old));
  const original=await artifacts.readArtifact('StakingContract'),protectedLegacy=await artifacts.readArtifact('MainnetLegacyStaking');
  expect(JSON.parse(JSON.stringify(protectedLegacy.abi).replaceAll('MainnetLegacyStaking.','StakingContract.'))).to.deep.equal(original.abi);
  for(const name of ['MainnetLegacyStaking','MainnetStakingV2'])expect(ethers.getBytes((await artifacts.readArtifact(name)).deployedBytecode).length).to.be.at.most(24576);
 });
});
