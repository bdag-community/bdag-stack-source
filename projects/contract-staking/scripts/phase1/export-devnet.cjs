// Build an isolated, reproducible genesis fixture, never a live deployment.
// Runtime and constructor storage come from the London EVM. No private keys are exported.
// BDAG_FIXTURE_DATE=2026-09-01T00:00:00Z BDAG_BUILD_LABEL=export npx hardhat run --config hardhat.phase1.config.cjs scripts/phase1/export-devnet.cjs
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const hre = require('hardhat');
const { ethers, network } = hre;
const ROOT = path.resolve(__dirname, '../..');
const BDAG = path.resolve(ROOT, '../..');
const REVISION = Number(process.env.BDAG_FIXTURE_REVISION || '1');
assert([1,2,3,4,5,6,7].includes(REVISION), 'Unsupported fixture revision');
const TESTNET_BOOTSTRAP = REVISION >= 6;
const TESTNET_RELEASE = REVISION === 7;
const OUT = path.join(ROOT, REVISION === 1 ? 'out/governance-devnet' : REVISION === 2 ? 'out/governance-devnet-v2' : REVISION === 3 ? 'out/governance-devnet-v3' : REVISION === 4 ? 'out/governance-devnet-v4' : REVISION === 5 ? 'out/governance-devnet-v5' : TESTNET_RELEASE ? 'out/governance-testnet-release-v1' : 'out/governance-testnet-bootstrap-v1');
const PROXY = '0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7';
const V1 = '0x1000000000000000000000000000000000000011';
const V2 = '0x1000000000000000000000000000000000000012';
const READER = '0x1000000000000000000000000000000000000002';
const HUB = '0x1000000000000000000000000000000000000016';
const GOV_PROXY = '0x1000000000000000000000000000000000000005';
const BLOCKLIST = '0xe628505d5cB6F5F4f9a25Cd5c80Caa198cc645a0';
const SELECTIVE = '0xc0a42A43A05e39C3a4165CA98440D516959f41D9';
const OLD_OWNER = '0x4f1536fc181c541f3ef766d227373f55d03ce0ba';
const OLD_BLOCKLIST_OWNER = '0xe9ce34eda6a4554420ca871d7057edaec32e9e9c';
const OLD_ADMIN = '0x3655a92f2db839a9ef30216c8843b310c65ce298';
const SLOT_IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const SLOT_ADMIN = '0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103';
const SLOT_INIT = '0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00';
const SLOT_REENTRANCY = '0x9b779b17422d0df92223018b32b4d1fa46e071723d6817e2486d003becc55f00';
const GENESIS = 1758551400;
const h32 = n => ethers.toBeHex(BigInt(n), 32);
const a32 = a => ethers.zeroPadValue(a, 32);
const coder = ethers.AbiCoder.defaultAbiCoder();
const mapping = (a, slot) => ethers.keccak256(coder.encode(['address','uint256'], [a,slot]));
const keys = new Map(), contracts = {}, artifacts = {};
const noteSlot = (address, slot) => {
  address = ethers.getAddress(address); if (!keys.has(address)) keys.set(address,new Set());
  keys.get(address).add(h32(slot));
};
async function setSlot(address, slot, value) {
  noteSlot(address,slot);
  await network.provider.send('hardhat_setStorageAt',[address,h32(slot),typeof value === 'string' ? value : h32(value)]);
}
async function record(tx, address, depth) {
  const receipt = await (await tx).wait();
  const trace = await network.provider.send('debug_traceTransaction',[receipt.hash,{disableMemory:true,disableStorage:true}]);
  for (const step of trace.structLogs) if (step.op === 'SSTORE' && (depth === undefined || step.depth === depth)) {
    noteSlot(address,'0x'+step.stack.at(-1));
  }
  return receipt;
}
async function deploy(name,args=[]) {
  const contract = await ethers.deployContract(name,args);
  await record(contract.deploymentTransaction(),contract.target,1);
  contracts[name] = contract;
  return contract;
}
async function output(name) {
  const artifact = await hre.artifacts.readArtifact(name);
  const info = await hre.artifacts.getBuildInfo(`${artifact.sourceName}:${artifact.contractName}`);
  assert.equal(info.input.settings.evmVersion,'london');
  artifacts[name] = artifact;
  return { artifact, compiled:info.output.contracts[artifact.sourceName][name], info };
}
async function boundRuntime(name, values) {
  const { artifact,compiled,info } = await output(name);
  const declarations = new Map();
  const visit = node => {
    if (!node || typeof node !== 'object') return;
    if (node.nodeType === 'VariableDeclaration' && node.mutability === 'immutable') declarations.set(String(node.id),node.name);
    for (const item of Object.values(node)) if (item && typeof item === 'object') Array.isArray(item) ? item.forEach(visit) : visit(item);
  };
  Object.values(info.output.sources).forEach(source=>visit(source.ast));
  const bytes=ethers.getBytes(artifact.deployedBytecode), bound={};
  for(const [id,refs] of Object.entries(compiled.evm.deployedBytecode.immutableReferences || {})) {
    const field=declarations.get(id); assert(field && values[field] !== undefined,`Missing immutable ${name}.${field || id}`);
    const value=ethers.getBytes(h32(values[field])); bound[field]=h32(values[field]);
    for(const {start,length} of refs) { assert.equal(length,32); bytes.set(value,start); }
  }
  return { runtime:ethers.hexlify(bytes),bound };
}
async function main() {
  assert.equal(network.name,'hardhat'); assert.equal((await ethers.provider.getNetwork()).chainId,TESTNET_BOOTSTRAP ? 1043n : 8008n);
  assert(process.env.BDAG_FIXTURE_DATE,'Set a fixed BDAG_FIXTURE_DATE for reproducible fixture construction');
  fs.mkdirSync(OUT,{recursive:true});
  const people=await ethers.getSigners(),pools=TESTNET_BOOTSTRAP ? [] : people.slice(40,50),stakers=TESTNET_BOOTSTRAP ? [] : people.slice(1,26);
  const includeFunding=REVISION >= 3;
  const v1=await boundRuntime('StakingContract',{__self:V1});
  const stakingV2Name=TESTNET_RELEASE ? 'StakingContractV2Testnet' : TESTNET_BOOTSTRAP ? 'StakingContractV2' : 'StakingContractV2Devnet';
  const v2=await boundRuntime(stakingV2Name,{__self:V2});
  const proxyArtifact=(await output('UUPSProxy')).artifact;
  await network.provider.send('hardhat_setCode',[V1,v1.runtime]);
  await network.provider.send('hardhat_setCode',[V2,v2.runtime]);
  await network.provider.send('hardhat_setCode',[PROXY,proxyArtifact.deployedBytecode]);
  for(const [slot,value] of [[0,a32(OLD_OWNER)],[1,h32(70)],[2,h32(30)],[8,h32(GENESIS)],
    [SLOT_IMPL,a32(TESTNET_BOOTSTRAP ? V2 : V1)],[SLOT_INIT,h32(1)],[SLOT_REENTRANCY,h32(1)],...(TESTNET_BOOTSTRAP ? [[32,h32(0x100)]] : [[mapping(OLD_OWNER,16),h32(1)]])]) await setSlot(PROXY,slot,value);
  const staking=await ethers.getContractAt('StakingContract',PROXY),positions=[];
  for(let i=0;i<stakers.length;i++) {
    const who=stakers[i],pool=pools[i%pools.length],amount=ethers.parseEther('10');
    await record(staking.connect(who).stake(who.address,amount,pool.address,{value:amount}),PROXY);
    positions.push({staker:who.address,pool:pool.address,principal:amount.toString(),firstDeposit:(await staking.userFirstDepositTimestamp(who.address)).toString()});
  }
  // Fixture construction supplies the planned clock to constructors. Export restores V1.
  // This is genesis state assembly; live mainnet deployment must use tested V1 clock compatibility.
  await setSlot(PROXY,SLOT_IMPL,a32(V2));
  const readerRuntime=await boundRuntime('ChainTallyReader',{clockSource:PROXY});
  await network.provider.send('hardhat_setCode',[READER,readerRuntime.runtime]);
  const mining=await deploy('EpochCredential',[PROXY,READER,'BlockDAG Mining Vote','mVOTE']);
  // Two instances: retain both under explicit names in the address manifest.
  delete contracts.EpochCredential; contracts.MiningCredential=mining;
  const stakerCredential=await deploy('EpochCredential',[PROXY,ethers.ZeroAddress,'BlockDAG Staking Vote','sVOTE']);
  delete contracts.EpochCredential; contracts.StakingCredential=stakerCredential;
  for(const position of positions) await record(stakerCredential.registerPools(position.staker,[position.pool]),stakerCredential.target);
  const nonce=await ethers.provider.getTransactionCount(people[0].address);
  const predictedGovernor=ethers.getCreateAddress({from:people[0].address,nonce:nonce+3});
  const predictedPolicy=ethers.getCreateAddress({from:people[0].address,nonce:nonce+2});
  const predictedRegistry=ethers.getCreateAddress({from:people[0].address,nonce:nonce+4});
  const predictedDeveloperRegistry=ethers.getCreateAddress({from:people[0].address,nonce:nonce+5});
  const predictedTreasury=ethers.getCreateAddress({from:people[0].address,nonce:nonce+6});
  const predictedFactory=ethers.getCreateAddress({from:people[0].address,nonce:nonce+7});
  const predictedBondVault=ethers.getCreateAddress({from:people[0].address,nonce:nonce+8});
  const ordinaryDelay=TESTNET_BOOTSTRAP ? 3*24*60*60 : 5;
  const criticalDelay=TESTNET_BOOTSTRAP ? 7*24*60*60 : 15;
  const epochSeconds=TESTNET_BOOTSTRAP && !TESTNET_RELEASE ? 7*24*60*60 : 60;
  const miningTL=await deploy('GovernanceTimelock',[ordinaryDelay,predictedGovernor]);
  delete contracts.GovernanceTimelock; contracts.MiningTimelock=miningTL;
  const stakingTL=await deploy('GovernanceTimelock',[ordinaryDelay,predictedGovernor]);
  delete contracts.GovernanceTimelock; contracts.StakingTimelock=stakingTL;
  const policy=await deploy('GovernancePolicyModule',[predictedGovernor,miningTL.target]);
  assert.equal(policy.target,predictedPolicy);
  const v2iface=(await ethers.getContractFactory(stakingV2Name)).interface;
  const blockIface=(await ethers.getContractFactory('BlocklistGovernance')).interface;
  const selectiveIface=(await ethers.getContractFactory('SelectiveBlocklist')).interface;
  const hubIface=(await ethers.getContractFactory('GovernanceHubV2')).interface;
  const registryIface=(await ethers.getContractFactory('MiningPolicyRegistry')).interface;
  const developerRegistryIface=includeFunding ? (await ethers.getContractFactory('ApprovedDevRegistry')).interface : undefined;
  const treasuryIface=includeFunding ? (await ethers.getContractFactory('CommunityTreasury')).interface : undefined;
  const factoryIface=includeFunding ? (await ethers.getContractFactory('ProjectEscrowFactory')).interface : undefined;
  const bondIface=includeFunding ? (await ethers.getContractFactory('BidBondVault')).interface : undefined;
  const policyIface=(await ethers.getContractFactory('GovernancePolicyModule')).interface;
  const routes=[];
  const route=(target,iface,signature,domain,tier,ownerDomain)=>routes.push({target,selector:iface.getFunction(signature).selector,domain,tier,ownerDomain});
  for(const signature of ['setMinStake','setBondingPeriod','setUnbondingPeriod']) route(PROXY,v2iface,signature,2,1,2);
  for(const signature of ['transferOwnership','acceptOwnership','upgradeToAndCall']) route(PROXY,v2iface,signature,3,2,2);
  for(const signature of ['setBlocked','setBlockedBatch','transferOwnership']) route(BLOCKLIST,blockIface,signature,3,2,1);
  for(const signature of ['setRestricted','setRestrictedBatch','setAllowedTo','setAllowedToBatch','transferOwnership']) route(SELECTIVE,selectiveIface,signature,3,2,1);
  route(GOV_PROXY,hubIface,'announceRelease',3,2,1);
  if(hubIface.hasFunction('upgradeToAndCall')) route(GOV_PROXY,hubIface,'upgradeToAndCall',3,2,1);
  route(policy.target,policyIface,'schedulePolicy',3,2,1);
  route(predictedRegistry,registryIface,'setPolicy',1,1,1);
  if (includeFunding) for (const [target,iface,signature] of [
      [predictedDeveloperRegistry,developerRegistryIface,'setApproval'],
      [predictedTreasury,treasuryIface,'spend'],
      [predictedFactory,factoryIface,'createProject'],
      [predictedFactory,factoryIface,'vetoProject'],
      [predictedBondVault,bondIface,'resolveDelivery'],
      [predictedBondVault,bondIface,'resolveUnselected']
    ]) route(target,iface,signature,3,2,2);
  const governor=await deploy('EpochGovernor',[mining.target,stakerCredential.target,miningTL.target,stakingTL.target,
    policy.target,{epochSeconds,ordinaryDelay,criticalDelay,proposalBond:ethers.parseEther('0.001')},routes]);
  assert.equal(governor.target,predictedGovernor);
  const registry=await deploy('MiningPolicyRegistry',[miningTL.target]);
  assert.equal(registry.target,predictedRegistry);
  let council,developerRegistry,treasury,factory,implementation,bondVault;
  if (includeFunding) {
    council=[people[50].address,people[51].address,people[52].address];
    developerRegistry=await deploy('ApprovedDevRegistry',[stakingTL.target,council]);
    assert.equal(developerRegistry.target,predictedDeveloperRegistry);
    treasury=await deploy('CommunityTreasury',[stakingTL.target,governor.target,PROXY]);
    assert.equal(treasury.target,predictedTreasury);
    factory=await deploy('ProjectEscrowFactory',[stakingTL.target,developerRegistry.target,28*24*60*60]);
    assert.equal(factory.target,predictedFactory);
    implementation=await factory.implementation();
    await record(factory.deploymentTransaction(),implementation,2);
    contracts.ProjectEscrowImplementation=await ethers.getContractAt('ProjectEscrow',implementation);
    bondVault=await deploy('BidBondVault',[stakingTL.target,developerRegistry.target,treasury.target]);
    assert.equal(bondVault.target,predictedBondVault);
  }
  const hub=await boundRuntime('GovernanceHubV2',{executingTimelock:miningTL.target,governor:governor.target,__self:HUB});
  await setSlot(PROXY,SLOT_IMPL,a32(TESTNET_BOOTSTRAP ? V2 : V1));
	if (TESTNET_BOOTSTRAP) {
	  await setSlot(PROXY,0,a32(stakingTL.target));
	  await setSlot(V2,SLOT_INIT,h32((1n << 64n) - 1n));
	  await network.provider.send('hardhat_setCode',[HUB,hub.runtime]);
	}
  // Existing contracts and restrictions are preserved as explicit public fixture state.
  for(const [address,name] of [[BLOCKLIST,'BlocklistGovernance'],[SELECTIVE,'SelectiveBlocklist']]) {
    const artifact=(await output(name)).artifact;
    await network.provider.send('hardhat_setCode',[address,artifact.deployedBytecode]);
    await setSlot(address,0,a32(TESTNET_BOOTSTRAP ? miningTL.target : OLD_BLOCKLIST_OWNER));
    if (!TESTNET_BOOTSTRAP) for(const old of [OLD_OWNER,OLD_BLOCKLIST_OWNER,OLD_ADMIN]) await setSlot(address,mapping(old,1),h32(1));
  }
  const genesis=JSON.parse(fs.readFileSync(path.join(BDAG,'blockdag-corechain/bevm/bdag/genesis.json')));
  const mainnetAlloc=genesis.find(row=>row.network==='mainnet').data.genesis.alloc;
  const legacy=Object.entries(mainnetAlloc).find(([address])=>address.toLowerCase()===GOV_PROXY.toLowerCase())[1].code;
  const legacyBytes=ethers.getBytes(legacy),split=legacyBytes.findIndex((byte,i)=>byte===0xf3 && legacyBytes[i+1]===0xfe);
  assert(split>0); const legacyRuntime=ethers.hexlify(legacyBytes.slice(split+2));
  await network.provider.send('hardhat_setCode',[GOV_PROXY,TESTNET_BOOTSTRAP ? legacyRuntime : legacy]);
  await setSlot(GOV_PROXY,SLOT_IMPL,a32(TESTNET_BOOTSTRAP ? HUB : '0x1000000000000000000000000000000000000015'));
  await setSlot(GOV_PROXY,SLOT_ADMIN,a32(TESTNET_BOOTSTRAP ? miningTL.target : OLD_ADMIN));
  await setSlot(GOV_PROXY,0,a32(TESTNET_BOOTSTRAP ? miningTL.target : OLD_ADMIN));
  const alloc={};
  const addresses=new Set([PROXY,TESTNET_BOOTSTRAP ? V2 : V1,BLOCKLIST,SELECTIVE,GOV_PROXY,...(TESTNET_BOOTSTRAP ? [READER,HUB] : []),...Object.values(contracts).map(c=>c.target)]);
  for(const address of addresses) {
    const storage={};
    for(const slot of keys.get(ethers.getAddress(address)) || []) {
      const value=await ethers.provider.getStorage(address,slot); if(BigInt(value)!==0n) storage[slot]=value;
    }
    const balance=await ethers.provider.getBalance(address),code=await ethers.provider.getCode(address);
    assert.notEqual(code,'0x');
    const nonce=REVISION >= 4 ? await ethers.provider.getTransactionCount(address) : 1;
    alloc[address]={balance:balance.toString(),nonce,code,storage};
  }
  // A real zero-value transaction from an otherwise unused fixture account
  // makes revision2's genesis identity distinct without changing user positions.
  let revisionMarker;
  if (REVISION >= 2) {
    const receipt = await (await people[60].sendTransaction({to:people[60].address,value:0n})).wait();
    revisionMarker = {account:people[60].address,nonce:await ethers.provider.getTransactionCount(people[60].address),fixtureTransaction:receipt.hash};
    assert.equal(revisionMarker.nonce,1);
  }
  for(const who of people) alloc[who.address]={balance:ethers.parseEther('1000000').toString(),nonce:await ethers.provider.getTransactionCount(who.address)};
  assert.equal(alloc[PROXY].balance,TESTNET_BOOTSTRAP ? '0' : ethers.parseEther('250').toString());
  assert.equal(alloc[PROXY].storage[SLOT_IMPL],a32(TESTNET_BOOTSTRAP ? V2 : V1));
  const poolTotals=new Map();
  for(const position of positions) {
    const {staker,pool,principal,firstDeposit}=position;
    const nested=base=>mapping(pool,mapping(staker,base));
    assert.equal(alloc[PROXY].storage[nested(14)],h32(principal),'exported principal must match actual V1 stake');
    assert.equal(alloc[PROXY].storage[nested(3)],h32(principal),'no-reward fixture shares must equal principal');
    assert.equal(alloc[PROXY].storage[mapping(staker,11)],h32(firstDeposit));
    poolTotals.set(pool,(poolTotals.get(pool)||0n)+BigInt(principal));
  }
  for(const [pool,total] of poolTotals) for(const base of [5,6,15]) assert.equal(alloc[PROXY].storage[mapping(pool,base)],h32(total));
  if (TESTNET_BOOTSTRAP) {
    assert(alloc[V2] && alloc[HUB] && alloc[READER]);
  } else {
    assert(!alloc[V2] && !alloc[HUB] && !alloc[READER]);
  }
  const bound=[['StakingContractV2Governance',stakingV2Name,V2,[],v2],
    ['ChainTallyReader','ChainTallyReader',READER,[PROXY],readerRuntime],
    ['GovernanceHub','GovernanceHubV2',HUB,[miningTL.target,governor.target],hub]];
  const forkArtifacts={};
  for(const [file,name,address,args,result] of bound) {
    const factory=await ethers.getContractFactory(name),creation=(await factory.getDeployTransaction(...args)).data;
    fs.writeFileSync(path.join(OUT,file+'.creation.hex'),creation+'\n');
    fs.writeFileSync(path.join(OUT,file+'.runtime.hex'),result.runtime+'\n');
    forkArtifacts[file]={name,address,creationHash:ethers.keccak256(creation),runtimeHash:ethers.keccak256(result.runtime),runtimeBytes:ethers.getBytes(result.runtime).length,immutables:result.bound};
  }
  fs.writeFileSync(path.join(OUT,'GovernanceProxyLegacy.creation.hex'),legacy+'\n');
  fs.writeFileSync(path.join(OUT,'GovernanceProxyLegacy.runtime.hex'),legacyRuntime+'\n');
  const authorities={};
  for(const name of ['MiningTimelock','StakingTimelock','EpochGovernor']) {
    const address=contracts[name].target; authorities[name]={address,codeHash:ethers.keccak256(alloc[address].code)};
  }
  const json=(name,value)=>fs.writeFileSync(path.join(OUT,name),JSON.stringify(value,null,2)+'\n');
  json('genesis-alloc.json',alloc);
  if (TESTNET_BOOTSTRAP) json('bootstrap-alloc.json',Object.fromEntries([...addresses].sort((a,b)=>a.toLowerCase().localeCompare(b.toLowerCase())).map(address=>[ethers.getAddress(address),alloc[ethers.getAddress(address)]])));
  for(const name of ['EpochCredential','EpochGovernor','GovernanceTimelock','GovernancePolicyModule','GovernanceHubV2','MiningPolicyRegistry',stakingV2Name,'ChainTallyReader','BlocklistGovernance','SelectiveBlocklist',...(includeFunding ? ['ApprovedDevRegistry','CommunityTreasury','ProjectEscrowFactory','ProjectEscrow','BidBondVault'] : [])]) json(name+'.abi.json',(await hre.artifacts.readArtifact(name)).abi);
  const files={}; for(const file of fs.readdirSync(OUT).filter(f=>f!=='manifest.json')) files[file]=crypto.createHash('sha256').update(fs.readFileSync(path.join(OUT,file))).digest('hex');
  if (REVISION >= 4) {
    assert.equal(alloc[factory.target].nonce,2,'Factory constructor must consume nonce1 for its implementation');
    assert.equal(alloc[implementation].nonce,1,'Nested implementation must retain its deployment nonce');
  }
  let funding;
  if (includeFunding) {
    funding={council,contracts:Object.fromEntries(['ApprovedDevRegistry','CommunityTreasury','ProjectEscrowFactory','BidBondVault'].map(name=>[name,{address:contracts[name].target,codeHash:ethers.keccak256(alloc[contracts[name].target].code)}])),nestedProjectEscrowImplementation:{address:implementation,codeHash:ethers.keccak256(alloc[implementation].code),storage:alloc[implementation].storage,createdBy:factory.target,traceDepth:2,initializerDisabled:true},veto:{routeTarget:factory.target,selector:factoryIface.getFunction('vetoProject').selector,authority:'CORE via StakingTimelock'}};
    if (REVISION >= 4) Object.assign(funding,{deploymentNonces:{factory:alloc[factory.target].nonce,firstClone:ethers.getCreateAddress({from:factory.target,nonce:alloc[factory.target].nonce})},nestedProjectEscrowImplementation:{...funding.nestedProjectEscrowImplementation,nonce:alloc[implementation].nonce}});
  }
  const manifest={version:TESTNET_BOOTSTRAP ? 3 : 2,network:TESTNET_BOOTSTRAP ? 'bdag-community-testnet-bootstrap' : 'isolated-private-devnet-only',chainId:TESTNET_BOOTSTRAP ? 1043 : 8008,fixtureDate:process.env.BDAG_FIXTURE_DATE,genesisTime:GENESIS,epochSeconds,
    authority:authorities,contracts:Object.fromEntries(Object.entries(contracts).map(([n,c])=>[n,c.target])),stakingProxy:PROXY,reader:READER,hubProxy:GOV_PROXY,hubImplementation:HUB,blocklist:BLOCKLIST,selectiveBlocklist:SELECTIVE,
    oldAddresses:{owner:OLD_OWNER,blocklistOwner:OLD_BLOCKLIST_OWNER,admin:OLD_ADMIN},stakers:positions,pools:pools.map(p=>p.address),routes,
    forkArtifacts,legacyProxy:{creationHash:ethers.keccak256(legacy),runtimeHash:ethers.keccak256(legacyRuntime)},files,
    limitations:TESTNET_BOOTSTRAP
      ? ['Testnet-only atomic bootstrap for empty-code targets; preserve native balances, shares and historical accounting. Unexpected populated target state must fail closed.','Constructor state is assembled and independently re-imported on a local London EVM; this is not a mainnet deployment ceremony.','Known local test wallets only; no key receives emergency authority.', TESTNET_RELEASE ? 'Post-bootstrap voting and new user staking use 60 seconds. Native operator locks retain the RC2 block-based financial clock. Production governance delays remain unchanged.' : 'Production seven-day reward epochs and governance delays are retained for normal-duration testnet acceptance.']
      : ['Genesis constructor state is assembled on a local London EVM; this is not an on-chain production deployment ceremony.','Known local test wallets only; no key receives emergency authority.','Pre-fork V1 uses its original clock; V2/devnet engine and frozen credential clock use 60 seconds.','All retired address restrictions remain set; no retired key is used.']};
  if (TESTNET_BOOTSTRAP) Object.assign(manifest,{bootstrap:{mode:'all-targets-empty',allocFile:'bootstrap-alloc.json',allocSha256:files['bootstrap-alloc.json']},activation:TESTNET_RELEASE ? {status:'unscheduled-not-distributable',windowLayers:2000,thresholdLayers:1500,delayLayers:2000,minimumNoticeSeconds:24*60*60} : {signalStartLayer:1000,windowLayers:2000,thresholdLayers:1500,delayLayers:2000,earliestLayer:4999,minimumNoticeSeconds:24*60*60}});
  if (REVISION >= 2) Object.assign(manifest,{fixtureRevision:REVISION,revisionMarker});
  if (funding) Object.assign(manifest,{funding});
  json('manifest.json',manifest);
  console.log(JSON.stringify({out:OUT,authorities,forkArtifacts,genesisAccounts:Object.keys(alloc).length,positions:positions.length},null,2));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
