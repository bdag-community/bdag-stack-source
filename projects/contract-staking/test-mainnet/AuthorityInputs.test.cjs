const { expect } = require('chai');
const fs = require('node:fs');
const path = require('node:path');
const hre = require('hardhat');
const input = require('../release/mainnet/authority-inputs.json');
describe('Approved Mainnet public authority inputs', function () {
  let snapshot;
  beforeEach(async () => { snapshot = await hre.network.provider.send('evm_snapshot'); });
  afterEach(async () => { await hre.network.provider.send('evm_revert', [snapshot]); });
  it('retains the five-owner quorum and only the three expressly selected reviewers', function () {
    expect(input.signers).to.have.length(5); expect(input.threshold).to.equal(3);
    expect(input.reviewers.map(r => r.name)).to.deep.equal(['Francois', 'Duncan', 'Pieter']);
    expect(new Set(input.reviewers.map(r => r.address.toLowerCase())).size).to.equal(3);
    expect(input.reviewers.some(r => ['Jeremy', 'Matt'].includes(r.name))).to.equal(false);
    expect(input.extraMultisigApprovalOfDAOActions).to.equal(false);
    expect(input.deploymentAuthorized).to.equal(false);
  });
  it('exports approved reviewer identities without granting the multisig governance authority', async function () {
    expect(fs.existsSync(path.join(__dirname, '../scripts/release/export-mainnet-preparation.cjs')), 'missing preparation exporter with approved reviewer binding').to.equal(true);
    const exporter = require('../scripts/release/export-mainnet-preparation.cjs');
    expect(exporter.build, 'missing preparation exporter with approved reviewer binding').to.be.a('function');
    const result = await exporter.build(hre);
    expect(result.metadata.reviewerAssignmentsAreFixtures).to.equal(false);
    const registry = await hre.ethers.getContractAt('ApprovedDevRegistry', result.addresses.developers);
    for (const r of input.reviewers) expect(await registry.reviewers(r.address)).to.equal(true);
    for (const r of input.signers.filter(s => ['Jeremy', 'Matt'].includes(s.name))) expect(await registry.reviewers(r.address)).to.equal(false);
    expect(await registry.owner()).to.equal(result.addresses.stakingTimelock);
  });
});
