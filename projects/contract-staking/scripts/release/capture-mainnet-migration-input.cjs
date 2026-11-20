// Read-only public code/pre-state capture. No signer, wallet or transaction RPC.
const fs=require('node:fs');const {execFileSync}=require('node:child_process');
const {keccak256,AbiCoder}=require('ethers');
const restrictionAccounts=require('./restriction-accounts.json').accounts;
const coder=AbiCoder.defaultAbiCoder();
const urls=['https://rpc.capedag.com/','https://rpc.east.bdag-us.org/'];
const legacy='0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7',implementation='0xdc1440ab25b1a3727ddc4ad2bd20a185a0c03fea';
const blocklist='0xe628505d5cB6F5F4f9a25Cd5c80Caa198cc645a0',selective='0xc0a42A43A05e39C3a4165CA98440D516959f41D9';
const governance='0x1000000000000000000000000000000000000005';
const implSlot='0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
function read(url,queries){
 if(queries.some(([m])=>!['eth_chainId','eth_getBlockByNumber','eth_getCode','eth_getStorageAt'].includes(m)))throw Error('Read-only method required');
 const data=queries.map(([method,params],id)=>({jsonrpc:'2.0',id,method,params}));
 const r=JSON.parse(execFileSync('curl',['-fsS','--max-time','30','-H','Content-Type: application/json','--data',JSON.stringify(data),url],{encoding:'utf8',maxBuffer:4*1024*1024}));
 return queries.map((_,id)=>{const row=r.find(x=>x.id===id);if(!row||row.error)throw Error('Read unavailable at fixed checkpoint');return row.result;});
}
function main(){
 const output=process.argv[2];if(!output)throw Error('Supply a local output path');
 const number=26827713,tag='0x'+number.toString(16),rows=[];
 for(const url of urls){
  const [chain,genesis,baseline,...values]=read(url,[['eth_chainId',[]],['eth_getBlockByNumber',['0x0',false]],['eth_getBlockByNumber',[tag,false]],
   ...[legacy,implementation,blocklist,selective,governance].map(a=>['eth_getCode',[a,tag]]),
   ...[legacy,blocklist,selective].map(a=>['eth_getStorageAt',[a,'0x0',tag]]),
   ['eth_getStorageAt',[legacy,'0x8',tag]],['eth_getStorageAt',[legacy,implSlot,tag]],
   ...restrictionAccounts.map(address=>['eth_getStorageAt',[blocklist,keccak256(coder.encode(['address','uint256'],[address,1])),tag]]),
   ['eth_getStorageAt',[selective,keccak256(coder.encode(['address','uint256'],['0x4f1536fc181c541f3ef766d227373f55d03ce0ba',1])),tag]]]);
  if(chain!=='0x57c'||genesis.hash!=='0x3fb19ea409ac7399ad7b988b88e0d436722c967137e51e834ccefd7611a592ef'||genesis.stateRoot!=='0x564aeb23c21b395f94839991240f0665fdf6b083cad3a41ac6412fa2180ac76a')throw Error('Canonical genesis mismatch');
  if(baseline.hash!=='0x43ee06b99e53f7245b134ff125edb91e636674fd421bc5796e3eac0f3dc3eb23')throw Error('Canonical checkpoint mismatch');
  const [legacyCode,implementationCode,blocklistCode,selectiveCode,governanceCode,legacyOwner,blocklistOwner,selectiveOwner,legacyGenesis,impl,...restrictionValues]=values;
  if(impl.toLowerCase().slice(-40)!==implementation.slice(2))throw Error('Unexpected legacy implementation');
  rows.push({genesis,baseline,legacy,implementation,blocklist,selective,governance,legacyCode,implementationCode,blocklistCode,selectiveCode,governanceCode,
   legacyOwner:'0x'+legacyOwner.slice(-40),blocklistOwner:'0x'+blocklistOwner.slice(-40),selectiveOwner:'0x'+selectiveOwner.slice(-40),legacyGenesisTime:Number(BigInt(legacyGenesis)),
   restrictionStorage:{[blocklist]:Object.fromEntries(restrictionAccounts.map((a,i)=>[keccak256(coder.encode(['address','uint256'],[a,1])),restrictionValues[i]])),
    [selective]:{[keccak256(coder.encode(['address','uint256'],['0x4f1536fc181c541f3ef766d227373f55d03ce0ba',1]))]:restrictionValues.at(-1)}}});
 }
 if(JSON.stringify(rows[0])!==JSON.stringify(rows[1]))throw Error('Canonical providers disagree');
 const value={schema:'bdag.mainnet-migration.readonly-input.v1',capturedAt:new Date().toISOString(),transactionsSent:0,providers:urls,...rows[0],
  scope:'Public canonical header/code and fixed owner/clock slots; no balances or user-state replacement and no claim of complete liabilities.'};
 fs.writeFileSync(output,JSON.stringify(value,null,2)+'\n');console.log(JSON.stringify({output,number,implementationCodeHash:keccak256(value.implementationCode),providers:urls.length,transactionsSent:0}));
}
if(require.main===module)main();
