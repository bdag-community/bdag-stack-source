const {expect}=require('chai');const hre=require('hardhat');const {time}=require('@nomicfoundation/hardhat-network-helpers');
const fs=require('node:fs');const path=require('node:path');
describe('Protected Legacy native-auto-stake-only exit',function(){
 let save,legacy,operator,other,genesis,epoch,amount,source;
 const WEEK=604800n,IMPL='0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
 beforeEach(async()=>{
  save=await hre.network.provider.send('evm_snapshot');
  const {ethers,network}=hre;const input=JSON.parse(fs.readFileSync(path.join(__dirname,'fixtures/canonical-input.json')));
  [,operator,other]=await ethers.getSigners();genesis=BigInt(input.legacyGenesisTime);epoch=BigInt(Math.floor((Number(await time.latest())-Number(genesis))/Number(WEEK)));
  amount=ethers.parseEther('10');source=input.legacy;
  const word=n=>ethers.toBeHex(n,32),map=(a,s)=>ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(['address','uint256'],[a,s]));
  const set=(slot,v)=>network.provider.send('hardhat_setStorageAt',[source,ethers.toQuantity(slot),v]);
  await network.provider.send('hardhat_setCode',[input.implementation,input.implementationCode]);await network.provider.send('hardhat_setCode',[source,input.legacyCode]);
  await set(IMPL,ethers.zeroPadValue(input.implementation,32));await set(8n,word(genesis));await set(map(operator.address,5n),word(amount));await set(map(operator.address,6n),word(amount));
  await set(ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(['uint256','bytes32'],[epoch,map(operator.address,7n)])),word(amount));
  await set(map(operator.address,BigInt(map(operator.address,12n))),word(amount));
  // The real native path records auto-stake as principal, but omits a first
  // deposit timestamp. Keep that exact combination in this regression fixture.
  await set(map(operator.address,BigInt(map(operator.address,14n))),word(amount));
  await set(map(operator.address,15n),word(amount));
  await network.provider.send('hardhat_setBalance',[source,ethers.toQuantity(amount)]);
  const safe=await ethers.deployContract('MainnetLegacyStaking');legacy=safe.attach(source);
  await set(IMPL,ethers.zeroPadValue(safe.target,32));
 });
 afterEach(async()=>{await hre.network.provider.send('evm_revert',[save]);});
 it('keeps the exact 52-epoch lock then returns earned operator funds without a new deposit',async()=>{
  expect(await legacy.userFirstDepositTimestamp(operator.address)).to.equal(0n);
  await time.increaseTo(genesis+(epoch+51n)*WEEK);
  await expect(legacy.connect(operator).claimStake(operator.address,operator.address)).to.be.revertedWithCustomError(legacy,'NothingToClaim');
  await time.increaseTo(genesis+(epoch+52n)*WEEK);
  await expect(legacy.connect(other).claimStake(operator.address,operator.address)).to.be.revertedWithCustomError(legacy,'NotAuthorized');
  await expect(legacy.connect(operator).claimStake(operator.address,operator.address)).to.changeEtherBalance(operator,amount);
  expect(await legacy.userFirstDepositTimestamp(operator.address)).to.equal(0n);
  expect(await legacy.stakerTotalLockedShares(operator.address,operator.address)).to.equal(0n);
 });
 it('does not let a missing timestamp bypass an ordinary pending-exit gate',async()=>{
  const {ethers,network,artifacts}=hre;
  const a=await artifacts.readArtifact('MainnetLegacyStaking'),build=await artifacts.getBuildInfo(a.sourceName+':MainnetLegacyStaking');
  const slot=build.output.contracts[a.sourceName].MainnetLegacyStaking.storageLayout.storage.find(s=>s.label==='unbondingRequests').slot;
  const key=ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(['address','uint256'],[operator.address,slot]));
  await network.provider.send('hardhat_setStorageAt',[source,key,ethers.toBeHex(1n,32)]);
  await time.increaseTo(genesis+(epoch+52n)*WEEK);
  await expect(legacy.connect(operator).claimStake(operator.address,operator.address)).to.be.revertedWith('No deposit found');
 });
});
