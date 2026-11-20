const { expect } = require('chai');
const { artifacts } = require('hardhat');
describe('Phase 1 governance artifact compatibility', function () {
  for (const name of ['EpochGovernor','VersionedEpochGovernor','VersionedGovernanceTiming','VersionedGovernanceTimelock','VersionedProjectEscrow','VersionedProjectEscrowFactory','GovernanceTimelock','GovernancePolicyModule','GovernanceHubV2','MiningPolicyRegistry']) {
    it(`${name} has London-only runtime instructions and fits EIP170`, async function () {
      const artifact=await artifacts.readArtifact(name);
      const bytes=Buffer.from(artifact.deployedBytecode.slice(2),'hex');
      expect(bytes.length).to.be.greaterThan(0).and.at.most(24576);
      const metadataLength=bytes.readUInt16BE(bytes.length-2);
      const end=bytes.length-metadataLength-2;
      expect(end).to.be.greaterThan(0);
      const forbidden=new Set([0x49,0x4a,0x5c,0x5d,0x5e,0x5f]);
      for(let pc=0;pc<end;pc++) {
        const opcode=bytes[pc];
        // Solidity emits INVALID before its embedded role-hash/data pool, then
        // CBOR metadata. Those bytes are CODECOPY data, not EVM instructions.
        if(opcode===0xfe) break;
        expect(forbidden.has(opcode),`${name}: post-London instruction ${opcode.toString(16)} at ${pc}`).to.equal(false);
        if(opcode>=0x60&&opcode<=0x7f) pc+=opcode-0x5f;
      }
    });
  }
});
