// Local candidate construction only. Never regenerate historical allocations.
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const ROOT = path.resolve(__dirname, '../..');
const BASELINE_SHA256 = '304aeee52c451e383f1960bdc32d0415d1ad9bc3e0fe19dff95af197bc82d199';
function loadBaseline() {
  const raw = fs.readFileSync(path.join(ROOT,'test/phase1/fixtures/TestnetGovernanceBootstrap.alloc.json'));
  assert.equal(crypto.createHash('sha256').update(raw).digest('hex'),BASELINE_SHA256,'historical allocation changed');
  return JSON.parse(raw);
}

async function bindRuntime(hre, name, values) {
  const { ethers, artifacts } = hre;
  const artifact = await artifacts.readArtifact(name);
  const info = await artifacts.getBuildInfo(`${artifact.sourceName}:${name}`);
  assert.equal(info.solcVersion,'0.8.24');
  assert.equal(info.input.settings.evmVersion,'london');
  const compiled = info.output.contracts[artifact.sourceName][name];
  const declarations = new Map();
  const visit = node => {
    if (!node || typeof node !== 'object') return;
    if (node.nodeType === 'VariableDeclaration' && node.mutability === 'immutable') {
      declarations.set(String(node.id),node.name);
    }
    for (const value of Object.values(node)) {
      if (value && typeof value === 'object') Array.isArray(value) ? value.forEach(visit) : visit(value);
    }
  };
  Object.values(info.output.sources).forEach(source => visit(source.ast));
  const bytes = ethers.getBytes(artifact.deployedBytecode), bound = {};
  for (const [id, refs] of Object.entries(compiled.evm.deployedBytecode.immutableReferences || {})) {
    const field = declarations.get(id);
    assert(field && values[field] !== undefined,`unbound ${name}.${field || id}`);
    const encoded = ethers.toBeHex(BigInt(values[field]),32);
    bound[field] = encoded;
    for (const { start, length } of refs) {
      assert.equal(length,32); assert(start+length <= bytes.length);
      bytes.set(ethers.getBytes(encoded),start);
    }
  }
  assert(bytes.length > 0 && bytes.length <= 24576,'EIP-170');
  return {code:ethers.hexlify(bytes),immutables:bound,abi:artifact.abi,
    compiler:{version:info.solcVersion,evmVersion:'london',optimizer:info.input.settings.optimizer,viaIR:info.input.settings.viaIR === true}};
}

async function buildVersionedRuntimeBundle(hre) {
  assert.equal(hre.network.name,'hardhat','candidate export must never use a live RPC');
  const { ethers } = hre;
  const baseline = loadBaseline();
  const a = Object.fromEntries(Object.entries({
    stakingProxy:'0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7',
    stakingImplementation:'0x1000000000000000000000000000000000000012',
    reader:'0x1000000000000000000000000000000000000002',
    timing:'0x1000000000000000000000000000000000000018',
    escrow:'0x1000000000000000000000000000000000000019',
    miningCredential:'0x5FbDB2315678afecb367f032d93F642f64180aa3',
    stakingCredential:'0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512',
    miningTimelock:'0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0',
    stakingTimelock:'0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9',
    governor:'0x5FC8d32690cc91D4c39d9d3abcBD16989F875707',
    policy:'0xDc64a140Aa3E981100a9becA4E685f962f0cF6C9',
    developers:'0xa513E6E4b8f2a923D98304ec87F64353C4D5C853',
    factory:'0x8A791620dd6260079BF849Dc5567aDC3F2FdC318',
    legacyEscrow:'0x8aCd85898458400f7Db866d53FCFF6f0D49741FF',
  }).map(([key,address])=>[key,ethers.getAddress(address)]));
  const genesis = 1758551400;
  const short = value => {
    const bytes=ethers.toUtf8Bytes(value);assert(bytes.length<32);
    const word=new Uint8Array(32);word.set(bytes);word[31]=bytes.length;
    return ethers.hexlify(word);
  };
  const domain = (name,address) => ({
    _cachedThis:address,_cachedChainId:1043,
    _cachedDomainSeparator:ethers.TypedDataEncoder.hashDomain({name,version:'1',chainId:1043,verifyingContract:address}),
    _hashedName:ethers.id(name),_hashedVersion:ethers.id('1'),_name:short(name),_version:short('1'),
  });
  const rows = [
    ['VersionedChainTallyReader',a.reader,{clockSource:a.stakingProxy}],
    ['VersionedGovernanceTiming',a.timing,{governor:a.governor,clock:a.reader,legacyDuration:60,legacyOrdinaryDelay:259200,legacyCriticalDelay:604800,
      ordinaryDelayNumerator:3,ordinaryDelayDenominator:7,criticalDelayEpochs:1,actionGraceEpochs:2}],
    ['VersionedTestnetStaking',a.stakingImplementation,{__self:a.stakingImplementation,timingModule:a.timing}],
    ...[['miningCredential','BlockDAG Mining Vote',a.reader],['stakingCredential','BlockDAG Staking Vote',ethers.ZeroAddress]].map(([key,name,mining])=>
      ['VersionedEpochCredential',a[key],{stakingSource:a.stakingProxy,miningSource:mining,_genesisTime:genesis,_epochDuration:60,...domain(name,a[key])}]),
    ...[a.miningTimelock,a.stakingTimelock].map(address=>['VersionedGovernanceTimelock',address,{governor:a.governor,timingModule:a.timing}]),
    ['VersionedEpochGovernor',a.governor,{miningCredential:a.miningCredential,stakingCredential:a.stakingCredential,
      miningTimelock:a.miningTimelock,stakingTimelock:a.stakingTimelock,policyModule:a.policy,timingModule:a.timing,
      epochSeconds:60,ordinaryDelay:26,criticalDelay:60,proposalBond:ethers.parseEther('0.001'),...domain('BlockDAG Epoch Governance',a.governor)}],
    ['VersionedProjectEscrow',a.escrow,{clock:a.reader,timing:a.timing}],
    ['VersionedProjectEscrowFactory',a.factory,{governance:a.stakingTimelock,registry:a.developers,clock:a.reader,timing:a.timing,
      implementation:a.escrow,legacyImplementation:a.legacyEscrow,legacyChallengeSeconds:2419200}],
  ];
  const runtimes=[];
  for (const [contract,address,values] of rows) {
    const bound=await bindRuntime(hre,contract,values);
    const old=baseline[address];
    const newStorage={};
    if (address===a.escrow) {
      newStorage['0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00']=ethers.toBeHex((1n<<64n)-1n,32);
    }
    runtimes.push({contract,address,mode:old?'replace':'create',previousCodeHash:old?ethers.keccak256(old.code):null,
      ...bound,codeHash:ethers.keccak256(bound.code),newStorage});
  }
  assert.equal(new Set(runtimes.map(row=>row.address)).size,10);
  assert(!baseline[a.timing] && !baseline[a.escrow],'new destinations overlap history');
  const replaced=new Set(runtimes.map(row=>row.address));
  const preservedCode=Object.entries(baseline).filter(([address])=>!replaced.has(address)).map(([address,account])=>({address,codeHash:ethers.keccak256(account.code)}));
  return {schemaVersion:1,chainId:1043,genesisTime:genesis,historicalAllocationSha256:BASELINE_SHA256,
    status:'uninstalled-candidate-not-qualified',installationAuthorized:false,activationSchedule:null,
    addresses:a,runtimes,preservedCode};
}
module.exports={buildVersionedRuntimeBundle,loadBaseline,bindRuntime};
