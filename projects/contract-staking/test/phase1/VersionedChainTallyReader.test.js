const { expect } = require('chai');
const { ethers, network, artifacts } = require('hardhat');
const { time } = require('@nomicfoundation/hardhat-network-helpers');

const set = (address, slot, value) => network.provider.send('hardhat_setStorageAt', [address, ethers.toQuantity(slot), ethers.toBeHex(value, 32)]);
const base = BigInt(ethers.keccak256(ethers.toBeHex(8, 32)));
const fqn = 'contracts/credentials/VersionedChainTallyReader.sol:VersionedChainTallyReader';

describe('Versioned native reward clock reader', function () {
  let reader, genesis;
  beforeEach(async () => {
    const source = await ethers.deployContract('CredentialVotingSource');
    reader = await ethers.deployContract('VersionedChainTallyReader', [source.target]);
    genesis = await source.genesisTime();
  });
  async function install() {
    // Simulate consensus-owned writes, never a public contract initializer.
    await set(reader.target, 8n, 2n);
    for (const [i, value] of [0n, genesis, 60n, 10n, genesis + 600n, 1800n].entries()) {
      await set(reader.target, base + BigInt(i), value);
    }
  }
  it('appends an unpacked three-word schedule at slot 8 without moving native reward storage', async () => {
    const info = await artifacts.getBuildInfo(fqn);
    const layout = info.output.contracts['contracts/credentials/VersionedChainTallyReader.sol'].VersionedChainTallyReader.storageLayout;
    expect(layout.storage.map(s => [s.label, s.slot])).to.deep.equal([
      ['engineReserved','0'], ['rewardEpoch','4'], ['rewardBootstrapEpochPlusOne','5'],
      ['firstCompleteRewardEpochPlusOne','6'], ['rewardTotals','7'], ['_epochSchedule','8'], ['_governanceTimeFloor','9'],
    ]);
    const array = layout.types[layout.storage.find(s => s.label === '_epochSchedule').type];
    const segment = layout.types[array.base];
    expect(segment.numberOfBytes).to.equal('96');
    expect(segment.members.map(s => [s.label,s.slot,s.offset])).to.deep.equal([
      ['firstEpoch','0',0], ['startTime','1',0], ['duration','2',0],
    ]);
  });
  it('reads epoch bounds from native slots without recursion into staking', async () => {
    await install();
    await set(reader.target, 4n, 11n);
    const status = await reader.epochStatus();
    expect([...status]).to.deep.equal([11n,2n,genesis+2400n,genesis+4200n,1800n]);
    expect(await reader.currentEpoch()).to.equal(11n);
    expect(await reader.genesisTime()).to.equal(genesis);
    expect([...(await reader.epochBounds(9))]).to.deep.equal([9n,1n,genesis+540n,genesis+600n,60n]);
    await time.increaseTo(genesis + 4200n);
    expect(await reader.currentEpoch()).to.equal(12n);
  });
  it('fails closed for absent, oversized or malformed native schedule storage', async () => {
    await expect(reader.epochStatus()).to.be.reverted;
    await set(reader.target, 8n, 65n);
    await expect(reader.epochStatus()).to.be.reverted;
    await install();
    await set(reader.target, base + 4n, genesis + 601n);
    await expect(reader.epochStatus()).to.be.reverted;
  });
  it('retains a monotonic timestamp clock for proposal deadlines independently of epoch length', async () => {
    await install();
    await set(reader.target, 9n, genesis + 777n);
    expect(await reader.governanceTime()).to.equal(genesis + 777n);
    await set(reader.target, 4n, 11n);
    expect(await reader.governanceTime()).to.equal(genesis + 2400n);
    await time.increaseTo(genesis + 2500n);
    expect(await reader.governanceTime()).to.equal(genesis + 2500n);
    await set(reader.target, 9n, 2n**64n);
    await expect(reader.governanceTime()).to.be.reverted;
  });
  it('looks up historical timestamp bounds without applying the current voting floor', async () => {
    await install();
    await set(reader.target, 4n, 20n);
    expect([...(await reader.epochAtTime(genesis + 599n))]).to.deep.equal([9n,1n,genesis+540n,genesis+600n,60n]);
    expect([...(await reader.epochAtTime(genesis + 600n))]).to.deep.equal([10n,2n,genesis+600n,genesis+2400n,1800n]);
    expect(await reader.currentEpoch()).to.equal(20n);
  });
  it('retains historical financial fields and finalized rewards and has no wallet-writable entrypoints', async () => {
    await install();
    await set(reader.target, 0n, 3n);
    await set(reader.target, 4n, 11n);
    await set(reader.target, 5n, 1n);
    await set(reader.target, 6n, 2n);
    const totalSlot = ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode(['uint256','uint256'], [9,7]));
    await set(reader.target, BigInt(totalSlot), 12345n);
    expect(await reader.engineEpoch()).to.equal(3n);
    expect(await reader.totalAt(9)).to.equal(12345n);
    const writes = reader.interface.fragments.filter(f => f.type === 'function' && !['view','pure'].includes(f.stateMutability));
    expect(writes).to.deep.equal([]);
  });
});
