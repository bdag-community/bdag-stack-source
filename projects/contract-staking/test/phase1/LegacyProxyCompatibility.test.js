const { expect } = require('chai');
const { ethers, network } = require('hardhat');
const { canonicalProxyCreation } = require('./fixtures/canonical-proxy.cjs');

const PROXY = '0x1000000000000000000000000000000000000005';
const IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const ADMIN = '0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103';

describe('Exact canonical genesis proxy compatibility', function () {
  it('allows the proposed DAO admin to call implementation functions', async function () {
    const [admin, caller] = await ethers.getSigners();
    const creation = ethers.getBytes(canonicalProxyCreation());
    const delimiter = creation.findIndex((byte, i) => byte === 0xf3 && creation[i + 1] === 0xfe);
    expect(delimiter).to.be.greaterThan(0);
    const runtime = ethers.hexlify(creation.slice(delimiter + 2));
    const target = await ethers.deployContract('GovernanceTargetFixture');
    await network.provider.send('hardhat_setCode', [PROXY, runtime]);
    await network.provider.send('hardhat_setStorageAt', [PROXY, IMPL, ethers.zeroPadValue(target.target, 32)]);
    await network.provider.send('hardhat_setStorageAt', [PROXY, ADMIN, ethers.zeroPadValue(admin.address, 32)]);
    expect(await target.attach(PROXY).connect(caller).value()).to.equal(0);
    expect(await target.attach(PROXY).connect(admin).value()).to.equal(0);
  });
});
