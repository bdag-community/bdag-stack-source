const { expect } = require('chai');
const { ethers, network, artifacts } = require('hardhat');
const { time } = require('@nomicfoundation/hardhat-network-helpers');

const READER = '0x1000000000000000000000000000000000000002';
const IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const coder = ethers.AbiCoder.defaultAbiCoder();
const UNIT = ethers.parseEther('1');
const word = value => ethers.toBeHex(value, 32);
const set = (address, slot, value) => network.provider.send('hardhat_setStorageAt', [address, ethers.toQuantity(slot), word(value)]);
const map = (key, slot, type = 'uint256') => BigInt(ethers.keccak256(coder.encode([type,'uint256'], [key,slot])));
const nested = (address, epoch, slot) => map(epoch, map(address, slot, 'address'));
const domain = ethers.keccak256(ethers.toUtf8Bytes('bdag.reward.voting.v1'));
const minerSlot = (address, epoch) => BigInt(ethers.keccak256(coder.encode(['address','bytes32'], [address, ethers.keccak256(coder.encode(['bytes32','uint256'], [domain,epoch]))])));

describe('Candidate credentials retain voting history across clock changes', function () {
  let snapshot, alice, pool, delegatee, staking, mining, stakers, genesis;
  beforeEach(async () => {
    snapshot = await network.provider.send('evm_snapshot');
    [,alice,pool,delegatee] = await ethers.getSigners();
    await network.provider.send('hardhat_setCode', [READER, '0x']);
    const oldStaking = await ethers.deployContract('StakingContractV2Testnet');
    const proxy = await ethers.deployContract('UUPSProxy', [oldStaking.target, oldStaking.interface.encodeFunctionData('initialize')]);
    staking = oldStaking.attach(proxy.target);
    genesis = await staking.genesisTime();
    await staking.connect(alice).stake(alice.address, UNIT, pool.address, {value:UNIT});
    const reader = await ethers.deployContract('ChainTallyReader', [staking.target]);
    await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(reader.target)]);
    await set(READER, 4n, 2n);
    await set(READER, 5n, 1n);
    await set(READER, 6n, 2n);
    await rewards(1n);
    stakers = await ethers.deployContract('EpochCredential', [staking.target, ethers.ZeroAddress, 'Staking Vote', 'sVOTE']);
    mining = await ethers.deployContract('EpochCredential', [staking.target, READER, 'Mining Vote', 'mVOTE']);
    await time.increaseTo(genesis + 120n);
  });
  afterEach(async () => { await network.provider.send('evm_revert', [snapshot]); });

  async function rewards(epoch) {
    await set(staking.target, nested(pool.address, epoch, 34n), 5n);
    await set(staking.target, map(epoch, 36n), 5n * UNIT);
    await set(READER, map(epoch, 7n), 7n * UNIT);
    await set(READER, minerSlot(alice.address, epoch), 7n * UNIT);
  }
  async function install() {
    const reader = await ethers.deployContract('VersionedChainTallyReader', [staking.target]);
    await network.provider.send('hardhat_setCode', [READER, await ethers.provider.getCode(reader.target)]);
    await set(READER, 8n, 2n);
    const base = BigInt(ethers.keccak256(word(8)));
    for (const [i,value] of [0n,genesis,60n,10n,genesis+600n,1800n].entries()) await set(READER, base + BigInt(i), value);
    const [governor] = await ethers.getSigners();
    const timing = await ethers.deployContract('VersionedGovernanceTiming', [governor.address, READER, 60, 259200, 604800,
      { ordinaryNumerator: 3, ordinaryDenominator: 7, criticalEpochs: 1, graceEpochs: 2 }]);
    const impl = await ethers.deployContract('VersionedTestnetStaking', [timing.target]);
    await set(staking.target, BigInt(IMPL), BigInt(impl.target));
    staking = impl.attach(staking.target);
    for (const [credential,source,name,symbol] of [[stakers,ethers.ZeroAddress,'Staking Vote','sVOTE'],[mining,READER,'Mining Vote','mVOTE']]) {
      const candidate = await ethers.deployContract('VersionedEpochCredential', [staking.target,source,name,symbol]);
      // Isolated runtime replacement only, preserving storage at the original
      // credential address. No such public mutator exists in either contract.
      await network.provider.send('hardhat_setCode', [credential.target, await ethers.provider.getCode(candidate.target)]);
    }
  }

  it('preserves receipts, raw historical rewards, pool registration and delegation checkpoints', async () => {
    await stakers.claimFor(alice.address, 2n, [pool.address]);
    await mining.claimFor(alice.address, 2n, []);
    await stakers.registerPools(alice.address, [pool.address]);
    await stakers.connect(alice).delegate(delegatee.address);
    const nonce = await stakers.nonces(alice.address);
    await install();
    await time.increaseTo(genesis + 601n);
    await set(READER, 4n, 10n);
    expect(await stakers.rawWeightOf(alice.address, 2n, [pool.address])).to.equal(5n * UNIT);
    expect(await mining.rawWeightOf(alice.address, 2n, [])).to.equal(7n * UNIT);
    expect(await stakers['balanceOf(address,uint256)'](alice.address, 2n)).to.equal(5n * UNIT);
    expect(await mining['balanceOf(address,uint256)'](alice.address, 2n)).to.equal(7n * UNIT);
    expect(await stakers.poolsOf(alice.address)).to.deep.equal([pool.address]);
    expect(await stakers.delegateAt(alice.address, 2n)).to.equal(alice.address);
    expect(await stakers.delegateAt(alice.address, 3n)).to.equal(delegatee.address);
    expect(await stakers.nonces(alice.address)).to.equal(nonce);
    await expect(stakers.claimFor(alice.address, 2n, [pool.address])).to.be.revertedWithCustomError(stakers, 'ClaimWindowClosed');
  });

  it('uses each complete earned epoch in the next voting epoch without a claim', async () => {
    await install();
    await rewards(10n);
    await time.increaseTo(genesis + 2400n);
    await set(READER, 4n, 11n);
    for (const credential of [stakers,mining]) {
      expect(await credential.currentEpoch()).to.equal(11n);
      expect(await credential.epochDuration()).to.equal(1800n);
      expect(await credential.votingReady()).to.equal(true);
      expect(await credential['balanceOf(address,uint256)'](alice.address, 11n)).to.equal(0n);
    }
    expect(await stakers.rawWeightOf(alice.address, 11n, [pool.address])).to.equal(5n * UNIT);
    expect(await mining.rawWeightOf(alice.address, 11n, [])).to.equal(7n * UNIT);
    expect(await stakers.totalSupplyAt(11n)).to.equal(5n * UNIT);
    expect(await mining.totalSupplyAt(11n)).to.equal(7n * UNIT);
  });

  it('schedules delegation by the next epoch number, not the retired 60-second clock', async () => {
    await install();
    await time.increaseTo(genesis + 2400n);
    await set(READER, 4n, 11n);
    await rewards(11n);
    await stakers.connect(alice).delegate(delegatee.address);
    expect(await stakers.delegateAt(alice.address, 11n)).to.equal(alice.address);
    expect(await stakers.delegateAt(alice.address, 12n)).to.equal(delegatee.address);
    await time.increaseTo(genesis + 4200n);
    await set(READER, 4n, 12n);
    const params = coder.encode(['address[]','address[][]'], [[alice.address],[[pool.address]]]);
    expect(await stakers.getVotes(delegatee.address, 12n, params)).to.equal(5n * UNIT);
    expect(await stakers.getVotes(alice.address, 12n, '0x')).to.equal(0n);
  });

  it('fails closed when the installed schedule disappears or disagrees with staking genesis', async () => {
    await install();
    await set(READER, 8n, 0n);
    await expect(stakers.currentEpoch()).to.be.reverted;
    await install();
    await set(staking.target, 8n, genesis + 1n);
    await expect(stakers.votingReady()).to.be.reverted;
  });

  it('preserves the signing domain and nonce while enforcing absolute signature expiry', async () => {
    const domainBefore = await stakers.eip712Domain();
    const signingDomain = {name:'Staking Vote',version:'1',chainId:(await ethers.provider.getNetwork()).chainId,verifyingContract:stakers.target};
    const types = {Delegation:[{name:'account',type:'address'},{name:'delegatee',type:'address'},{name:'nonce',type:'uint256'},{name:'deadline',type:'uint256'}]};
    const message = {account:alice.address,delegatee:delegatee.address,nonce:0n,deadline:genesis + 5000n};
    const valid = await alice.signTypedData(signingDomain, types, message);
    const expiredMessage = {...message,deadline:genesis + 500n};
    const expired = await alice.signTypedData(signingDomain, types, expiredMessage);
    await install();
    await time.increaseTo(genesis + 601n);
    await set(READER, 4n, 10n);
    expect(await stakers.eip712Domain()).to.deep.equal(domainBefore);
    await expect(stakers.delegateBySig(alice.address, delegatee.address, 0n, expiredMessage.deadline, expired))
      .to.be.revertedWithCustomError(stakers, 'SignatureExpired');
    await stakers.delegateBySig(alice.address, delegatee.address, 0n, message.deadline, valid);
    expect(await stakers.nonces(alice.address)).to.equal(1n);
    expect(await stakers.delegateAt(alice.address, 10n)).to.equal(alice.address);
    expect(await stakers.delegateAt(alice.address, 11n)).to.equal(delegatee.address);
    await expect(stakers.delegateBySig(alice.address, delegatee.address, 0n, message.deadline, valid))
      .to.be.revertedWithCustomError(stakers, 'InvalidNonce');
  });

  it('keeps the existing storage schema and stays within the London runtime size limit', async () => {
    await install();
    const old = await artifacts.getBuildInfo('contracts/credentials/EpochCredential.sol:EpochCredential');
    const fresh = await artifacts.getBuildInfo('contracts/credentials/VersionedEpochCredential.sol:VersionedEpochCredential');
    const a = old.output.contracts['contracts/credentials/EpochCredential.sol'].EpochCredential.storageLayout;
    const b = fresh.output.contracts['contracts/credentials/VersionedEpochCredential.sol'].VersionedEpochCredential.storageLayout;
    expect(b.storage.map(s => [s.label,s.slot,s.offset])).to.deep.equal(a.storage.map(s => [s.label,s.slot,s.offset]));
    const artifact = await artifacts.readArtifact('VersionedEpochCredential');
    const code = ethers.getBytes(artifact.deployedBytecode);
    expect(code.length).to.be.lessThan(24577);
    for (let i = 0; i < code.length; i++) {
      const op = code[i]; if (op === 0xfe) break;
      expect(op).not.to.equal(0x5f); expect(op).not.to.equal(0x5e);
      if (op >= 0x60 && op <= 0x7f) i += op - 0x5f;
    }
  });
});
