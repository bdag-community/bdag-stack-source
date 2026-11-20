// Whitelisted public reads only. Produces MPT proof input, never transactions.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { promisify } = require('node:util');
const exec = promisify(require('node:child_process').execFile);
const { keccak256, id, toBeHex } = require('ethers');
const providers = ['https://rpc.capedag.com/', 'https://rpc.bdagexplorer.com/'];
const input = require('../../test-mainnet/fixtures/canonical-input.json');
const allowed = new Set(['eth_chainId', 'eth_blockNumber', 'eth_getBlockByNumber', 'eth_getProof', 'eth_getCode']);
async function rpc(url, q) {
  assert(q.every(([m]) => allowed.has(m)), 'read-only RPC required');
  const { stdout } = await exec('curl', ['-fsS', '--max-time', '30', '-H', 'Content-Type: application/json', '--data', JSON.stringify(q.map(([method, params], i) => ({ jsonrpc: '2.0', id: i, method, params }))), url], { encoding: 'utf8', timeout: 33000, maxBuffer: 8 * 1024 * 1024 });
  const rows = JSON.parse(stdout); assert(Array.isArray(rows));
  return q.map((_, i) => { const r = rows.find(r => r.id === i); assert(r && !r.error, JSON.stringify(r?.error || 'missing result')); return r.result; });
}
async function main() {
  const [bundlePath, out] = process.argv.slice(2); assert(bundlePath && out, 'supply review bundle path and new output file'); assert(!fs.existsSync(out), 'existing witness retained');
  const bundle = JSON.parse(fs.readFileSync(bundlePath)); assert.equal(bundle.chainId, 1404);
  const identities = await Promise.all(providers.map(async url => {
    const [chain, head, genesis, baseline] = await rpc(url, [['eth_chainId', []], ['eth_blockNumber', []], ['eth_getBlockByNumber', ['0x0', false]], ['eth_getBlockByNumber', [input.baseline.number, false]]]);
    assert.equal(chain, '0x57c'); assert.equal(genesis.hash, input.genesis.hash); assert.equal(genesis.stateRoot, input.genesis.stateRoot); assert.equal(baseline.hash, input.baseline.hash); assert.equal(baseline.stateRoot, input.baseline.stateRoot);
    return { url, head: Number(BigInt(head)) };
  }));
  const height = Math.min(...identities.map(r => r.head)) - 128, tag = '0x' + height.toString(16);
  const headers = await Promise.all(providers.map(url => rpc(url, [['eth_getBlockByNumber', [tag, false]]]).then(r => r[0])));
  assert.equal(headers[0].hash, headers[1].hash); assert.equal(headers[0].stateRoot, headers[1].stateRoot);
  const word = n => toBeHex(n, 32), impl = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
  // Correct standard ERC-1967 admin word (derive it rather than trust a literal).
  const adminSlot = word(BigInt(id('eip1967.proxy.admin')) - 1n);
  const slots = new Map(); const add = (a, keys) => slots.set(a.toLowerCase(), keys);
  for (const a of bundle.accounts) add(a.address, []);
  add(input.legacy, [word(0), word(1), word(2), word(8), impl]); add(input.implementation, []);
  add(input.blocklist, [word(0)]); add(input.selective, [word(0)]); add(input.governance, [word(0), impl, adminSlot]);
  const base = BigInt(keccak256(word(8)));
  add('0x1000000000000000000000000000000000000002', [word(4), word(5), word(6), word(8), word(9), word(10), word(11), word(base), word(base + 1n), word(base + 2n),
    ...['bdag.mainnet.dao.staking.v2.activation', 'bdag.mainnet.dao.staking.v2.manifest', 'bdag.governance.v3.activation.manifest', 'bdag.testnet.timing.v1.activation', 'bdag.separate.staking.v1.activation'].map(id)]);
  const accounts = [];
  for (const [address, keys] of slots) {
    const [proof, code] = await rpc(providers[0], [['eth_getProof', [address, keys, tag]], ['eth_getCode', [address, tag]]]);
    // This RPC reports zero codeHash for absent accounts. The Go verifier must
    // prove non-inclusion before accepting that convention, never trust it for
    // an existing account or install arbitrary code under a mismatched hash.
    const absentConvention = code === '0x' && proof.codeHash === '0x' + '00'.repeat(32) && BigInt(proof.balance) === 0n && BigInt(proof.nonce) === 0n;
    assert(absentConvention || keccak256(code) === proof.codeHash, 'code/proof hash mismatch'); accounts.push({ address, proof, code });
  }
  const [after] = await rpc(providers[0], [['eth_getBlockByNumber', [tag, false]]]); assert.equal(after.hash, headers[0].hash);
  const result = { schema: 'bdag.mainnet.public-state-witness.v1', capturedAt: new Date().toISOString(), transactionsSent: 0, chainId: 1404, providers, header: headers[0], accounts,
    scope: 'Account/storage proofs anchored to a common public EVM root for isolated native-installer state preservation. Synthetic future gates only; not complete native/EVM history replay or consensus finality proof.' };
  fs.mkdirSync(path.dirname(path.resolve(out)), { recursive: true }); fs.writeFileSync(out, JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify({ out, block: height, hash: after.hash, stateRoot: after.stateRoot, accounts: accounts.length, transactionsSent: 0 }));
}
if (require.main === module) main().catch(e => { console.error(e.message); process.exitCode = 1; });
