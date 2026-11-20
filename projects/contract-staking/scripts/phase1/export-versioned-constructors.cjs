// Isolated native-EVM qualification input, never a live deployment script.
const fs=require('node:fs');
const path=require('node:path');
const assert=require('node:assert/strict');
const hre=require('hardhat');
const {buildVersionedRuntimeBundle}=require('./versioned-runtime-bundle.cjs');
async function main(){
  assert.equal(hre.network.name,'hardhat');
  assert.equal((await hre.ethers.provider.getNetwork()).chainId,1043n);
  const bundle=await buildVersionedRuntimeBundle(hre),a=bundle.addresses;
  const baseline=JSON.parse(fs.readFileSync(path.resolve(__dirname,'../../out/governance-testnet-release-v1/manifest.json'),'utf8'));
  const args=new Map([
    [a.reader,[a.stakingProxy]],
    [a.timing,[a.governor,a.reader,60,259200,604800,[3,7,1,2]]],
    [a.stakingImplementation,[a.timing]],
    [a.miningCredential,[a.stakingProxy,a.reader,'BlockDAG Mining Vote','mVOTE']],
    [a.stakingCredential,[a.stakingProxy,hre.ethers.ZeroAddress,'BlockDAG Staking Vote','sVOTE']],
    [a.miningTimelock,[a.timing]],[a.stakingTimelock,[a.timing]],
    [a.governor,[a.miningCredential,a.stakingCredential,a.miningTimelock,a.stakingTimelock,a.policy,[60,26,60,hre.ethers.parseEther('0.001')],baseline.routes,a.timing]],
    [a.escrow,[a.reader,a.timing]],
    [a.factory,[a.stakingTimelock,a.developers,a.reader,a.timing,a.legacyEscrow,2419200]],
  ]);
  const rows=[];
  for(const row of bundle.runtimes){
    const artifact=await hre.artifacts.readArtifact(row.contract);
    const constructorData=new hre.ethers.Interface(artifact.abi).encodeDeploy(args.get(row.address));
    let relocation=[];
    if(row.address===a.factory){
      const info=await hre.artifacts.getBuildInfo(`${artifact.sourceName}:${row.contract}`);
      const ast=info.output.sources[artifact.sourceName].ast;
      const contract=ast.nodes.find(node=>node.nodeType==='ContractDefinition'&&node.name===row.contract);
      const implementation=contract.nodes.find(node=>node.nodeType==='VariableDeclaration'&&node.name==='implementation');
      assert(implementation);
      relocation=info.output.contracts[artifact.sourceName][row.contract].evm.deployedBytecode.immutableReferences[String(implementation.id)];
      assert(relocation.length>0);
    }
    rows.push({address:row.address,contract:row.contract,initCode:hre.ethers.concat([artifact.bytecode,constructorData]),factoryImplementationRelocation:relocation});
  }
  const output=path.resolve(__dirname,'../../out/testnet-timing-v1-candidate/constructors.json');
  fs.writeFileSync(output,JSON.stringify(rows,null,2)+'\n');
  console.log(JSON.stringify({output,constructors:rows.length,liveDeployment:false}));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
