const { expect } = require('chai');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const hre = require('hardhat');
const helperPath = path.join(__dirname, '../scripts/release/mainnet-multisig.cjs');
const available = fs.existsSync(helperPath);

describe('Mainnet 3-of-5 multisig preparation boundary', function () {
  it('provides a pinned offline configuration and signature tool without a broadcast function', function () {
    expect(fs.existsSync(helperPath), 'missing offline multisig qualification tool').to.equal(true);
    const m = require(helperPath);
    expect(m.broadcast).to.equal(undefined);
    expect(m.submit).to.equal(undefined);
  });
});

(available ? describe : describe.skip)('Pinned Safe 3-of-5 on isolated Mainnet-ID London EVM', function () {
  const m = available ? require(helperPath) : {};
  let snapshot, deployer, owners, outsider, safe, singleton, factory;
  const supplied = ['0xA5968191D122aF96fD7C43008a121A653536B86e', '0x754cBD6A7f2B3485a00066422166eC63112bC6a1', '0xE39302970855E5A7Af5790d7B3624c830e1aCE5f', '0xFE248EdDAF92285551DB802661334E9702980752', '0x1F4040F2bFDCdeD94f2d8f27Fcd930b6C67Ee5F1'];
  const { ethers, network } = hre;
  async function deploy(ownerAddresses, salt = 1n) {
    const initializer = m.setupData(ownerAddresses);
    const address = await factory.createProxyWithNonce.staticCall(singleton.target, initializer, salt);
    await (await factory.createProxyWithNonce(singleton.target, initializer, salt)).wait();
    return new ethers.Contract(address, m.artifacts().Safe.abi, deployer);
  }
  function packet(overrides = {}, wallet = safe.target, chainId = 1404) {
    return m.transactionPacket({ chainId, safeAddress: wallet, transaction: { to: outsider.address, value: '1', data: '0x', operation: 0, safeTxGas: '0', baseGas: '0', gasPrice: '0', gasToken: ethers.ZeroAddress, refundReceiver: ethers.ZeroAddress, nonce: '0', ...overrides } });
  }
  async function signatures(p, selected = owners.slice(0, 3)) {
    const sigs = await Promise.all(selected.map(s => s.signTypedData(p.domain, p.types, p.message)));
    return m.assembleSignatures(p, sigs, owners.map(s => s.address));
  }
  async function exec(p, sigs, signer = deployer) {
    const t = p.message;
    return safe.connect(signer).execTransaction(t.to, t.value, t.data, t.operation, t.safeTxGas, t.baseGas, t.gasPrice, t.gasToken, t.refundReceiver, sigs);
  }
  beforeEach(async function () {
    snapshot = await network.provider.send('evm_snapshot');
    assert.equal(network.name, 'hardhat');
    assert.equal((await ethers.provider.getNetwork()).chainId, 1404n);
    [deployer, ...owners] = await ethers.getSigners(); owners = owners.slice(0, 5);
    outsider = (await ethers.getSigners())[8];
    const a = m.artifacts();
    singleton = await new ethers.ContractFactory(a.Safe.abi, a.Safe.bytecode, deployer).deploy(); await singleton.waitForDeployment();
    factory = await new ethers.ContractFactory(a.SafeProxyFactory.abi, a.SafeProxyFactory.bytecode, deployer).deploy(); await factory.waitForDeployment();
    safe = await deploy(owners.map(s => s.address));
    await deployer.sendTransaction({ to: safe.target, value: ethers.parseEther('2') });
  });
  afterEach(async function () { await network.provider.send('evm_revert', [snapshot]); });
  it('atomically initializes the exact five supplied addresses and threshold without claiming their signatures', async function () {
    const candidate = await deploy(supplied, 2n);
    expect(await candidate.getOwners()).to.deep.equal(supplied);
    expect(await candidate.getThreshold()).to.equal(3n);
    expect(await candidate.VERSION()).to.equal('1.5.0');
    expect((await candidate.getModulesPaginated('0x0000000000000000000000000000000000000001', 10))[0]).to.deep.equal([]);
    expect(await candidate.getStorageAt(BigInt(ethers.id('fallback_manager.handler.address')), 1)).to.equal(ethers.ZeroHash);
    expect(await candidate.getStorageAt(BigInt(ethers.id('guard_manager.guard.address')), 1)).to.equal(ethers.ZeroHash);
    await expect(candidate.setup(supplied, 3, ethers.ZeroAddress, '0x', ethers.ZeroAddress, ethers.ZeroAddress, 0, ethers.ZeroAddress)).to.be.revertedWith('GS200');
    expect(() => m.assembleSignatures(packet({}, candidate.target), [], supplied)).to.throw('three');
  });
  it('pins untouched upstream creation/runtime bytes and respects EIP-170', async function () {
    const a = m.artifacts();
    expect(ethers.keccak256(await ethers.provider.getCode(singleton.target))).to.equal(a.pins.Safe.runtimeHash);
    expect((await ethers.provider.getCode(singleton.target)).length / 2 - 1).to.be.at.most(24576);
    expect(ethers.keccak256(await ethers.provider.getCode(factory.target))).to.equal(a.pins.SafeProxyFactory.runtimeHash);
    expect(await singleton.getThreshold()).to.equal(1n);
    await expect(singleton.setup(supplied, 3, ethers.ZeroAddress, '0x', ethers.ZeroAddress, ethers.ZeroAddress, 0, ethers.ZeroAddress)).to.be.revertedWith('GS200');
  });
  it('rejects malformed, zero, sentinel, duplicate and non-five owner sets', function () {
    const addresses = owners.map(s => s.address);
    for (const list of [addresses.slice(0, 4), [...addresses, outsider.address], [ethers.ZeroAddress, ...addresses.slice(1)], ['0x0000000000000000000000000000000000000001', ...addresses.slice(1)], [addresses[1], ...addresses.slice(1)], ['0x1234', ...addresses.slice(1)]]) expect(() => m.setupData(list)).to.throw();
  });
  it('rejects a real two-owner signature set at the contract boundary', async function () {
    const p = packet();
    const two = await Promise.all(owners.slice(0, 2).map(async s => ({ address: s.address, signature: await s.signTypedData(p.domain, p.types, p.message) })));
    const raw = ethers.concat(two.sort((a, b) => a.address.toLowerCase().localeCompare(b.address.toLowerCase())).map(s => s.signature));
    await expect(exec(p, raw)).to.be.revertedWith('GS020');
    expect(await safe.nonce()).to.equal(0n);
  });
  it('executes every one of the ten three-owner combinations using real lab signatures', async function () {
    let nonce = 0;
    for (let i = 0; i < 5; i++) for (let j = i + 1; j < 5; j++) for (let k = j + 1; k < 5; k++) {
      const p = packet({ nonce: String(nonce) });
      const sig = await signatures(p, [owners[k], owners[i], owners[j]]);
      const digest = ethers.TypedDataEncoder.hash(p.domain, p.types, p.message);
      await expect(exec(p, sig)).to.emit(safe, 'ExecutionSuccess').withArgs(digest, 0);
      nonce++;
    }
    expect(await safe.nonce()).to.equal(10n);
  });
  it('rejects duplicate and non-owner signatures without counting them toward the quorum', async function () {
    const p = packet(); const a = await owners[0].signTypedData(p.domain, p.types, p.message);
    const b = await owners[1].signTypedData(p.domain, p.types, p.message);
    expect(() => m.assembleSignatures(p, [a, a, b], owners.map(s => s.address))).to.throw('duplicate');
    const other = await outsider.signTypedData(p.domain, p.types, p.message);
    expect(() => m.assembleSignatures(p, [a, b, other], owners.map(s => s.address))).to.throw('owner');
  });
  it('rejects nonce replay and recipient/value/calldata tampering', async function () {
    const p = packet(), sig = await signatures(p);
    for (const altered of [packet({ to: owners[4].address }), packet({ value: '2' }), packet({ data: '0x1234' })]) await expect(exec(altered, sig)).to.be.reverted;
    const wrongNonce = packet({ nonce: '1' });
    await expect(exec(wrongNonce, await signatures(wrongNonce))).to.be.reverted;
    await (await exec(p, sig)).wait();
    await expect(exec(p, sig)).to.be.reverted;
    expect(await safe.nonce()).to.equal(1n);
  });
  it('rejects signatures for a different chain ID or Safe address', async function () {
    for (const p of [packet({}, safe.target, 1), packet({}, factory.target, 1404)]) {
      const sig = await signatures(p);
      await expect(exec(packet(), sig)).to.be.reverted;
    }
    expect(await safe.nonce()).to.equal(0n);
  });
  it('allows signer replacement only through a three-owner Safe call and retains threshold three', async function () {
    const current = [...await safe.getOwners()]; const old = current[2]; const previous = current[1];
    const data = safe.interface.encodeFunctionData('swapOwner', [previous, old, outsider.address]);
    await expect(safe.connect(outsider).swapOwner(previous, old, outsider.address)).to.be.revertedWith('GS031');
    const p = packet({ to: safe.target, value: '0', data });
    await (await exec(p, await signatures(p))).wait();
    expect(await safe.isOwner(old)).to.equal(false);
    expect(await safe.isOwner(outsider.address)).to.equal(true);
    expect(await safe.getThreshold()).to.equal(3n);
    expect((await safe.getOwners()).length).to.equal(5);
  });
  it('does not consume a nonce on a failed execution and can execute a corrected quorum-authorized call', async function () {
    const bad = packet({ value: ethers.parseEther('3').toString() });
    await expect(exec(bad, await signatures(bad))).to.be.reverted;
    expect(await safe.nonce()).to.equal(0n);
    const good = packet(); await (await exec(good, await signatures(good))).wait();
    expect(await safe.nonce()).to.equal(1n);
  });
  it('keeps unsigned preparation restricted to plain calls with no fee refund or hidden delegatecall setup', function () {
    expect(() => packet({ operation: 1 })).to.throw('plain CALL');
    expect(() => packet({ gasPrice: '1' })).to.throw('refund');
    expect(() => packet({ gasToken: outsider.address })).to.throw('refund');
    expect(() => packet({ refundReceiver: outsider.address })).to.throw('refund');
  });
});
