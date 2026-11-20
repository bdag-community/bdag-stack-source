const {expect}=require('chai');
const {ethers,network}=require('hardhat');
const {time}=require('@nomicfoundation/hardhat-network-helpers');
const set=(address,slot,value)=>network.provider.send('hardhat_setStorageAt',[address,ethers.toQuantity(slot),ethers.toBeHex(value,32)]);
// Illustrative policy preserving the agreed 3/7-day Mainnet minimums.
// The release manifest must explicitly select its reviewed epoch policy.
const epochPolicy={ordinaryNumerator:3,ordinaryDenominator:7,criticalEpochs:1,graceEpochs:2};
describe('Versioned proposal timing',function(){
 let clock,module,governor,other,genesis;
 beforeEach(async()=>{
  [governor,other]=await ethers.getSigners();
  const source=await ethers.deployContract('CredentialVotingSource');
  clock=await ethers.deployContract('VersionedChainTallyReader',[source.target]);genesis=await source.genesisTime();
  await set(clock.target,8n,2n);
  const base=BigInt(ethers.keccak256(ethers.toBeHex(8,32)));
  for(const [i,value]of[0n,genesis,60n,10n,genesis+600n,1800n].entries())await set(clock.target,base+BigInt(i),value);
  module=await ethers.deployContract('VersionedGovernanceTiming',[governor.address,clock.target,60,5,15,epochPolicy]);
 });
 it('keeps legacy voting deadlines, queue delay and grace on their original 60-second terms',async()=>{
  const terms=await module.terms(42,10,true);
  expect([...terms]).to.deep.equal([genesis+600n,genesis+660n,15n,120n]);
  await time.increaseTo(genesis+620n);expect(await module.phase(42,10)).to.equal(1);
  await time.increaseTo(genesis+660n);expect(await module.phase(42,10)).to.equal(2);
  expect(await clock.currentEpoch()).to.equal(10n); // New 30-minute epoch still active.
 });
 it('freezes new proposal start/end and scales ordinary delay and grace with its voting epoch',async()=>{
  await time.increaseTo(genesis+601n);await set(clock.target,4n,10n);
  await module.record(43,11,false);
  expect([...(await module.terms(43,11,false))]).to.deep.equal([genesis+2400n,genesis+4200n,772n,3600n]);
  expect(await module.phase(43,11)).to.equal(0);
  await time.increaseTo(genesis+2400n);expect(await module.phase(43,11)).to.equal(1);
  await time.increaseTo(genesis+4200n);expect(await module.phase(43,11)).to.equal(2);
 });
 it('uses a one-epoch critical delay and rejects unauthorized, repeated or mismatched records',async()=>{
  await time.increaseTo(genesis+601n);
  await expect(module.connect(other).record(1,11,true)).to.be.reverted;
  await expect(module.record(1,12,true)).to.be.reverted;
  await module.record(1,11,true);
  expect((await module.terms(1,11,true)).delay).to.equal(1800n);
  await expect(module.record(1,11,true)).to.be.reverted;
  await expect(module.terms(1,12,true)).to.be.reverted;
  await expect(module.terms(1,11,false)).to.be.reverted;
 });
 it('does not reopen old votes when a later native timestamp falls backwards',async()=>{
  await time.increaseTo(genesis+620n);
  await set(clock.target,9n,genesis+660n);
  expect(await module.phase(42,10)).to.equal(2);
  expect(await clock.currentEpoch()).to.equal(10n);
 });
 for(const duration of [60n,600n,1800n,3600n,604800n]){
  it(`scales new terms from the actual ${duration}-second epoch, not a fixed day count`,async()=>{
   const base=BigInt(ethers.keccak256(ethers.toBeHex(8,32)));
   await set(clock.target,base+5n,duration);
   await time.increaseTo(genesis+601n);
   await module.record(100,11,false);await module.record(101,11,true);
   const ordinary=await module.terms(100,11,false),critical=await module.terms(101,11,true);
   expect(ordinary.delay).to.equal((duration*3n+6n)/7n);
   expect(critical.delay).to.equal(duration);
   expect(ordinary.grace).to.equal(2n*duration);
   expect(ordinary.end-ordinary.start).to.equal(duration);
  });
 }
 it('freezes existing terms when a later epoch segment changes duration',async()=>{
  await time.increaseTo(genesis+601n);await module.record(100,11,true);
  const saved=[...(await module.terms(100,11,true))];
  const base=BigInt(ethers.keccak256(ethers.toBeHex(8,32)));
  await set(clock.target,8n,3n);
  for(const [i,value]of[12n,genesis+4200n,60n].entries())await set(clock.target,base+6n+BigInt(i),value);
  await time.increaseTo(genesis+2401n);await module.record(101,12,true);
  expect([...(await module.terms(100,11,true))]).to.deep.equal(saved);
  expect((await module.terms(101,12,true)).delay).to.equal(60n);
  expect((await module.terms(101,12,true)).grace).to.equal(120n);
 });
 it('accepts stronger policies in epoch units, including one-epoch ordinary delay',async()=>{
  const stronger=await ethers.deployContract('VersionedGovernanceTiming',[
   governor.address,clock.target,60,5,15,{ordinaryNumerator:1,ordinaryDenominator:1,criticalEpochs:1,graceEpochs:2}
  ]);
  expect([...(await stronger.delaysForDuration(1800))]).to.deep.equal([1800n,1800n,3600n]);
 });
 it('rejects zero, weaker or inverted epoch policy and invalid durations',async()=>{
  for(const update of [
   {ordinaryNumerator:0},{ordinaryDenominator:0},{ordinaryNumerator:2},
   {criticalEpochs:0},{graceEpochs:1},{ordinaryNumerator:8}
  ])await expect(ethers.deployContract('VersionedGovernanceTiming',[
   governor.address,clock.target,60,5,15,{...epochPolicy,...update}
  ])).to.be.reverted;
  await expect(module.delaysForDuration(0)).to.be.revertedWithCustomError(module,'InvalidTiming');
  await expect(module.delaysForDuration(1n<<64n)).to.be.revertedWithCustomError(module,'InvalidTiming');
  expect([...(await module.delaysForDuration(1))]).to.deep.equal([1n,1n,2n]);
 });
});
