const fs=require('node:fs');
const path=require('node:path');
const assert=require('node:assert/strict');
const crypto=require('node:crypto');
const hre=require('hardhat');
const {buildVersionedRuntimeBundle}=require('./versioned-runtime-bundle.cjs');
async function main(){
  const core=process.env.BDAG_CORE_SOURCE_ROOT;assert(core,'BDAG_CORE_SOURCE_ROOT is required');
  const raw=fs.readFileSync(path.join(core,'bevm/bdag/consensus/bytecode/TestnetTimingV1.runtime-bundle.json'));
  assert.equal(crypto.createHash('sha256').update(raw).digest('hex'),'b8d100d8c69746070d02b497ee3e4c2837f9020981d036eff7d5a94ef61a5275');
  const frozen=JSON.parse(raw),compiled=await buildVersionedRuntimeBundle(hre);
  for(const row of frozen.runtimes){const rebuilt=compiled.runtimes.find(r=>r.address===row.address);assert(rebuilt);assert.equal(rebuilt.code,row.code,`historical runtime changed: ${row.contract}`);}
  console.log(JSON.stringify({historicalRuntimes:frozen.runtimes.length,byteIdentical:true}));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
