// Immutable public creation bytes, copied from the core's pinned historical fixture.
// No sibling checkout, generated allocation, account keys or live RPC is required.
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');
const assert = require('node:assert/strict');
function canonicalProxyCreation() {
  const bytes = fs.readFileSync(path.join(__dirname, 'GovernanceProxyLegacy.creation.hex'));
  assert.equal(createHash('sha256').update(bytes).digest('hex'), '73f2f6fefbad8a6da48cbe962a1a6f9a58a6d74cb5f355ea000db2bec8643900', 'historical proxy fixture changed');
  return bytes.toString('utf8').trim();
}
module.exports = { canonicalProxyCreation };
