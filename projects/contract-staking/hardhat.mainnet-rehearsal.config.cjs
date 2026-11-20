// Isolated in-process EVM only. This config has no external RPC or signer keys.
const base=require('./hardhat.phase1.config.cjs');
module.exports={...base,networks:{hardhat:{...base.networks.hardhat,chainId:1404,initialDate:'2027-01-01T00:00:00Z'}},
 paths:{...base.paths,tests:'./test-mainnet',cache:'./cache',artifacts:'./artifacts'}};
