// Offline constructor/runtime program for an isolated chain-1404 rehearsal.
// No external RPC, live signer, production authority assignment or release seal.
const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');const crypto=require('node:crypto');
async function buildMainnetMigrationBundle(hre,input,options={}) {
 const {ethers,network,artifacts}=hre;
 assert.equal(network.name,'hardhat','in-process EVM required');
 assert.equal((await ethers.provider.getNetwork()).chainId,1404n,'isolated Mainnet-ID EVM required');
 assert.equal(input.transactionsSent,0);assert.equal(input.legacyGenesisTime,1770760980);
 assert.equal(ethers.keccak256(input.implementationCode),'0x2f0ad946ee06e0351e743f9d25fc4363377d4d7007fa3ee1fe6855e1f68d0040');
 const word=n=>ethers.toBeHex(BigInt(n),32),addr=a=>ethers.zeroPadValue(a,32),coder=ethers.AbiCoder.defaultAbiCoder();
 const set=(a,k,v)=>network.provider.send('hardhat_setStorageAt',[a,ethers.toQuantity(k),v]);
 const IMPL='0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
 const INIT='0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00';
 const GUARD='0x9b779b17422d0df92223018b32b4d1fa46e071723d6817e2486d003becc55f00';
 const roles=['reader','legacyImplementation','stakingV2Implementation','stakingV2','miningCredential','stakingCredential','governor','miningTimelock','stakingTimelock','timing','policy','hub','developers','bondVault','treasury','factory','escrow','miningRegistry','legacyEscrow'];
 const virtual=role=>ethers.getAddress(ethers.dataSlice(ethers.id('BDAG Mainnet native constructor '+role),12));
 const a=Object.fromEntries(roles.map(role=>[role,ethers.getCreateAddress({from:virtual(role),nonce:0})]));
 Object.assign(a,{legacy:ethers.getAddress(input.legacy),reader:'0x1000000000000000000000000000000000000002',stakingV2:'0x1000000000000000000000000000000000000020'});
 a.escrow=ethers.getCreateAddress({from:a.factory,nonce:1});
 const metadata={reviewOnly:true,installationAuthorized:false,addresses:a,constructors:{},abi:{},compiler:'0.8.24',evmVersion:'london',publicInputCheckpoint:input.baseline.hash};
 metadata.legacySourceProof=await require('./verify-mainnet-v1-source.cjs').verifyMainnetV1Source(hre,input);
 const rows=[];
 async function deploy(role,name,args=[]) {
  const sender=virtual(role);
  await network.provider.send('hardhat_impersonateAccount',[sender]);
  await network.provider.send('hardhat_setBalance',[sender,ethers.toQuantity(ethers.parseEther('100'))]);
  assert.equal(await ethers.provider.getTransactionCount(sender),0,'isolated exporter requires fresh virtual deployers');
  try {
   const signer=await ethers.getSigner(sender),factory=await ethers.getContractFactory(name,signer);
   const c=await factory.deploy(...args);await c.waitForDeployment();
   const receipt=await c.deploymentTransaction().wait();
   const trace=await network.provider.send('debug_traceTransaction',[receipt.hash,{disableMemory:true,disableStorage:true}]);
   const keys=[...new Set(trace.structLogs.filter(x=>x.op==='SSTORE'&&x.depth===1).map(x=>word('0x'+x.stack.at(-1))))];
   const storage={};for(const k of keys){const v=await ethers.provider.getStorage(c.target,k);if(BigInt(v)!==0n)storage[k]=v;}
   const code=await ethers.provider.getCode(c.target),target=ethers.getAddress(a[role]);
   if(c.target!==target){
    assert(['reader','stakingV2'].includes(role),'unexpected final address');
    await network.provider.send('hardhat_setCode',[target,code]);
    // These two records are initialized exclusively by the native kernel.
    for(const k of Object.keys(storage))delete storage[k];
   }
   const artifact=await artifacts.readArtifact(name);
   metadata.abi[role]=artifact.abi;
   metadata.constructors[role]={contract:name,virtualSender:sender,creationData:c.deploymentTransaction().data};
   const row={role,address:target,code,codeHash:ethers.keccak256(code),nonce:await ethers.provider.getTransactionCount(c.target),storage};
   rows.push(row);return {contract:await ethers.getContractAt(name,target),trace};
  } finally {await network.provider.send('hardhat_stopImpersonatingAccount',[sender]);}
 }
 // The original proxy/runtime and its actual historical epoch are read-only
 // public fixtures. No real balances or staker records are imported/reset.
 for(const [address,code]of [[input.legacy,input.legacyCode],[input.implementation,input.implementationCode],
  [input.blocklist,input.blocklistCode],[input.selective,input.selectiveCode],[input.governance,input.governanceCode]])await network.provider.send('hardhat_setCode',[address,code]);
 for(const [address,owner]of [[input.legacy,input.legacyOwner],[input.blocklist,input.blocklistOwner],[input.selective,input.selectiveOwner]])await set(address,0,addr(owner));
 for(const [slot,value]of [[IMPL,addr(input.implementation)],[8,word(input.legacyGenesisTime)],[1,word(70)],[2,word(30)],[INIT,word(1)],[GUARD,word(1)]])await set(input.legacy,slot,value);
 await deploy('reader','SeparatedChainTallyReader',[a.legacy]);
 await set(a.reader,8,word(1));const base=BigInt(ethers.keccak256(word(8)));
 for(const [i,value]of [0,input.legacyGenesisTime,604800].entries())await set(a.reader,base+BigInt(i),word(value));
 const now=(await ethers.provider.getBlock('latest')).timestamp,epoch=Math.floor((now-input.legacyGenesisTime)/604800);
 for(const [slot,value]of [[4,word(epoch)],[5,word(epoch+1)],[9,word(now)],[10,addr(a.stakingV2)],[11,word(epoch+1)]])await set(a.reader,slot,value);
 await deploy('legacyImplementation','MainnetLegacyStaking');
 await deploy('stakingV2Implementation','MainnetStakingV2',[a.legacy]);
 await deploy('stakingV2','UUPSProxy',[a.stakingV2Implementation,'0x']);
 for(const [slot,value]of [[0,addr(a.stakingTimelock)],[IMPL,addr(a.stakingV2Implementation)],[1,word(70)],[2,word(30)],
  [8,word(input.legacyGenesisTime)],[18,word(now)],[32,word(256)],[INIT,word(2)],[GUARD,word(1)]])await set(a.stakingV2,slot,value);
 await deploy('timing','VersionedGovernanceTiming',[a.governor,a.reader,604800,259200,604800,[3,7,1,2]]);
 await deploy('miningTimelock','VersionedGovernanceTimelock',[a.timing]);
 await deploy('stakingTimelock','VersionedGovernanceTimelock',[a.timing]);
 await deploy('policy','GovernancePolicyModule',[a.governor,a.miningTimelock]);
 await deploy('miningCredential','SeparatedEpochCredential',[a.legacy,a.reader,'BlockDAG Mining Vote','mVOTE']);
 await deploy('stakingCredential','SeparatedEpochCredential',[a.legacy,ethers.ZeroAddress,'BlockDAG Staking Vote','sVOTE']);
 const routes=[];
 async function route(target,name,signature,domain,tier,ownerDomain){const artifact=await artifacts.readArtifact(name);const iface=new ethers.Interface(artifact.abi);routes.push({target,selector:iface.getFunction(signature).selector,domain,tier,ownerDomain});}
 for(const f of ['setMinStake','setBondingPeriod','setUnbondingPeriod'])await route(a.stakingV2,'MainnetStakingV2',f,2,1,2);
 for(const f of ['transferOwnership','acceptOwnership','upgradeToAndCall','setStakingBlockedUntil','pause','unpause'])await route(a.stakingV2,'MainnetStakingV2',f,3,2,2);
 for(const f of ['upgradeToAndCall','unpause'])await route(a.legacy,'MainnetLegacyStaking',f,3,2,2);
 for(const f of ['setBlocked','setBlockedBatch','transferOwnership'])await route(input.blocklist,'BlocklistGovernance',f,3,2,1);
 for(const f of ['setRestricted','setRestrictedBatch','setAllowedTo','setAllowedToBatch','transferOwnership'])await route(input.selective,'SelectiveBlocklist',f,3,2,1);
 await route(input.governance,'GovernanceHubV2','announceRelease',3,2,1);
 await route(input.governance,'GovernanceHubV2','upgradeToAndCall',3,2,1);
 await route(a.policy,'GovernancePolicyModule','schedulePolicy',3,2,1);
 await route(a.miningRegistry,'MiningPolicyRegistry','setPolicy',1,1,1);
 await route(a.developers,'ApprovedDevRegistry','setApproval',3,2,2);
 for(const f of ['resolveDelivery','resolveUnselected'])await route(a.bondVault,'BidBondVault',f,3,2,2);
 await deploy('governor','VersionedEpochGovernor',[a.miningCredential,a.stakingCredential,a.miningTimelock,a.stakingTimelock,a.policy,
  {epochSeconds:604800,ordinaryDelay:259200,criticalDelay:604800,proposalBond:ethers.parseEther('0.001')},routes,a.timing]);
 await deploy('miningRegistry','MiningPolicyRegistry',[a.miningTimelock]);
 const people=await ethers.getSigners();const reviewers=options.reviewers||people.slice(50,53).map(x=>x.address);
 assert.equal(reviewers.length,3);assert.equal(new Set(reviewers.map(x=>x.toLowerCase())).size,3);
 metadata.reviewerAssignmentsAreFixtures=!options.reviewers;
 await deploy('developers','ApprovedDevRegistry',[a.stakingTimelock,reviewers]);
 await deploy('treasury','CommunityTreasury',[a.stakingTimelock,a.governor,a.stakingV2]);
 await deploy('legacyEscrow','ProjectEscrow');
 const factory=await deploy('factory','VersionedProjectEscrowFactory',[a.stakingTimelock,a.developers,a.reader,a.timing,a.legacyEscrow,2419200]);
 const childCode=await ethers.provider.getCode(a.escrow),childStorage={};
 for(const key of new Set(factory.trace.structLogs.filter(x=>x.op==='SSTORE'&&x.depth===2).map(x=>word('0x'+x.stack.at(-1)))))childStorage[key]=await ethers.provider.getStorage(a.escrow,key);
 rows.push({role:'escrow',address:a.escrow,code:childCode,codeHash:ethers.keccak256(childCode),nonce:1,storage:childStorage});
 metadata.abi.escrow=(await artifacts.readArtifact('VersionedProjectEscrow')).abi;
 await deploy('bondVault','BidBondVault',[a.stakingTimelock,a.developers,a.treasury]);
 await deploy('hub','GovernanceHubV2',[a.miningTimelock,a.governor]);
 const creation=fs.readFileSync(path.join(__dirname,'../../test/phase1/fixtures/GovernanceProxyLegacy.creation.hex'),'utf8').trim();
 assert.equal(crypto.createHash('sha256').update(fs.readFileSync(path.join(__dirname,'../../test/phase1/fixtures/GovernanceProxyLegacy.creation.hex'))).digest('hex'),'73f2f6fefbad8a6da48cbe962a1a6f9a58a6d74cb5f355ea000db2bec8643900');
 const bytes=ethers.getBytes(creation),split=bytes.findIndex((v,i)=>v===0xf3&&bytes[i+1]===0xfe);assert(split>0);
 const runtime=ethers.hexlify(bytes.slice(split+2));assert.equal(ethers.keccak256(runtime),'0x75498dc0fa24902bbd116019b098f2e3c8474c5c1f26213d56594a6457e3a7ed');
 const bundle={version:1,chainId:1404,legacyGenesisTime:input.legacyGenesisTime,genesisHash:input.genesis.hash,genesisStateRoot:input.genesis.stateRoot,
  baselineNumber:Number(BigInt(input.baseline.number)),baselineHash:input.baseline.hash,baselineStateRoot:input.baseline.stateRoot,
  legacyProxyCodeHash:ethers.keccak256(input.legacyCode),legacyImplementation:input.implementation,legacyImplementationCodeHash:ethers.keccak256(input.implementationCode),legacyOwner:input.legacyOwner,
  blocklistCodeHash:ethers.keccak256(input.blocklistCode),blocklistOwner:input.blocklistOwner,selectiveCodeHash:ethers.keccak256(input.selectiveCode),selectiveOwner:input.selectiveOwner,
  governanceProxyCodeHash:ethers.keccak256(input.governanceCode),governanceProxyRuntime:runtime,accounts:roles.map(role=>{const row=rows.find(x=>x.role===role);assert(row,'missing '+role);return row;})};
 // Retire authority in the isolated runtime to expose binding checks. Native
 // writes (not an old owner signature) do this atomically on the target chain.
 await set(a.legacy,0,addr(a.stakingTimelock));await set(a.legacy,IMPL,addr(a.legacyImplementation));
 return {bundle,addresses:a,metadata,genesisHeader:input.genesis,baselineHeader:input.baseline};
}
module.exports={buildMainnetMigrationBundle};
