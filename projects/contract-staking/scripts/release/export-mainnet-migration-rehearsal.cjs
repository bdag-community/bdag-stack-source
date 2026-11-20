// Exports a review/test fixture only; never writes the compiled live profile.
const fs=require('node:fs');const path=require('node:path');const crypto=require('node:crypto');
const hre=require('hardhat');const {buildMainnetMigrationBundle}=require('./mainnet-migration-bundle.cjs');
async function main(){
 const root=path.resolve(__dirname,'../..');
 const input=JSON.parse(fs.readFileSync(path.join(root,'test-mainnet/fixtures/canonical-input.json')));
 const result=await buildMainnetMigrationBundle(hre,input);
 const out=path.resolve(process.env.MAINNET_REHEARSAL_OUTPUT||path.join(root,'out/mainnet-migration-review'));
 fs.mkdirSync(out,{recursive:true});
 const raw=JSON.stringify(result.bundle,null,2)+'\n';
 fs.writeFileSync(path.join(out,'bundle.json'),raw);
 fs.writeFileSync(path.join(out,'context.json'),JSON.stringify({metadata:result.metadata,genesisHeader:result.genesisHeader,baselineHeader:result.baselineHeader,prestate:input},null,2)+'\n');
 console.log(JSON.stringify({out,sha256:crypto.createHash('sha256').update(raw).digest('hex'),accounts:result.bundle.accounts.length,reviewOnly:true,installationAuthorized:false}));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
