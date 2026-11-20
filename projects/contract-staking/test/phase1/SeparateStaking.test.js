const {expect} = require('chai');
const {ethers, network, artifacts} = require('hardhat');
const {time} = require('@nomicfoundation/hardhat-network-helpers');
const READER='0x1000000000000000000000000000000000000002';
const IMPL=BigInt('0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc');
const word=v=>ethers.toBeHex(v,32);
const set=(a,s,v)=>network.provider.send('hardhat_setStorageAt',[a,ethers.toQuantity(s),word(v)]);
const map=(k,s,type='uint256')=>BigInt(ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode([type,'uint256'],[k,s])));
const nested=(a,e,s)=>map(e,map(a,s,'address'));
const UNIT=ethers.parseEther('1');

describe('Separate staking sources on one chain',function(){
  let snapshot,owner,alice,pool,legacy,fresh,genesis,timing,reader;
  beforeEach(async()=>{
    snapshot=await network.provider.send('evm_snapshot');
    [owner,alice,pool]=await ethers.getSigners();
    await network.provider.send('hardhat_setCode',[READER,'0x']);
    const impl=await ethers.deployContract('StakingContractV2Testnet');
    const proxy=await ethers.deployContract('UUPSProxy',[impl.target,impl.interface.encodeFunctionData('initialize')]);
    legacy=impl.attach(proxy.target);genesis=await legacy.genesisTime();
    timing=await ethers.deployContract('TimingExecutionFixture');
    const old=await ethers.deployContract('VersionedTestnetStaking',[timing.target]);
    await set(legacy.target,IMPL,BigInt(old.target));legacy=old.attach(legacy.target);
    reader=await ethers.deployContract('SeparatedChainTallyReader',[legacy.target]);
    await network.provider.send('hardhat_setCode',[READER,await ethers.provider.getCode(reader.target)]);
    reader=reader.attach(READER);
    await set(READER,8n,1n);
    const base=BigInt(ethers.keccak256(word(8)));
    for(const [i,v] of [0n,genesis,1800n].entries())await set(READER,base+BigInt(i),v);
    await set(READER,4n,1n);await set(READER,5n,1n);await set(READER,6n,2n);
    const next=await ethers.deployContract('SeparatedStaking',[timing.target,legacy.target]);
    const freshProxy=await ethers.deployContract('UUPSProxy',[next.target,next.interface.encodeFunctionData('initialize')]);
    fresh=next.attach(freshProxy.target);
    await set(fresh.target,8n,genesis);
    await set(READER,10n,BigInt(fresh.target));await set(READER,11n,3n); // source epoch 2 is the first new epoch
  });
  afterEach(async()=>{await network.provider.send('evm_revert',[snapshot]);});
  async function deposit(contract,amount=10n*UNIT){return(await contract.connect(alice).stake(alice.address,amount,pool.address,{value:amount})).wait();}
  async function rewards(contract,epoch,units){
    await set(contract.target,nested(pool.address,epoch,34n),units);
    await set(contract.target,map(epoch,36n),units*10n*UNIT);
  }
  it('keeps the legacy clock identity and routes historical reward sources without aliasing',async()=>{
    expect(await reader.clockSource()).to.equal(legacy.target);
    expect(await reader.activeStakingSource()).to.equal(fresh.target);
    expect(await reader.stakingSourceAt(1)).to.equal(legacy.target);
    expect(await reader.stakingSourceAt(2)).to.equal(fresh.target);
    expect(await legacy.currentEpoch()).to.equal(await fresh.currentEpoch());
    expect(await fresh.genesisTime()).to.equal(genesis);
  });
  it('preserves old pending exits and allows a separate fresh bonded stake on the same wallet/network',async()=>{
    await deposit(legacy);
    await legacy.connect(alice).unstake(alice.address,UNIT,[pool.address]);
    const pending=await legacy.getUnbondingRequests(alice.address);
    const end=await legacy.bondingEndsAt(alice.address);
    const balance=await ethers.provider.getBalance(legacy.target);
    await time.increaseTo(genesis+3601n);
    const receipt=await deposit(fresh);
    const first=BigInt((await ethers.provider.getBlock(receipt.blockNumber)).timestamp);
    expect(await fresh.bondingEndsAt(alice.address)).to.equal(first+7200n);
    expect(await legacy.getUnbondingRequests(alice.address)).to.deep.equal(pending);
    expect(await ethers.provider.getBalance(legacy.target)).to.equal(balance);
    expect(await fresh.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(10n*UNIT);
    expect(await legacy.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(9n*UNIT);
    await time.increaseTo(end>pending[0].releaseTime?end:pending[0].releaseTime);
    await expect(legacy.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,UNIT);
    expect(await fresh.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(10n*UNIT);
    await expect(legacy.connect(alice).claimStake(alice.address,pool.address)).to.be.reverted;
  });
  it('uses new rewards only for V2 voting and retains historical old-source power',async()=>{
    await deposit(legacy);await deposit(fresh);
    await rewards(legacy,1n,3n);await rewards(legacy,2n,100n);await rewards(fresh,2n,7n);
    const credential=await ethers.deployContract('SeparatedEpochCredential',[legacy.target,ethers.ZeroAddress,'Stake Vote','sVOTE']);
    await time.increaseTo(genesis+5401n);await set(READER,4n,3n);
    expect(await credential.rawWeightOf(alice.address,2n,[pool.address])).to.equal(30n*UNIT);
    expect(await credential.rawWeightOf(alice.address,3n,[pool.address])).to.equal(70n*UNIT);
    expect(await credential.totalSupplyAt(3n)).to.equal(70n*UNIT);
    await expect(credential.rawWeightOf(alice.address,4n,[pool.address])).to.be.reverted;
  });
  it('lets a new V2 position earn, unstake and claim without touching the retiring position',async()=>{
    await deposit(legacy);await deposit(fresh);
    // Native credits increase the active vault and its backing by the same amount.
    await set(fresh.target,map(pool.address,5n,'address'),11n*UNIT);
    await network.provider.send('hardhat_setBalance',[fresh.target,ethers.toQuantity(11n*UNIT)]);
    expect(await fresh.poolStakerYield(pool.address,alice.address)).to.equal(UNIT);
    await fresh.connect(alice).unstake(alice.address,11n*UNIT,[pool.address]);
    const pending=await fresh.getUnbondingRequests(alice.address);
    const end=await fresh.bondingEndsAt(alice.address);
    await expect(fresh.connect(alice).claimStake(alice.address,pool.address)).to.be.reverted;
    await time.increaseTo(end>pending[0].releaseTime?end:pending[0].releaseTime);
    await expect(fresh.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,11n*UNIT);
    expect(await ethers.provider.getBalance(fresh.target)).to.equal(0n);
    expect(await legacy.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(10n*UNIT);
  });
  it('completes legacy exit to wallet then an entirely new V2 stake and exit on one chain',async()=>{
    const chain=(await ethers.provider.getNetwork()).chainId;
    await deposit(legacy);
    const oldFirst=await legacy.userFirstDepositTimestamp(alice.address);
    await legacy.connect(alice).unstake(alice.address,10n*UNIT,[pool.address]);
    const oldExit=(await legacy.getUnbondingRequests(alice.address))[0].releaseTime;
    const oldBond=await legacy.bondingEndsAt(alice.address);
    await time.increaseTo(oldExit>oldBond?oldExit:oldBond);
    await expect(legacy.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,10n*UNIT);
    expect(await ethers.provider.getBalance(legacy.target)).to.equal(0n);
    expect(await fresh.stakerPrincipalBDAG(alice.address,pool.address)).to.equal(0n);
    const receipt=await deposit(fresh);
    const first=BigInt((await ethers.provider.getBlock(receipt.blockNumber)).timestamp);
    expect(first).to.be.greaterThan(oldFirst);
    expect(await fresh.bondingEndsAt(alice.address)).to.equal(first+7200n);
    await fresh.connect(alice).unstake(alice.address,10n*UNIT,[pool.address]);
    const exit=(await fresh.getUnbondingRequests(alice.address))[0].releaseTime;
    await time.increaseTo(exit>first+7200n?exit:first+7200n);
    await expect(fresh.connect(alice).claimStake(alice.address,pool.address)).to.changeEtherBalance(alice,10n*UNIT);
    expect(await ethers.provider.getBalance(fresh.target)).to.equal(0n);
    expect((await ethers.provider.getNetwork()).chainId).to.equal(chain);
  });
  it('allows unclaimed old receipts and delegation history to remain visible after source replacement',async()=>{
    await deposit(legacy);await deposit(fresh);await rewards(legacy,1n,3n);
    const old=await ethers.deployContract('VersionedEpochCredential',[legacy.target,ethers.ZeroAddress,'Stake Vote','sVOTE']);
    await time.increaseTo(genesis+3601n);await set(READER,4n,2n);
    await old.claimFor(alice.address,2n,[pool.address]);
    await old.connect(alice).delegate(pool.address);
    const next=await ethers.deployContract('SeparatedEpochCredential',[legacy.target,ethers.ZeroAddress,'Stake Vote','sVOTE']);
    await network.provider.send('hardhat_setCode',[old.target,await ethers.provider.getCode(next.target)]);
    const credential=next.attach(old.target);
    expect(await credential['balanceOf(address,uint256)'](alice.address,2n)).to.equal(30n*UNIT);
    expect(await credential.delegateAt(alice.address,3n)).to.equal(pool.address);
    expect(await credential.nonces(alice.address)).to.equal(1n);
    expect(await credential.rawWeightOf(alice.address,2n,[pool.address])).to.equal(30n*UNIT);
  });
  it('rejects missing, aliased or wrong-genesis source state instead of guessing',async()=>{
    await set(READER,10n,BigInt(legacy.target));
    await expect(reader.activeStakingSource()).to.be.reverted;
    await expect(fresh.currentEpoch()).to.be.reverted;
    await set(READER,10n,BigInt(fresh.target));await set(fresh.target,8n,genesis+1n);
    await expect(fresh.currentEpoch()).to.be.reverted;
  });
  it('preserves existing DAO staking restrictions when a wallet tries the fresh V2',async()=>{
    await legacy.setStakingBlockedUntil(alice.address,ethers.MaxUint256);
    await expect(deposit(fresh)).to.be.revertedWithCustomError(fresh,'StakingBlocked');
  });
  it('rejects an unused old-source delegation signature after cutover while retaining nonces',async()=>{
    const old=await ethers.deployContract('VersionedEpochCredential',[legacy.target,ethers.ZeroAddress,'Stake Vote','sVOTE']);
    const domain={name:'Stake Vote',version:'1',chainId:(await ethers.provider.getNetwork()).chainId,verifyingContract:old.target};
    const types={Delegation:[{name:'account',type:'address'},{name:'delegatee',type:'address'},{name:'nonce',type:'uint256'},{name:'deadline',type:'uint256'}]};
    const message={account:alice.address,delegatee:pool.address,nonce:0n,deadline:genesis+86400n};
    const signature=await alice.signTypedData(domain,types,message);
    const next=await ethers.deployContract('SeparatedEpochCredential',[legacy.target,ethers.ZeroAddress,'Stake Vote','sVOTE']);
    await network.provider.send('hardhat_setCode',[old.target,await ethers.provider.getCode(next.target)]);
    const credential=next.attach(old.target);
    await expect(credential.delegateBySig(alice.address,pool.address,0n,message.deadline,signature)).to.be.revertedWithCustomError(credential,'InvalidSignature');
    const freshSignature=await alice.signTypedData({...domain,version:'2'},types,message);
    await credential.delegateBySig(alice.address,pool.address,0n,message.deadline,freshSignature);
    expect(await credential.nonces(alice.address)).to.equal(1n);
  });
  it('preserves credential storage layout and keeps historical sources immutable in the shipped contracts',async()=>{
    const old=await artifacts.getBuildInfo('contracts/credentials/EpochCredential.sol:EpochCredential');
    const next=await artifacts.getBuildInfo('contracts/credentials/SeparatedEpochCredential.sol:SeparatedEpochCredential');
    const fields=(info,file,name)=>info.output.contracts[file][name].storageLayout.storage.map(s=>[s.label,s.slot,s.offset]);
    expect(fields(next,'contracts/credentials/SeparatedEpochCredential.sol','SeparatedEpochCredential')).to.deep.equal(fields(old,'contracts/credentials/EpochCredential.sol','EpochCredential'));
    for(const name of ['SeparatedStaking','SeparatedChainTallyReader','SeparatedEpochCredential'])expect(ethers.getBytes((await artifacts.readArtifact(name)).deployedBytecode).length).to.be.at.most(24576);
  });
});
