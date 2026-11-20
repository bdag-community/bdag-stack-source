const {expect}=require('chai');const {ethers}=require('hardhat');const {time}=require('@nomicfoundation/hardhat-network-helpers');
const coder=ethers.AbiCoder.defaultAbiCoder();
describe('Funding authority through actual governor and two timelocks',function(){
 async function fixture(treasuryDomain=3){
  const p=await ethers.getSigners(),start=(await time.latest())-600;
  const mining=await ethers.deployContract('GovernanceCredentialFixture',[start,60]);const staking=await ethers.deployContract('GovernanceCredentialFixture',[start,60]);
  const n=await ethers.provider.getTransactionCount(p[0].address),address=i=>ethers.getCreateAddress({from:p[0].address,nonce:n+i});
  const ml=await ethers.deployContract('GovernanceTimelock',[5,address(3)]);const sl=await ethers.deployContract('GovernanceTimelock',[5,address(3)]);
  const policy=await ethers.deployContract('GovernancePolicyModule',[address(3),ml.target]);
  const route=(target,signature,domain=3)=>({target,selector:ethers.id(signature).slice(0,10),domain,tier:domain===3?2:1,ownerDomain:2});
  const gov=await ethers.deployContract('EpochGovernor',[mining.target,staking.target,ml.target,sl.target,policy.target,{epochSeconds:60,ordinaryDelay:5,criticalDelay:15,proposalBond:1000},[
   route(address(4),'spend(address,uint256)',treasuryDomain),route(address(5),'setApproval(address,bool,bytes32)'),route(address(6),'createProject(bytes32,address,address,uint256,uint64,uint8,address[3],uint256[])'),route(address(6),'vetoProject(bytes32)'),route(address(7),'resolveDelivery(bytes32,bool,bytes32)')]]);
  const treasury=await ethers.deployContract('CommunityTreasury',[sl.target,gov.target,staking.target]);const registry=await ethers.deployContract('ApprovedDevRegistry',[sl.target,[p[50].address,p[51].address,p[52].address]]);const factory=await ethers.deployContract('ProjectEscrowFactory',[sl.target,registry.target,28*24*60*60]);const bond=await ethers.deployContract('BidBondVault',[sl.target,registry.target,treasury.target]);
  for(let i=1;i<=10;i++)await mining.setWeight(p[i].address,100);
  for(let i=11;i<=35;i++)await staking.setWeight(p[i].address,40);
  await p[0].sendTransaction({to:treasury.target,value:10000});return{p,start,mining,staking,gov,treasury,registry,factory,bond};
 }
 async function approve(f,targets,data,domain=3){
  const description='funding '+ethers.hexlify(ethers.randomBytes(8)),values=targets.map(()=>0),dh=ethers.id(description);
  await f.gov.connect(f.p[domain===2?11:1]).proposeWithDeposit(targets,values,data,description,{value:1000});const id=await f.gov.hashProposal(targets,values,data,dh);let epoch=await f.mining.currentEpoch();await time.increaseTo(f.start+Number(epoch+1n)*60);
  if(domain===3)for(let i=1;i<=10;i++)await f.gov.connect(f.p[i]).castVoteWithReasonAndParams(id,1,'',coder.encode(['uint8','bytes'],[1,'0x']));
  for(let i=11;i<=35;i++)await f.gov.connect(f.p[i]).castVoteWithReasonAndParams(id,1,'',coder.encode(['uint8','bytes'],[2,'0x']));
  epoch=await f.mining.currentEpoch();await time.increaseTo(f.start+Number(epoch+1n)*60);await f.gov.queue(targets,values,data,dh);await time.increaseTo(await f.gov.proposalEta(id));return{args:[targets,values,data,dh],id};
 }
 it('reads the real CORE id and aggregates treasury spending across a batch',async()=>{const f=await fixture();const x=await approve(f,[f.treasury.target,f.treasury.target],[f.treasury.interface.encodeFunctionData('spend',[f.p[40].address,60]),f.treasury.interface.encodeFunctionData('spend',[f.p[41].address,40])]);await f.gov.execute(...x.args);expect(await f.treasury.proposalSpent(x.id)).to.equal(100);expect(await f.gov.executingCoreProposal()).to.equal(0);});
 it('refuses even a passing STAKING/PARAM route without CORE execution context',async()=>{const f=await fixture(2);const x=await approve(f,[f.treasury.target],[f.treasury.interface.encodeFunctionData('spend',[f.p[40].address,100])],2);await expect(f.gov.execute(...x.args)).to.be.reverted;expect(await ethers.provider.getBalance(f.treasury.target)).to.equal(10000);});
 it('approves a developer and creates the fixed-payout escrow through CORE execution',async()=>{const f=await fixture();const id=ethers.id('approved project');const x=await approve(f,[f.registry.target,f.factory.target],[f.registry.interface.encodeFunctionData('setApproval',[f.p[40].address,true,ethers.id('review')]),f.factory.interface.encodeFunctionData('createProject',[id,f.p[40].address,f.p[41].address,1000,(await time.latest())+1000,0,[f.p[50].address,f.p[51].address,f.p[52].address],[]])]);await f.gov.execute(...x.args);const escrow=await ethers.getContractAt('ProjectEscrow',await f.factory.projects(id));expect(await escrow.governance()).to.equal(await f.gov.stakingTimelock());await escrow.connect(f.p[42]).contribute({value:1000});await expect(escrow.release()).to.changeEtherBalance(f.p[41],1000);});
 it('routes a tracked project veto through a real CORE proposal',async()=>{const f=await fixture();const id=ethers.id('tracked project');const createData=[
  f.registry.interface.encodeFunctionData('setApproval',[f.p[40].address,true,ethers.id('review')]),
  f.factory.interface.encodeFunctionData('createProject',[id,f.p[40].address,f.p[41].address,1000,(await time.latest())+1000,1,[f.p[50].address,f.p[51].address,f.p[52].address],[1000]])
 ];const create=await approve(f,[f.registry.target,f.factory.target],createData);await f.gov.execute(...create.args);const escrow=await ethers.getContractAt('ProjectEscrow',await f.factory.projects(id));await escrow.connect(f.p[42]).contribute({value:1000});await escrow.connect(f.p[40]).submitMilestone(ethers.id('bad'));const veto=await approve(f,[f.factory.target],[f.factory.interface.encodeFunctionData('vetoProject',[id])]);await f.gov.execute(...veto.args);expect(await escrow.evidenceHash()).to.equal(ethers.ZeroHash);});
});
