// Regenerates only the unqualified v0.2 candidate directory, never old exports.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const hre = require('hardhat');
const { buildVersionedRuntimeBundle } = require('./versioned-runtime-bundle.cjs');
async function main() {
  assert.equal(hre.network.name,'hardhat');
  assert.equal((await hre.ethers.provider.getNetwork()).chainId,1043n);
  const bundle=await buildVersionedRuntimeBundle(hre);
  const out=path.resolve(__dirname,'../../out/testnet-timing-v1-candidate');
  fs.mkdirSync(out,{recursive:true});
  const raw=JSON.stringify(bundle,null,2)+'\n';
  fs.writeFileSync(path.join(out,'runtime-bundle.json'),raw);
  for (const row of bundle.runtimes) fs.writeFileSync(path.join(out,row.contract+'.abi.json'),JSON.stringify(row.abi,null,2)+'\n');
  const sha256=crypto.createHash('sha256').update(raw).digest('hex');
  fs.writeFileSync(path.join(out,'runtime-bundle.sha256'),sha256+'  runtime-bundle.json\n');
  console.log(JSON.stringify({out,sha256,runtimes:bundle.runtimes.length,status:bundle.status,installationAuthorized:false}));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
