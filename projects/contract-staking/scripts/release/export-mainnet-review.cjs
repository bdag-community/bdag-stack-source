// Public review ABIs/storage metadata only. Never exports an installable image.
const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');const crypto=require('node:crypto');
const hre=require('hardhat');
async function main(){
 assert.equal(hre.network.name,'hardhat','review export may not use a live RPC');
 const root=path.resolve(__dirname,'../..'),out=path.join(root,'abi/mainnet-review');fs.mkdirSync(out,{recursive:true});
 const manifest={schema:'bdag.mainnet-contract-review.v1',installationAuthorized:false,productionAddressesApproved:false,nativeInstallerSealed:false,contracts:[],inputs:{}};
 for(const file of ['package-lock.json','hardhat.config.js','contracts/staking/StakingPool.sol','contracts/staking/SeparatedStakingBase.sol','contracts/staking/MainnetLegacyStaking.sol','contracts/staking/MainnetStakingV2.sol'])manifest.inputs[file]=crypto.createHash('sha256').update(fs.readFileSync(path.join(root,file))).digest('hex');
 for(const name of ['MainnetLegacyStaking','MainnetStakingV2']){
  const a=await hre.artifacts.readArtifact(name),info=await hre.artifacts.getBuildInfo(a.sourceName+':'+name),compiled=info.output.contracts[a.sourceName][name];
  assert.equal(info.solcVersion,'0.8.24');assert.equal(info.input.settings.evmVersion,'london');
  const body=JSON.stringify(a.abi,null,2)+'\n';fs.writeFileSync(path.join(out,name+'.json'),body);
  const layout=JSON.stringify(compiled.storageLayout,null,2)+'\n';fs.writeFileSync(path.join(out,name+'.storage.json'),layout);
  manifest.contracts.push({name,source:a.sourceName,abiSha256:crypto.createHash('sha256').update(body).digest('hex'),storageSha256:crypto.createHash('sha256').update(layout).digest('hex'),compiler:info.solcLongVersion,evmVersion:info.input.settings.evmVersion,optimizer:info.input.settings.optimizer,unboundRuntimeBytes:(a.deployedBytecode.length-2)/2,constructorImmutableBindingRequired:true});
 }
 fs.writeFileSync(path.join(out,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
 console.log(JSON.stringify({exported:manifest.contracts.map(c=>c.name),installationAuthorized:false}));
}
main().catch(error=>{console.error(error.message);process.exitCode=1;});
