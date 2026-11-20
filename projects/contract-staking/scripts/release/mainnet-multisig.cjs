// Pure offline Safe preparation. No provider, signer key loading or broadcasting.
const assert = require('node:assert/strict');
const { Interface, getAddress, ZeroAddress, keccak256, verifyTypedData, concat } = require('ethers');
const version = '1.5.0';
const pins = {
  Safe: { source: 'Safe.sol/Safe.json', creationHash: '0x3c32e7eb3961c145eef8620b24e8b829e6d2062a5730629d6169319f29fc1d07', runtimeHash: '0xdda019cbd7c867a533a2a86e5c53434fdc50b13122b5a5ddb4a8df61b31c20f2' },
  SafeProxy: { source: 'proxies/SafeProxy.sol/SafeProxy.json', creationHash: '0x941b3e88811b2f33b8e26783c7407d2e978404581a21c9f07abf2cad9cb87e12', runtimeHash: '0x4e381985ca68b3e5d27b4425fa581c19cf33146d3f887a3cfca96f55528ea46f' },
  SafeProxyFactory: { source: 'proxies/SafeProxyFactory.sol/SafeProxyFactory.json', creationHash: '0xd8803696e4c627ba4cab33cfab2625cf66c88f218c017306e392fcf88cc4e0c6', runtimeHash: '0x967dae4cda22b0c9ef7f31b010bdc1ceb0af9904b0c3dc060b5302e4c18a4529' },
};
function artifacts() {
  assert.equal(require('@safe-global/safe-smart-account/package.json').version, version, 'unreviewed Safe package');
  const out = { pins, version };
  for (const [name, pin] of Object.entries(pins)) {
    const a = require('@safe-global/safe-smart-account/build/artifacts/contracts/' + pin.source);
    assert.equal(keccak256(a.bytecode), pin.creationHash, name + ' creation mismatch');
    assert.equal(keccak256(a.deployedBytecode), pin.runtimeHash, name + ' runtime mismatch');
    assert((a.deployedBytecode.length - 2) / 2 <= 24576, name + ' EIP-170 limit');
    out[name] = a;
  }
  return out;
}
function owners(input) {
  assert(Array.isArray(input) && input.length === 5, 'exactly five owners required');
  const normalized = input.map(getAddress);
  assert(normalized.every(a => a !== ZeroAddress && a.toLowerCase() !== '0x0000000000000000000000000000000000000001'), 'zero/sentinel owner');
  assert.equal(new Set(normalized.map(a => a.toLowerCase())).size, 5, 'duplicate owner');
  return normalized;
}
function setupData(input) {
  return new Interface(artifacts().Safe.abi).encodeFunctionData('setup', [owners(input), 3, ZeroAddress, '0x', ZeroAddress, ZeroAddress, 0, ZeroAddress]);
}
const types = { SafeTx: [
  { name: 'to', type: 'address' }, { name: 'value', type: 'uint256' }, { name: 'data', type: 'bytes' }, { name: 'operation', type: 'uint8' },
  { name: 'safeTxGas', type: 'uint256' }, { name: 'baseGas', type: 'uint256' }, { name: 'gasPrice', type: 'uint256' },
  { name: 'gasToken', type: 'address' }, { name: 'refundReceiver', type: 'address' }, { name: 'nonce', type: 'uint256' },
] };
function transactionPacket({ chainId, safeAddress, transaction }) {
  assert(Number.isSafeInteger(chainId) && chainId > 0, 'invalid chain ID');
  const message = { ...transaction, to: getAddress(transaction.to), gasToken: getAddress(transaction.gasToken), refundReceiver: getAddress(transaction.refundReceiver) };
  assert.equal(message.operation, 0, 'only a plain CALL may be prepared');
  assert(BigInt(message.gasPrice) === 0n && message.gasToken === ZeroAddress && message.refundReceiver === ZeroAddress, 'Safe refund payments disabled');
  for (const key of ['value', 'safeTxGas', 'baseGas', 'gasPrice', 'nonce']) {
    assert(/^(0|[1-9][0-9]*)$/.test(String(message[key])) && BigInt(message[key]) < 2n ** 256n, 'invalid uint256 ' + key);
    message[key] = String(message[key]);
  }
  assert(/^0x(?:[0-9a-fA-F]{2})*$/.test(message.data), 'invalid call bytes');
  return { domain: { chainId, verifyingContract: getAddress(safeAddress) }, types, primaryType: 'SafeTx', message };
}
function assembleSignatures(packet, signatures, inputOwners) {
  const allowed = new Set(owners(inputOwners).map(a => a.toLowerCase()));
  assert(signatures.length >= 3 && signatures.length <= 5, 'three to five distinct signatures required');
  const seen = new Set();
  const rows = signatures.map(signature => {
    assert(/^0x[0-9a-fA-F]{130}$/.test(signature), '65-byte ECDSA signature required');
    const owner = verifyTypedData(packet.domain, packet.types, packet.message, signature).toLowerCase();
    assert(allowed.has(owner), 'signature is not from an owner');
    assert(!seen.has(owner), 'duplicate owner signature'); seen.add(owner);
    return { owner, signature };
  });
  return concat(rows.sort((a, b) => a.owner.localeCompare(b.owner)).map(s => s.signature));
}
module.exports = { artifacts, owners, setupData, transactionPacket, assembleSignatures };
