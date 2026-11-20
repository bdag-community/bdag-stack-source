// Candidate-only, offline artifact construction. Never rewrites a shipped bundle.
const fs=require('node:fs');
const path=require('node:path');
const assert=require('node:assert/strict');
const crypto=require('node:crypto');
const hre=require('hardhat');
const {bindRuntime,buildVersionedRuntimeBundle}=require('./versioned-runtime-bundle.cjs');
async function main(){
  assert.equal(hre.network.name,'hardhat');
  const {ethers,artifacts}=hre;
  const historical=await buildVersionedRuntimeBundle(hre);
  const a={...historical.addresses,
    freshStaking:ethers.getAddress('0x1000000000000000000000000000000000000020'),
    freshImplementation:ethers.getAddress('0x1000000000000000000000000000000000000021')};
  const old=new Map(historical.runtimes.map(r=>[r.address,r]));
  const genesis=historical.genesisTime;
  const short=value=>{const b=ethers.toUtf8Bytes(value);assert(b.length<32);const w=new Uint8Array(32);w.set(b);w[31]=b.length;return ethers.hexlify(w);};
  const domain=(name,address)=>({_cachedThis:address,_cachedChainId:1043,
    _cachedDomainSeparator:ethers.TypedDataEncoder.hashDomain({name,version:'2',chainId:1043,verifyingContract:address}),
    _hashedName:ethers.id(name),_hashedVersion:ethers.id('2'),_name:short(name),_version:short('2')});
  const rows=[
    ['SeparatedChainTallyReader',a.reader,{clockSource:a.stakingProxy}],
    ['SeparatedStaking',a.freshImplementation,{__self:a.freshImplementation,timingModule:a.timing,legacyClockSource:a.stakingProxy}],
    ['UUPSProxy',a.freshStaking,{}],
    ...[['miningCredential','BlockDAG Mining Vote',a.reader],['stakingCredential','BlockDAG Staking Vote',ethers.ZeroAddress]].map(([key,name,mining])=>
      ['SeparatedEpochCredential',a[key],{stakingSource:a.stakingProxy,miningSource:mining,_genesisTime:genesis,_epochDuration:60,...domain(name,a[key])}])
  ];
  const runtimes=[];
  for(const [contract,address,values]of rows){
    const bound=await bindRuntime(hre,contract,values);
    runtimes.push({contract,address,mode:old.has(address)?'replace':'create',previousCodeHash:old.get(address)?.codeHash??null,
      ...bound,codeHash:ethers.keccak256(bound.code)});
  }
  // Clone only existing reviewed routes to the new target. Old routes stay put
  // so already queued operations retain their target, policy and timelock ID.
  const gov=await artifacts.readArtifact('VersionedEpochGovernor');
  const info=await artifacts.getBuildInfo(`${gov.sourceName}:VersionedEpochGovernor`);
  const fields=info.output.contracts[gov.sourceName].VersionedEpochGovernor.storageLayout.storage;
  const root=label=>BigInt(fields.find(s=>s.label===label).slot);
  const coder=ethers.AbiCoder.defaultAbiCoder();
  const outer=(address,slot)=>ethers.keccak256(coder.encode(['address','uint256'],[address,slot]));
  const route=(address,selector)=>ethers.keccak256(coder.encode(['bytes4','bytes32'],[selector,outer(address,root('routes'))]));
  const baseline=require('./versioned-runtime-bundle.cjs').loadBaseline();
  const storage=baseline[a.governor].storage;
  const abi=(await artifacts.readArtifact('SeparatedStaking')).abi;
  const iface=new ethers.Interface(abi),routeCopies=[];
  for(const f of iface.fragments.filter(f=>f.type==='function')){
    const selector=iface.getFunction(f.format('sighash')).selector;
    const fromSlot=route(a.stakingProxy,selector),toSlot=route(a.freshStaking,selector);
    const value=storage[fromSlot];
    if(value&&BigInt(value)!==0n)routeCopies.push({selector,fromSlot,toSlot,value});
  }
  assert(routeCopies.length>0,'no reviewed staking routes');
  const fromOwnerSlot=outer(a.stakingProxy,root('targetOwnerDomain'));
  const toOwnerSlot=outer(a.freshStaking,root('targetOwnerDomain'));
  assert.equal(BigInt(storage[fromOwnerSlot]),2n,'staking owner domain');
  const bundle={schemaVersion:1,chainId:1043,status:'unqualified-no-live-schedule',installationAuthorized:false,
    addresses:a,genesisTime:genesis,cutoverRule:'first-new-reward-epoch-after-native-anchor-and-notice',
    ownership:'Governor -> StakingTimelock -> V2; no additional multisig execution gate',
    credentialDomainVersion:'2',
    runtimes,routeCopies,ownerDomainCopy:{fromSlot:fromOwnerSlot,toSlot:toOwnerSlot,value:storage[fromOwnerSlot]},
    preservedCode:[...historical.preservedCode,...historical.runtimes.filter(r=>!runtimes.some(n=>n.address===r.address)).map(r=>({address:r.address,codeHash:r.codeHash}))]};
  const out=path.resolve(__dirname,'../../out/separate-staking-v1-candidate');fs.mkdirSync(out,{recursive:true});
  const raw=JSON.stringify(bundle,null,2)+'\n';fs.writeFileSync(path.join(out,'runtime-bundle.json'),raw);
  const sha256=crypto.createHash('sha256').update(raw).digest('hex');
  fs.writeFileSync(path.join(out,'runtime-bundle.sha256'),sha256+'  runtime-bundle.json\n');
  console.log(JSON.stringify({out,sha256,runtimes:runtimes.length,routes:routeCopies.length,installationAuthorized:false}));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
