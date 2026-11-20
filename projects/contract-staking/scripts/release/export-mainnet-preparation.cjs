// Constructor-bound REVIEW output, with the user's explicit reviewer identities.
// The compiled production profile/bundle are never written by this tool.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { buildMainnetMigrationBundle } = require('./mainnet-migration-bundle.cjs');
async function build(hre) {
  const raw = fs.readFileSync(path.join(__dirname, '../../release/mainnet/authority-inputs.json'));
  const authority = JSON.parse(raw);
  const input = require('../../test-mainnet/fixtures/canonical-input.json');
  assert.equal(authority.chainId, 1404); assert.equal(authority.threshold, 3); assert.equal(authority.deploymentAuthorized, false);
  assert.equal(authority.genesisHash, input.genesis.hash);
  assert.equal(authority.genesisStateRoot, input.genesis.stateRoot);
  const signers = require('./mainnet-multisig.cjs').owners(authority.signers.map(s => s.address));
  const reviewers = authority.reviewers.map(r => hre.ethers.getAddress(r.address));
  assert.equal(reviewers.length, 3); assert.equal(new Set(reviewers).size, 3);
  assert(reviewers.every(a => a !== hre.ethers.ZeroAddress), 'zero reviewer');
  const result = await buildMainnetMigrationBundle(hre, input, { reviewers });
  Object.assign(result.metadata, { authorityInputSha256: crypto.createHash('sha256').update(raw).digest('hex'),
    reviewerSelectionApprovedOn: authority.reviewerSelectionApprovedOn, reviewers, humanMultisigSigners: signers, humanMultisigThreshold: 3,
    humanMultisigAddress: null, realSignerControlProven: false, extraMultisigApprovalOfDAOActions: false });
  return result;
}
if (require.main === module) build(require('hardhat')).then(result => {
  const out = process.env.MAINNET_PREPARATION_OUTPUT;
  assert(out, 'supply a new local MAINNET_PREPARATION_OUTPUT directory');
  assert(!fs.existsSync(out), 'refusing to overwrite prior qualification output'); fs.mkdirSync(out, { recursive: true });
  const raw = JSON.stringify(result.bundle, null, 2) + '\n'; fs.writeFileSync(path.join(out, 'bundle.json'), raw);
  const profiles = require('./mainnet-platform-profile.cjs');
  const profile = profiles.platformProfile(result);
  fs.writeFileSync(path.join(out, 'platform-profile.review.json'), JSON.stringify(profile));
  fs.writeFileSync(path.join(out, 'platform-environments.review.json'), JSON.stringify(profiles.environmentTemplates(profile), null, 2) + '\n');
  fs.writeFileSync(path.join(out, 'context.json'), JSON.stringify({ metadata: result.metadata, genesisHeader: result.genesisHeader, baselineHeader: result.baselineHeader, prestate: require('../../test-mainnet/fixtures/canonical-input.json') }, null, 2) + '\n');
  console.log(JSON.stringify({ out, sha256: crypto.createHash('sha256').update(raw).digest('hex'), accounts: result.bundle.accounts.length, reviewers: result.metadata.reviewers.length, reviewOnly: true, installationAuthorized: false }));
}).catch(error => { console.error(error.message); process.exitCode = 1; });
module.exports = { build };
