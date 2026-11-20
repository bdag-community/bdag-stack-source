// Read-only release input capture. Never sends a transaction, impersonates an
// account, changes node configuration, or treats public RPC as native DB proof.
const fs = require('node:fs');
const { execFileSync } = require('node:child_process');
const { AbiCoder, id, keccak256 } = require('ethers');
const urls = ['https://rpc.capedag.com/', 'https://rpc.east.bdag-us.org/', 'https://rpc.bdagexplorer.com/'];
const GENESIS='0x3fb19ea409ac7399ad7b988b88e0d436722c967137e51e834ccefd7611a592ef';
const ROOT='0x564aeb23c21b395f94839991240f0665fdf6b083cad3a41ac6412fa2180ac76a';
const HISTORY='0x4845e973c4d5b7e3e8898d392e68bcef9e0f7cd6b6ed896be28f7e7d15a250cf';
const restrictionAccounts=require('./restriction-accounts.json').accounts;
const coder=AbiCoder.defaultAbiCoder();
const BLOCKLIST='0xe628505d5cB6F5F4f9a25Cd5c80Caa198cc645a0';
const SELECTIVE='0xc0a42A43A05e39C3a4165CA98440D516959f41D9';
const restrictedOwner='0x4f1536fc181c541f3ef766d227373f55d03ce0ba';
const STAKING='0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7';
const IMPL='0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const allowed=new Set(['eth_chainId','eth_blockNumber','eth_syncing','eth_getBlockByNumber','eth_getCode','eth_getStorageAt','eth_getBalance','eth_call','eth_getProof']);
function rpc(url, rows) {
  if(rows.some(([method])=>!allowed.has(method)))throw Error('Non-read RPC refused');
  const result=[];
  for(let start=0;start<rows.length;start+=20){
    const batch=rows.slice(start,start+20);
    const response=JSON.parse(execFileSync('curl',['-fsS','--max-time','25','-H','Content-Type: application/json','--data',JSON.stringify(batch.map(([method,params],id)=>({jsonrpc:'2.0',id,method,params}))),url],{encoding:'utf8',timeout:30000,maxBuffer:8*1024*1024}));
    if(!Array.isArray(response))throw Error('Batch response unavailable');
    for(let i=0;i<batch.length;i++){
      const row=response.find(v=>v.id===i);if(!row)throw Error('Missing response');
      result.push(row.error?{error:{code:row.error.code,message:row.error.message}}:row.result);
    }
  }
  return result;
}
const output=process.argv[2];if(!output)throw Error('Supply an output JSON path');
const identities=urls.map(url=>{
 try{const [chain,head,sync,genesis,historical]=rpc(url,[['eth_chainId',[]],['eth_blockNumber',[]],['eth_syncing',[]],['eth_getBlockByNumber',['0x0',false]],['eth_getBlockByNumber',['0x136e277',false]]]);
   if(chain!=='0x57c'||sync!==false||genesis.hash!==GENESIS||genesis.stateRoot!==ROOT||historical.hash!==HISTORY)throw Error('Canonical identity/history mismatch');
   return{url,verified:true,head:parseInt(head,16)};
 }catch(error){return{url,verified:false,error:error.message.slice(0,300)};}
});
const verified=identities.filter(v=>v.verified);if(verified.length<2)throw Error('At least two canonical providers required');
const fixed=Math.min(...verified.map(v=>v.head))-128,tag='0x'+fixed.toString(16);
const observations=[];
for(const {url} of verified){
 const [header,code,implementation,balance,...slots]=rpc(url,[['eth_getBlockByNumber',[tag,false]],['eth_getCode',[STAKING,tag]],['eth_getStorageAt',[STAKING,IMPL,tag]],['eth_getBalance',[STAKING,tag]],...Array.from({length:18},(_,i)=>['eth_getStorageAt',[STAKING,'0x'+i.toString(16),tag]])]);
 const implementationAddress='0x'+implementation.slice(-40);
 const [implementationCode,...interfaces]=rpc(url,[['eth_getCode',[implementationAddress,tag]],...['genesisTime()','EPOCH_DURATION()','INITIAL_BONDING_PERIOD()','UNBONDING_PERIOD()','epochDuration()','bondingPeriod()','stakingBlockedUntil(address)'].map(method=>['eth_call',[{to:STAKING,data:id(method).slice(0,10)+(method.includes('address')?'0'.repeat(64):'')},tag]])]);
 const restrictionValues=rpc(url,[...restrictionAccounts.map(address=>['eth_getStorageAt',[BLOCKLIST,keccak256(coder.encode(['address','uint256'],[address,1])),tag]]),['eth_getStorageAt',[SELECTIVE,keccak256(coder.encode(['address','uint256'],[restrictedOwner,1])),tag]]]);
 const [after]=rpc(url,[['eth_getBlockByNumber',[tag,false]]]);
 if(header.hash!==after.hash||header.stateRoot!==after.stateRoot)throw Error('Pinned checkpoint changed');
 observations.push({url,header:{number:header.number,hash:header.hash,stateRoot:header.stateRoot},staking:{address:STAKING,codeHash:keccak256(code),implementationAddress,implementationCodeHash:keccak256(implementationCode),balanceWei:BigInt(balance).toString(),slots,interfaceResults:interfaces,restrictions:{full:Object.fromEntries(restrictionAccounts.map((address,i)=>[address,restrictionValues[i]])),restrictedOwner:restrictionValues.at(-1)}}});
}
if(new Set(observations.map(v=>JSON.stringify(v.header))).size!==1||new Set(observations.map(v=>JSON.stringify(v.staking))).size!==1)throw Error('Providers disagree on fixed state');
fs.writeFileSync(output,JSON.stringify({checkedAt:new Date().toISOString(),transactionsSent:0,chainId:1404,fixedBlock:fixed,identities,observations,scope:'Public RPC identity/code/interface/top-level storage and14 historical blocklist keys plus known restricted owner. Not a complete mapping/position/liability census or paired native/EVM migration/replay proof.'},null,2)+'\n');
console.log(JSON.stringify({matched:observations.length,fixedBlock:fixed,header:observations[0].header,implementation:observations[0].staking.implementationAddress,interfaceResults:observations[0].staking.interfaceResults}));
