const {expect}=require('chai');
const {ethers}=require('hardhat');
const {time}=require('@nomicfoundation/hardhat-network-helpers');
describe('Project funding, refunds and accountable releases',function(){
 async function fixture(mode=0){
  const [owner,developer,payout,alice,bob,c1,c2,c3,retiredKey]=await ethers.getSigners();
  const registry=await ethers.deployContract('ApprovedDevRegistry',[owner.address,[c1.address,c2.address,c3.address]]);
  await registry.setApproval(developer.address,true,ethers.id('review'));
  const factory=await ethers.deployContract('ProjectEscrowFactory',[owner.address,registry.target,28*86400]);
  const id=ethers.id('project'), deadline=(await time.latest())+100;
  await factory.createProject(id,developer.address,payout.address,1000,deadline,mode,[c1.address,c2.address,c3.address],mode?[400,600]:[]);
  const escrow=await ethers.getContractAt('ProjectEscrow',await factory.projects(id));
  const context=await ethers.deployContract('TreasuryContextFixture');const treasury=await ethers.deployContract('CommunityTreasury',[owner.address,context.target,context.target]);
  return {owner,developer,payout,alice,bob,c1,c2,c3,retiredKey,registry,factory,id,deadline,escrow,treasury};
 }
 it('only governance approves reviewed developers; reviewers cannot bid',async()=>{
  const f=await fixture();
  await expect(f.registry.connect(f.alice).setApproval(f.alice.address,true,ethers.id('x'))).to.be.reverted;
  await expect(f.registry.setApproval(f.c1.address,true,ethers.id('x'))).to.be.reverted;
  await expect(f.factory.connect(f.developer).createProject(ethers.id('x'),f.developer.address,f.payout.address,1,f.deadline,0,[f.c1.address,f.c2.address,f.c3.address],[])).to.be.reverted;
  await f.registry.setApproval(f.developer.address,false,ethers.id('revocation'));
  expect(await f.registry.isApproved(f.developer.address)).to.equal(false);
 });
 it('keeps contributions in escrow until the exact goal, then pays only fixed payout once',async()=>{
  const f=await fixture();await f.escrow.connect(f.alice).contribute({value:600});
  await expect(f.escrow.release()).to.be.reverted;
  await expect(f.escrow.connect(f.bob).contribute({value:401})).to.be.reverted;
  await f.escrow.connect(f.bob).contribute({value:400});
  await expect(f.escrow.connect(f.alice).refund()).to.be.reverted;
  await expect(f.escrow.connect(f.bob).release()).to.changeEtherBalance(f.payout,1000);
  await expect(f.escrow.release()).to.be.reverted;
 });
 it('allows permissionless refunds only to each contributor after failed funding',async()=>{
  const f=await fixture();await f.escrow.connect(f.alice).contribute({value:600});
  await expect(f.escrow.refundFor(f.alice.address)).to.be.reverted;
  await time.increaseTo(f.deadline);
  await expect(f.escrow.connect(f.bob).refundFor(f.alice.address)).to.changeEtherBalance(f.alice,600);
  await expect(f.escrow.refundFor(f.alice.address)).to.be.reverted;
  await expect(f.escrow.contribute({value:1})).to.be.reverted;
 });
 it('requires two distinct reviewers and a full challenge period for milestone release',async()=>{
  const f=await fixture(1);await f.escrow.connect(f.alice).contribute({value:1000});
  await expect(f.escrow.connect(f.alice).submitMilestone(ethers.id('evidence'))).to.be.reverted;
  await f.escrow.connect(f.developer).submitMilestone(ethers.id('evidence'));
  await f.escrow.connect(f.c1).approveMilestone();
  await expect(f.escrow.connect(f.c1).approveMilestone()).to.be.reverted;
  await expect(f.escrow.releaseMilestone()).to.be.reverted;
  await f.escrow.connect(f.c2).approveMilestone();
  await expect(f.escrow.releaseMilestone()).to.be.reverted;
  await time.increase(28*86400);
  await expect(f.escrow.releaseMilestone()).to.changeEtherBalance(f.payout,400);
  expect(await f.escrow.released()).to.equal(400);
  await f.escrow.connect(f.developer).submitMilestone(ethers.id('evidence2'));
  await f.escrow.connect(f.c1).approveMilestone();await f.escrow.connect(f.c3).approveMilestone();await time.increase(28*86400);
  await expect(f.escrow.releaseMilestone()).to.changeEtherBalance(f.payout,600);
  await expect(f.escrow.connect(f.developer).submitMilestone(ethers.id('extra'))).to.be.reverted;
 });
 it('exposes no Guardian veto and requires an executed governance decision',async()=>{
  const f=await fixture(1);await f.escrow.connect(f.alice).contribute({value:1000});await f.escrow.connect(f.developer).submitMilestone(ethers.id('bad'));
  await f.escrow.connect(f.c1).approveMilestone();await f.escrow.connect(f.c2).approveMilestone();
  expect(f.escrow.interface.hasFunction('guardian')).to.equal(false);
  await expect(f.escrow.connect(f.retiredKey).vetoMilestone()).to.be.reverted;
  await f.factory.vetoProject(f.id);await time.increase(28*86400);
  await expect(f.escrow.releaseMilestone()).to.be.reverted;expect(await ethers.provider.getBalance(f.escrow.target)).to.equal(1000);
 });
 it('lets only governance route an existing tracked project veto through its factory',async()=>{
  const f=await fixture(1);await f.escrow.connect(f.alice).contribute({value:1000});await f.escrow.connect(f.developer).submitMilestone(ethers.id('bad'));
  await expect(f.factory.connect(f.developer).vetoProject(f.id)).to.be.reverted;
  await expect(f.factory.vetoProject(ethers.id('unknown'))).to.be.reverted;
  await f.factory.vetoProject(f.id);expect(await f.escrow.evidenceHash()).to.equal(ethers.ZeroHash);
  await expect(f.escrow.releaseMilestone()).to.be.reverted;
 });
 it('returns only unspent funds pro rata after 180 days without a release',async()=>{
  const f=await fixture(1);await f.escrow.connect(f.alice).contribute({value:600});await f.escrow.connect(f.bob).contribute({value:400});
  await f.escrow.connect(f.developer).submitMilestone(ethers.id('done'));await f.escrow.connect(f.c1).approveMilestone();await f.escrow.connect(f.c2).approveMilestone();await time.increase(28*86400);await f.escrow.releaseMilestone();
  await expect(f.escrow.activateReclaim()).to.be.reverted;await time.increase(180*86400);await f.escrow.activateReclaim();
  await expect(f.escrow.refundFor(f.alice.address)).to.changeEtherBalance(f.alice,360);
  await expect(f.escrow.refundFor(f.bob.address)).to.changeEtherBalance(f.bob,240);
  await expect(f.escrow.connect(f.developer).submitMilestone(ethers.id('late'))).to.be.reverted;
 });
 it('rejects duplicate ids, unapproved developers and contradictory milestone allocations',async()=>{
  const f=await fixture();
  const args=[f.id,f.developer.address,f.payout.address,1000,f.deadline,0,[f.c1.address,f.c2.address,f.c3.address],[]];
  await expect(f.factory.createProject(...args)).to.be.reverted;
  await expect(f.factory.createProject(ethers.id('new'),f.alice.address,...args.slice(2))).to.be.reverted;
  await expect(f.factory.createProject(ethers.id('tracked'),f.developer.address,f.payout.address,1000,f.deadline,1,[f.c1.address,f.c2.address,f.c3.address],[999])).to.be.reverted;
 });
 it('rejects EOA treasury configuration and lets anyone deliver forfeiture to a real treasury',async()=>{
  const f=await fixture();const context=await ethers.deployContract('TreasuryContextFixture');const treasury=await ethers.deployContract('CommunityTreasury',[f.owner.address,context.target,context.target]);
  await expect(ethers.deployContract('BidBondVault',[f.owner.address,f.registry.target,f.payout.address])).to.be.reverted;
  const vault=await ethers.deployContract('BidBondVault',[f.owner.address,f.registry.target,treasury.target]);const id=ethers.id('treasury-bid');await vault.connect(f.developer).deposit(id,1000,{value:20});await vault.resolveDelivery(id,false,ethers.id('abandonment evidence'));
  await expect(vault.connect(f.alice).withdrawFor(treasury.target)).to.changeEtherBalance(treasury,20);
  await expect(vault.withdrawFor(treasury.target)).to.be.reverted;
 });
 it('allocates reclaim rounding remainder to the last contributor without locking dust',async()=>{
  const f=await fixture(1);const id=ethers.id('dust');await f.factory.createProject(id,f.developer.address,f.payout.address,1000,f.deadline,1,[f.c1.address,f.c2.address,f.c3.address],[333,667]);const e=await ethers.getContractAt('ProjectEscrow',await f.factory.projects(id));
  await e.connect(f.alice).contribute({value:500});await e.connect(f.bob).contribute({value:500});await e.connect(f.developer).submitMilestone(ethers.id('done'));await e.connect(f.c1).approveMilestone();await e.connect(f.c2).approveMilestone();await time.increase(28*86400);await e.releaseMilestone();await time.increase(180*86400);await e.activateReclaim();
  await expect(e.refundFor(f.alice.address)).to.changeEtherBalance(f.alice,333);await expect(e.refundFor(f.bob.address)).to.changeEtherBalance(f.bob,334);expect(await ethers.provider.getBalance(e.target)).to.equal(0);
 });
 it('escrows a rounded-up 2% bid bond, with governance resolution and one pull withdrawal',async()=>{
  const f=await fixture();const vault=await ethers.deployContract('BidBondVault',[f.owner.address,f.registry.target,f.treasury.target]);const id=ethers.id('bid');
  await expect(vault.connect(f.developer).deposit(id,1001,{value:20})).to.be.reverted;
  await vault.connect(f.developer).deposit(id,1001,{value:21});
  await expect(vault.connect(f.developer).resolveUnselected(id,ethers.id('selection result'))).to.be.reverted;
  await f.registry.setApproval(f.developer.address,false,ethers.id('revoked'));
  await vault.resolveUnselected(id,ethers.id('selection result'));expect((await vault.bonds(id)).status).to.equal(3);await expect(vault.connect(f.developer).withdraw()).to.changeEtherBalance(f.developer,21);await expect(vault.connect(f.developer).withdraw()).to.be.reverted;
 });
 it('forfeits a resolved bond only to the immutable community treasury',async()=>{
  const f=await fixture();const vault=await ethers.deployContract('BidBondVault',[f.owner.address,f.registry.target,f.treasury.target]);const id=ethers.id('bid');await vault.connect(f.developer).deposit(id,1000,{value:20});await vault.resolveDelivery(id,false,ethers.id('critical abandonment decision'));const bond=await vault.bonds(id);expect(bond.status).to.equal(2);expect(bond.evidence).to.equal(ethers.id('critical abandonment decision'));await expect(vault.connect(f.alice).withdrawFor(f.treasury.target)).to.changeEtherBalance(f.treasury,20);await expect(vault.resolveDelivery(id,true,ethers.id('late'))).to.be.reverted;
 });
});
