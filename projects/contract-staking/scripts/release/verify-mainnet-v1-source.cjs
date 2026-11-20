// Proves executable equivalence while recording, not concealing, CBOR metadata
// differences. The complete canonical runtime hash remains an independent pin.
const assert=require('node:assert/strict');
async function verifyMainnetV1Source(hre,input){
 const {ethers,artifacts}=hre;
 const a=await artifacts.readArtifact('StakingContract');
 const build=await artifacts.getBuildInfo(a.sourceName+':StakingContract');
 assert.equal(build.solcVersion,'0.8.24');assert.equal(build.input.settings.evmVersion,'london');
 const compiled=build.output.contracts[a.sourceName].StakingContract;
 const names=new Map();
 function visit(x){if(!x||typeof x!=='object')return;if(x.nodeType==='VariableDeclaration'&&x.mutability==='immutable')names.set(String(x.id),x.name);for(const v of Object.values(x))if(v&&typeof v==='object')Array.isArray(v)?v.forEach(visit):visit(v);}
 for(const source of Object.values(build.output.sources))visit(source.ast);
 const code=ethers.getBytes(a.deployedBytecode);
 for(const [id,refs]of Object.entries(compiled.evm.deployedBytecode.immutableReferences)){
  assert.equal(names.get(id),'__self','unreviewed V1 immutable');
  for(const {start,length}of refs){assert.equal(length,32);code.set(ethers.getBytes(ethers.zeroPadValue(input.implementation,32)),start);}
 }
 function executable(hex){
  const b=ethers.getBytes(hex),n=(b.at(-2)<<8)+b.at(-1);assert.equal(n,51,'reviewed Solidity metadata length');
  // Solidity may place immutable data tables between INVALID and CBOR. Only
  // the exact ipfs+solc-0.8.24 trailer is excluded; all tables remain compared.
  assert.match(ethers.hexlify(b.slice(-n-2)),/^0xa2646970667358221220[0-9a-f]{64}64736f6c63430008180033$/,'unreviewed metadata structure');
  return ethers.hexlify(b.slice(0,b.length-n-2));
 }
 const canonicalHash=ethers.keccak256(input.implementationCode);
 assert.equal(canonicalHash,'0x2f0ad946ee06e0351e743f9d25fc4363377d4d7007fa3ee1fe6855e1f68d0040');
 const rebuilt=ethers.hexlify(code);assert.equal(executable(rebuilt),executable(input.implementationCode),'V1 executable source mismatch');
 return {source:a.sourceName,compiler:build.solcLongVersion,canonicalCodeHash:canonicalHash,rebuiltCodeHash:ethers.keccak256(rebuilt),
  fullRuntimeMatch:rebuilt===input.implementationCode,metadataExcludedRuntimeMatch:true,executableHash:ethers.keccak256(executable(rebuilt)),
  scope:'Executable equality with reviewed UUPS self binding; CBOR metadata differs. Not a full state/liability replay proof.'};
}
module.exports={verifyMainnetV1Source};
