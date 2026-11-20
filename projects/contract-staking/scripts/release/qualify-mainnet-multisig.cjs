// Local configuration proof only. Never connects to a real network or imports signer keys.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const hre = require('hardhat');
const m = require('./mainnet-multisig.cjs');
async function qualify() {
  assert.equal(hre.network.name, 'hardhat', 'only an in-process isolated EVM is allowed');
  assert.equal((await hre.ethers.provider.getNetwork()).chainId, 1404n);
  const inputRaw = fs.readFileSync(path.join(__dirname, '../../release/mainnet/authority-inputs.json'));
  const input = JSON.parse(inputRaw);
  assert.equal(input.chainId, 1404); assert.equal(input.threshold, 3); assert.equal(input.deploymentAuthorized, false);
  const ownerList = m.owners(input.signers.map(s => s.address));
  const a = m.artifacts(); const [labDeployer] = await hre.ethers.getSigners();
  const singleton = await new hre.ethers.ContractFactory(a.Safe.abi, a.Safe.bytecode, labDeployer).deploy(); await singleton.waitForDeployment();
  const factory = await new hre.ethers.ContractFactory(a.SafeProxyFactory.abi, a.SafeProxyFactory.bytecode, labDeployer).deploy(); await factory.waitForDeployment();
  const initializer = m.setupData(ownerList);
  const salt = hre.ethers.keccak256(hre.ethers.AbiCoder.defaultAbiCoder().encode(['string', 'bytes32', 'address[]', 'uint256'], ['BDAG Mainnet human admin wallet v1', input.genesisHash, ownerList, 3]));
  const localAddress = await factory.createProxyWithNonce.staticCall(singleton.target, initializer, BigInt(salt));
  await (await factory.createProxyWithNonce(singleton.target, initializer, BigInt(salt))).wait();
  const safe = new hre.ethers.Contract(localAddress, a.Safe.abi, labDeployer);
  assert.deepEqual([...await safe.getOwners()], ownerList); assert.equal(await safe.getThreshold(), 3n);
  assert.equal(hre.ethers.keccak256(await hre.ethers.provider.getCode(singleton.target)), a.pins.Safe.runtimeHash);
  assert.equal(hre.ethers.keccak256(await hre.ethers.provider.getCode(factory.target)), a.pins.SafeProxyFactory.runtimeHash);
  assert.equal(hre.ethers.keccak256(await hre.ethers.provider.getCode(localAddress)), a.pins.SafeProxy.runtimeHash);
  return {
    schema: 'bdag.mainnet.multisig-local-qualification.v1', package: '@safe-global/safe-smart-account', version: a.version,
    packageGitCommit: 'dc437e8fba8b4805d76bcbd1c668c9fd3d1e83be', license: 'LGPL-3.0',
    packageIntegrity: 'sha512-VIMWxoeY/kNuZVsQVvAeOFKcQS3eojCXg0ecGnesBSLn5ypYshmBhVEEU5XiurJJGTs/MrcoLTbTxhs9W9GvdQ==',
    authorityInputSha256: crypto.createHash('sha256').update(inputRaw).digest('hex'), chainId: 1404, owners: ownerList, threshold: 3,
    initializer, saltNonce: salt, setupDelegatecall: false, fallbackHandler: hre.ethers.ZeroAddress, modules: [], guard: hre.ethers.ZeroAddress, setupPayment: '0',
    artifactPins: a.pins, localConfigurationProof: { singleton: singleton.target, factory: factory.target, wallet: localAddress, threshold: Number(await safe.getThreshold()) },
    realSignerControlProven: false, productionWalletAddress: null, productionRoleAssignments: [], deploymentAuthorized: false,
    extraMultisigApprovalOfDAOActions: false,
    limitations: ['Configuration tested locally with supplied public owner addresses; real-signature threshold/replay tests use lab keys only.', 'Local deployment addresses are not approved production addresses. Verify actual deployment code, initializer, owners and threshold before any approved role binding.', 'Safe EIP-712 binds chain ID, wallet and nonce; identical-chain-ID fork replay must also be managed by verified canonical RPC/history and deployment context.', 'Owner/threshold/module changes are themselves governed by the current quorum; threshold is not claimed permanently immutable.'],
  };
}
if (require.main === module) qualify().then(result => {
  const out = process.env.MAINNET_MULTISIG_OUTPUT;
  assert(out, 'supply a local MAINNET_MULTISIG_OUTPUT path');
  fs.mkdirSync(path.dirname(path.resolve(out)), { recursive: true });
  fs.writeFileSync(out, JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify({ out, threshold: result.threshold, owners: result.owners.length, localOnly: true, realSignerControlProven: false, deploymentAuthorized: false }));
}).catch(error => { console.error(error.message); process.exitCode = 1; });
module.exports = { qualify };
