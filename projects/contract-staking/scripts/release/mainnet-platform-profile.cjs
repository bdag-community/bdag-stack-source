// Produces public REVIEW profiles only; no secret, network or deployment calls.
const assert=require('node:assert/strict');const crypto=require('node:crypto');
const input=require('../../test-mainnet/fixtures/canonical-input.json');
function platformProfile(result){
 const b=result.bundle,a=result.addresses;assert.equal(b.chainId,1404);assert.equal(result.metadata.reviewOnly,true);
 const row=role=>{const r=b.accounts.find(r=>r.role===role);assert(r,'missing runtime '+role);return r;};
 const contracts={governor:a.governor,miningCredential:a.miningCredential,stakingCredential:a.stakingCredential,policyModule:a.policy,chainTallyReader:a.reader,
  blocklist:input.blocklist,selectiveBlocklist:input.selective,approvedDevRegistry:a.developers,bidBondVault:a.bondVault,escrowFactory:a.factory,communityTreasury:a.treasury};
 const runtimePins=Object.fromEntries(b.accounts.map(r=>[r.role,{address:r.address,codeHash:r.codeHash}]));
 runtimePins.legacyProxy={address:a.legacy,codeHash:b.legacyProxyCodeHash};
 return {schema:'bdag.mainnet.platform-profile.v1',status:'review',installationAuthorized:false,chainId:1404,
  genesisHash:b.genesisHash,genesisStateRoot:b.genesisStateRoot,baseline:{number:b.baselineNumber,hash:b.baselineHash,stateRoot:b.baselineStateRoot},
  nativeMigrationBundleSha256:crypto.createHash('sha256').update(JSON.stringify(b,null,2)+'\n').digest('hex'),
  epochSeconds:604800,stakingProxy:a.stakingV2,legacyStakingProxy:a.legacy,contracts,runtimePins,
  epochClock:{interfaceVersion:'3',reader:a.reader,codeHash:row('reader').codeHash,stakingSource:a.legacy.toLowerCase(),activeStakingSource:a.stakingV2},
  reviewers:result.metadata.reviewers,authorityInputSha256:result.metadata.authorityInputSha256,fundingEnabled:false,stakers:[],pools:[],
  pending:['Final authority/profile sealing and canonical native/EVM replay qualification','Approved production API, database and website configuration','Supported-wallet acceptance against final deployed runtimes after separately approved activation']};
}
function environmentTemplates(profile){
 const raw=JSON.stringify(profile),digest=crypto.createHash('sha256').update(raw).digest('hex'),c=profile.contracts;
 const development={NEXT_PUBLIC_BDAG_DATA_MODE:'live',NEXT_PUBLIC_BDAG_CHAIN_ID:'1404',NEXT_PUBLIC_BDAG_RPC_URL:'https://rpc.capedag.com/',NEXT_PUBLIC_BDAG_GENESIS_HASH:profile.genesisHash,
  NEXT_PUBLIC_BDAG_MANIFEST_HASH:profile.nativeMigrationBundleSha256,NEXT_PUBLIC_BDAG_MAINNET_PROFILE_JSON:raw,NEXT_PUBLIC_BDAG_MAINNET_PROFILE_SHA256:digest,
  NEXT_PUBLIC_BDAG_EPOCH_CLOCK_INTERFACE:'3',NEXT_PUBLIC_BDAG_EPOCH_READER_CODE_HASH:profile.epochClock.codeHash,NEXT_PUBLIC_BDAG_TEST_FUNDING:'0'};
 for(const [field,name]of Object.entries({governor:'GOVERNOR',miningCredential:'MINING_CREDENTIAL',stakingCredential:'STAKING_CREDENTIAL',policyModule:'POLICY_MODULE',chainTallyReader:'CHAIN_TALLY_READER',blocklist:'BLOCKLIST',selectiveBlocklist:'SELECTIVE_BLOCKLIST',approvedDevRegistry:'DEV_REGISTRY',bidBondVault:'BID_BOND_VAULT',escrowFactory:'ESCROW_FACTORY',communityTreasury:'TREASURY'}))development['NEXT_PUBLIC_BDAG_'+name+'_ADDRESS']=c[field];
 const staking={NEXT_PUBLIC_CHAIN_NAME:'BlockDAG Mainnet',NEXT_PUBLIC_NETWORK_LABEL:'BlockDAG Mainnet',NEXT_PUBLIC_METAMASK_CHAIN_ID:'1404',NEXT_PUBLIC_RPC_URL:'https://rpc.capedag.com/',NEXT_PUBLIC_EXPECTED_GENESIS_HASH:profile.genesisHash,
  NEXT_PUBLIC_CURRNCY_NAME:'BDAG',NEXT_PUBLIC_CURRNCY_SYMBOL:'BDAG',NEXT_PUBLIC_STAKING_CONTRACT_ADDRESS:profile.stakingProxy,NEXT_PUBLIC_STAKING_V1_ADDRESS:profile.legacyStakingProxy,NEXT_PUBLIC_STAKING_V2_ADDRESS:profile.stakingProxy,
  NEXT_PUBLIC_MINING_CREDENTIAL_ADDRESS:c.miningCredential,NEXT_PUBLIC_STAKING_CREDENTIAL_ADDRESS:c.stakingCredential,NEXT_PUBLIC_READER_ADDRESS:profile.epochClock.reader,
  NEXT_PUBLIC_EPOCH_CLOCK_INTERFACE:'3',NEXT_PUBLIC_EPOCH_READER_CODE_HASH:profile.epochClock.codeHash,NEXT_PUBLIC_LEGACY_AVAILABLE:'true',NEXT_PUBLIC_LEGACY_ONLY:'false',NEXT_PUBLIC_LEGACY_REHEARSAL:'false',NEXT_PUBLIC_LEGACY_URL:'/legacy',NEXT_PUBLIC_CURRENT_URL:'/portfolio',
  NEXT_PUBLIC_MAINNET_PROFILE_JSON:raw,NEXT_PUBLIC_MAINNET_PROFILE_SHA256:digest};
 const api={NETWORK_ID:'1404',RPC_URL:'https://rpc.capedag.com/',EXPECTED_GENESIS_HASH:profile.genesisHash,STAKING_CONTRACT_ADDRESS:profile.stakingProxy,CHAIN_TALLY_ADDRESS:profile.epochClock.reader,INDEXER_CONFIRMATIONS:'64',
  STAKING_CONTRACTS_JSON:JSON.stringify([{version:'v1',address:profile.legacyStakingProxy,fromBlock:0,kind:'staking'},...[
    ['v2',profile.stakingProxy,'staking'],['mainnet-mining-vote',c.miningCredential,'miningCredential'],['mainnet-staking-vote',c.stakingCredential,'stakingCredential'],
    ['mainnet-governor',c.governor,'governance'],['mainnet-reader',profile.epochClock.reader,'chainTallyReader'],['mainnet-hub','0x1000000000000000000000000000000000000005','governanceHub'],
  ].map(([version,address,kind])=>({version,address,fromBlock:null,kind}))]),
  GOVERNANCE_FIXTURE_MANIFEST_PATH:'__APPROVED_ABSOLUTE_MAINNET_PROFILE_PATH__',GOVERNANCE_FIXTURE_MANIFEST_HASH:digest,DB_SCHEMA:'bdag_mainnet',INDEXER_START_BLOCK:'0'};
 const codeHashes={},chain={chainId:1404,genesisHash:profile.genesisHash};
 for(const [field,role]of Object.entries({vault:'bondVault',governor:'governor',registry:'developers',treasury:'treasury',timelock:'stakingTimelock'})){const p=profile.runtimePins[role];chain[field]=p.address.toLowerCase();codeHashes[chain[field]]=p.codeHash;}
 chain.codeHashes=codeHashes;
 const bondIndexer={rpcUrl:'https://rpc.capedag.com/',chain,mainnetEnvironment:development};
 return {development,staking,api,bondIndexer,notes:['Profiles deliberately remain review-only and production parsers reject them until the reviewed final seal.','Set approved HTTPS API/domain, wallet project IDs, isolated database/auth secrets and the exact activation/indexer boundary after approval.','All new contracts have unknown activation fromBlock=null and the placeholder path is intentionally invalid; do not install this template. Original Legacy remains indexed from genesis without truncating later exits.','The same exact compact profile bytes and SHA-256 must be used by every app/API. No secret is generated or included.','Phase1 bond indexing remains confirmed and source-pinned; Phase2 funding is disabled.']};
}
module.exports={platformProfile,environmentTemplates};
