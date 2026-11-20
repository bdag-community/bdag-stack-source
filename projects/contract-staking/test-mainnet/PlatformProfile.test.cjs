const {expect}=require('chai');const fs=require('node:fs');const path=require('node:path');const hre=require('hardhat');
describe('Mainnet applications share the exact constructor/runtime profile',function(){
 let snapshot;beforeEach(async()=>{snapshot=await hre.network.provider.send('evm_snapshot');});afterEach(async()=>{await hre.network.provider.send('evm_revert',[snapshot]);});
 it('generates matched separate-address seven-day profiles while leaving installation unapproved',async()=>{
  const file=path.join(__dirname,'../scripts/release/mainnet-platform-profile.cjs');expect(fs.existsSync(file),'missing Mainnet application profile generator').to.equal(true);
  const result=await require('../scripts/release/export-mainnet-preparation.cjs').build(hre);
  const profile=require(file).platformProfile(result);
  expect(profile.chainId).to.equal(1404);expect(profile.epochSeconds).to.equal(604800);
  expect(profile.status).to.equal('review');expect(profile.installationAuthorized).to.equal(false);
  expect(profile.stakingProxy).to.equal('0x1000000000000000000000000000000000000020');
  expect(profile.legacyStakingProxy.toLowerCase()).to.equal('0x08bd519f611556dc148e7bffe8d6f078f23a8ef7');
  expect(profile.contracts.governor).to.equal(result.addresses.governor);
  expect(profile.epochClock.codeHash).to.equal(result.bundle.accounts.find(a=>a.role==='reader').codeHash);
  expect(profile.epochClock.activeStakingSource).to.equal(profile.stakingProxy);
  expect(profile.reviewers).to.deep.equal(result.metadata.reviewers);
  expect(profile.fundingEnabled).to.equal(false);
  const output=require(file).environmentTemplates(profile);
  expect(output.development.NEXT_PUBLIC_BDAG_CHAIN_ID).to.equal('1404');
  expect(output.staking.NEXT_PUBLIC_METAMASK_CHAIN_ID).to.equal('1404');
  expect(output.api.NETWORK_ID).to.equal('1404');
  expect(output.staking.NEXT_PUBLIC_LEGACY_ONLY).to.equal('false');
  expect(output.staking.NEXT_PUBLIC_LEGACY_REHEARSAL).to.equal('false');
 });
});
