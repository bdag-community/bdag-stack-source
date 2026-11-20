// Exports the funding-module ABIs consumed by the local platform clients.
// No deployment, signing, or network access.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const hre=require('hardhat');
const OUT=path.resolve(__dirname,'../../out/platform-abi');
const names=['ApprovedDevRegistry','ProjectEscrow','ProjectEscrowFactory','BidBondVault','CommunityTreasury'];
const sha=value=>crypto.createHash('sha256').update(value).digest('hex');
async function main(){
  fs.mkdirSync(OUT,{recursive:true});const manifest={};
  for(const name of names){
    const artifact=await hre.artifacts.readArtifact(name),bytes=Buffer.from(artifact.deployedBytecode.slice(2),'hex');
    const output=JSON.stringify(artifact.abi,null,2)+'\n',file=path.join(OUT,`${name}.json`);
    fs.writeFileSync(file,output);manifest[`${name}.json`]={sha256:sha(output),runtimeBytes:bytes.length};
  }
  fs.writeFileSync(path.join(OUT,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
  console.log(JSON.stringify({out:OUT,contracts:names},null,2));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
