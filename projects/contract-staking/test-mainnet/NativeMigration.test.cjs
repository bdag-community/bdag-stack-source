const {expect}=require('chai');
const hre=require('hardhat');
const fs=require('node:fs');const path=require('node:path');

describe('Original Mainnet V1 to native DAO/separate V2 bundle (isolated chain 1404)',function(){
 this.timeout(180000);
 let result,save;
 before(async()=>{
  save=await hre.network.provider.send('evm_snapshot');
  const input=JSON.parse(fs.readFileSync(path.join(__dirname,'fixtures/canonical-input.json')));
  const {buildMainnetMigrationBundle}=require('../scripts/release/mainnet-migration-bundle.cjs');
  result=await buildMainnetMigrationBundle(hre,input);
 });
 after(async()=>{if(save)await hre.network.provider.send('evm_revert',[save]);});
 it('binds real original Mainnet clock and independent DAO owners, with no testnet keys or historical reward claims',async()=>{
  const {ethers}=hre,{addresses:a}=result;
  const v2=await ethers.getContractAt('MainnetStakingV2',a.stakingV2);
  const reader=await ethers.getContractAt('SeparatedChainTallyReader',a.reader);
  expect(await reader.genesisTime()).to.equal(1770760980n);
  expect(await reader.clockSource()).to.equal(a.legacy);
  expect(await reader.activeStakingSource()).to.equal(a.stakingV2);
  expect(await v2.owner()).to.equal(a.stakingTimelock);
  expect(await v2.bondingPeriod()).to.equal(4n*604800n);
  expect(await v2.unbondingPeriod()).to.equal(3n*604800n);
  for(const role of ['miningTimelock','stakingTimelock']) {
   const lock=await ethers.getContractAt('VersionedGovernanceTimelock',a[role]);
   expect(await lock.governor()).to.equal(a.governor);
   expect(await lock.getMinDelay()).to.equal(259200n);
   expect(await lock.hasRole(await lock.PROPOSER_ROLE(),a.governor)).to.equal(true);
   expect(await lock.hasRole(await lock.EXECUTOR_ROLE(),a.governor)).to.equal(true);
   expect(await lock.hasRole(await lock.EXECUTOR_ROLE(),ethers.ZeroAddress)).to.equal(false);
  }
  for(const role of ['miningCredential','stakingCredential']) {
   const c=await ethers.getContractAt('SeparatedEpochCredential',a[role]);
   expect(await c.genesisTime()).to.equal(1770760980n);
   expect(await c.epochDuration()).to.equal(604800n);
   expect(await c.votingReady()).to.equal(false);
  }
 });
 it('exports only fresh-account constructor state, pinned legacy code and reproducible London runtimes',async()=>{
  const {bundle}=result;
  expect(bundle.chainId).to.equal(1404);
  expect(bundle.accounts.length).to.equal(19);
  expect(new Set(bundle.accounts.map(x=>x.address)).size).to.equal(19);
  for(const a of bundle.accounts) {
   expect(hre.ethers.keccak256(a.code)).to.equal(a.codeHash);
   expect(hre.ethers.getBytes(a.code).length).to.be.at.most(24576);
   expect(a.nonce).to.be.at.least(1);
  }
  expect(bundle.accounts.find(x=>x.role==='reader').storage).to.deep.equal({});
  expect(bundle.accounts.find(x=>x.role==='stakingV2').storage).to.deep.equal({});
  expect(bundle.accounts.find(x=>x.role==='factory').nonce).to.equal(2);
  expect(bundle.legacyImplementationCodeHash).to.equal('0x2f0ad946ee06e0351e743f9d25fc4363377d4d7007fa3ee1fe6855e1f68d0040');
 });
});
