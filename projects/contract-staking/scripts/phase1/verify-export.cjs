// Independent fresh local EVM import of the exported private genesis fixture.
// Does not connect to any RPC or sign/broadcast a network transaction.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const {ethers, network} = require('hardhat');
const sha = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const IMPL = '0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc';
const word = value => ethers.toBeHex(value, 32);
async function main() {
  assert.equal(network.name, 'hardhat');
  const testnetBootstrap=Number(process.env.BDAG_FIXTURE_REVISION)>=6;
  assert.equal((await ethers.provider.getNetwork()).chainId, testnetBootstrap ? 1043n : 8008n);
  const revision=Number(process.env.BDAG_FIXTURE_REVISION);
  assert([2,3,4,5,6,7].includes(revision), 'Explicit candidate2 through revision7 fixture required');
  const dir = path.resolve(__dirname, revision === 2 ? '../../out/governance-devnet-v2' : revision === 3 ? '../../out/governance-devnet-v3' : revision === 4 ? '../../out/governance-devnet-v4' : revision === 5 ? '../../out/governance-devnet-v5' : revision === 7 ? '../../out/governance-testnet-release-v1' : '../../out/governance-testnet-bootstrap-v1');
  const manifestBytes = fs.readFileSync(path.join(dir, 'manifest.json'));
  const m = JSON.parse(manifestBytes);
  assert.equal(m.fixtureRevision, revision);
  for (const [file, hash] of Object.entries(m.files)) assert.equal(sha(fs.readFileSync(path.join(dir, file))), hash, file);
  const alloc = JSON.parse(fs.readFileSync(path.join(dir, testnetBootstrap ? 'bootstrap-alloc.json' : 'genesis-alloc.json')));
  await network.provider.send('hardhat_reset');
  for (const [address, account] of Object.entries(alloc)) {
    await network.provider.send('hardhat_setBalance', [address, ethers.toQuantity(BigInt(account.balance))]);
    await network.provider.send('hardhat_setNonce', [address, ethers.toQuantity(account.nonce)]);
    if (account.code) await network.provider.send('hardhat_setCode', [address, account.code]);
    for (const [slot, value] of Object.entries(account.storage || {})) await network.provider.send('hardhat_setStorageAt', [address, slot, value]);
  }
  if (testnetBootstrap) {
    const staking=await ethers.getContractAt(revision === 7 ? 'StakingContractV2Testnet' : 'StakingContractV2',m.stakingProxy);
    const reader=await ethers.getContractAt('ChainTallyReader',m.reader);
    const governor=await ethers.getContractAt('EpochGovernor',m.contracts.EpochGovernor);
    const policy=await ethers.getContractAt('GovernancePolicyModule',m.contracts.GovernancePolicyModule);
    const miningCredential=await ethers.getContractAt('EpochCredential',m.contracts.MiningCredential);
    const stakingCredential=await ethers.getContractAt('EpochCredential',m.contracts.StakingCredential);
    assert.equal(await staking.owner(),m.contracts.StakingTimelock);
    assert.equal(await staking.minerSplit(),70n);assert.equal(await staking.stakerSplit(),30n);
    const expectedEpoch = revision === 7 ? 60n : 604800n;
    assert.equal(BigInt(m.epochSeconds),expectedEpoch);
    assert.equal(await staking.genesisTime(),BigInt(m.genesisTime));assert.equal(await staking.epochDuration(),expectedEpoch);
    if (revision === 7) {
      assert.equal(await staking.operatorEpoch(),await reader.engineEpoch());
      assert.equal(m.activation.status,'unscheduled-not-distributable');
      assert.equal(m.activation.signalStartLayer,undefined);
      assert.equal(m.activation.earliestLayer,undefined);
    }
    assert.equal(await staking.daoInitialized(),true);
    assert.equal(await ethers.provider.getStorage(m.stakingProxy,IMPL),ethers.zeroPadValue(m.forkArtifacts.StakingContractV2Governance.address,32));
    assert.equal(await reader.clockSource(),m.stakingProxy);
    assert.equal(await reader.currentEpoch(),await staking.currentEpoch());
    for(const [name,credential] of [['MiningCredential',miningCredential],['StakingCredential',stakingCredential]]) {
      assert.equal(await credential.genesisTime(),BigInt(m.genesisTime),name);
      assert.equal(await credential.epochDuration(),expectedEpoch,name);
      assert.equal(await credential.currentEpoch(),await staking.currentEpoch(),name);
      assert.equal(await credential.votingReady(),false,name);
    }
    assert.equal(await governor.clock(),await staking.currentEpoch()+1n);
    assert.equal(await policy.governor(),governor.target);assert.equal(await policy.controller(),m.contracts.MiningTimelock);
    assert.equal(await policy.versionCount(),1n);
    for(const route of m.routes) {
      const rule=await governor.routes(route.target,route.selector);
      assert.equal(rule.domain,BigInt(route.domain));assert.equal(rule.tier,BigInt(route.tier));
      assert.equal(await governor.targetOwnerDomain(route.target),BigInt(route.ownerDomain));
    }
    for(const name of ['MiningTimelock','StakingTimelock']) {
      const lock=await ethers.getContractAt('GovernanceTimelock',m.contracts[name]);
      assert.equal(await lock.governor(),m.contracts.EpochGovernor);assert.equal(await lock.getMinDelay(),259200n);
      assert.equal(await lock.hasRole(await lock.DEFAULT_ADMIN_ROLE(),lock.target),true);
      for(const role of ['PROPOSER_ROLE','EXECUTOR_ROLE','CANCELLER_ROLE']) {
        assert.equal(await lock.hasRole(await lock[role](),m.contracts.EpochGovernor),true);
        assert.equal(await lock.hasRole(await lock[role](),ethers.ZeroAddress),false);
      }
    }
    for(const [address,name] of [[m.blocklist,'BlocklistGovernance'],[m.selectiveBlocklist,'SelectiveBlocklist']]) {
      const list=await ethers.getContractAt(name,address);assert.equal(await list.owner(),m.contracts.MiningTimelock);
    }
    const hub=await ethers.getContractAt('GovernanceHubV2',m.hubProxy);
    assert.equal(await hub.executingTimelock(),m.contracts.MiningTimelock);assert.equal(await hub.governor(),m.contracts.EpochGovernor);
    for(const [address,account] of Object.entries(alloc)) {
      assert.notEqual(account.code,'0x',address);
      assert.equal(ethers.keccak256(await ethers.provider.getCode(address)),ethers.keccak256(account.code),address);
      for(const [slot,value] of Object.entries(account.storage||{})) assert.equal(await ethers.provider.getStorage(address,slot),value,`${address}:${slot}`);
    }
    const funding=m.funding;
    const developerRegistry=await ethers.getContractAt('ApprovedDevRegistry',m.contracts.ApprovedDevRegistry);
    const treasury=await ethers.getContractAt('CommunityTreasury',m.contracts.CommunityTreasury);
    const factory=await ethers.getContractAt('ProjectEscrowFactory',m.contracts.ProjectEscrowFactory);
    const bond=await ethers.getContractAt('BidBondVault',m.contracts.BidBondVault);
    assert.equal(await developerRegistry.owner(),m.contracts.StakingTimelock);
    assert.equal(await treasury.owner(),m.contracts.StakingTimelock);assert.equal(await treasury.governor(),m.contracts.EpochGovernor);assert.equal(await treasury.clock(),m.stakingProxy);
    assert.equal(await factory.governance(),m.contracts.StakingTimelock);assert.equal(await factory.registry(),m.contracts.ApprovedDevRegistry);assert.equal(await factory.challengeSeconds(),2419200n);
    assert.equal(await bond.governance(),m.contracts.StakingTimelock);assert.equal(await bond.registry(),m.contracts.ApprovedDevRegistry);assert.equal(await bond.treasury(),m.contracts.CommunityTreasury);
    assert.equal(await factory.implementation(),funding.nestedProjectEscrowImplementation.address);
    console.log(JSON.stringify({status:'passed-testnet-bootstrap-import',manifestSha256:sha(manifestBytes),revision,contracts:Object.keys(alloc).length,routes:m.routes.length,epochSeconds:m.epochSeconds,checks:['artifact hashes','contract code and storage',revision === 7 ? '60-second reward clock and separate native financial clock' : 'production epoch clock','Guardian-free authorities','policy and routes','funding ownership','inert pre-reward credentials']},null,2));
    return;
  }
  const v1 = await ethers.getContractAt('StakingContract', m.stakingProxy);
  assert.equal((await v1.owner()).toLowerCase(), m.oldAddresses.owner);
  assert.equal(await v1.genesisTime(), BigInt(m.genesisTime));
  assert.equal(await v1.minerSplit(), 70n); assert.equal(await v1.stakerSplit(), 30n);
  let principal = 0n;
  for (const p of m.stakers) {
    assert.equal(await v1.stakerPrincipalBDAG(p.staker, p.pool), BigInt(p.principal));
    assert.equal(await v1.stakerPoolShares(p.staker, p.pool), BigInt(p.principal));
    assert.equal(await v1.userFirstDepositTimestamp(p.staker), BigInt(p.firstDeposit));
    principal += BigInt(p.principal);
  }
  assert.equal(principal, ethers.parseEther('250'));
  assert.equal(await ethers.provider.getBalance(m.stakingProxy), principal);
  for (const pool of m.pools) {
    const total = m.stakers.filter(p => p.pool === pool).reduce((sum, p) => sum + BigInt(p.principal), 0n);
    for (const method of ['poolTotalAssets', 'poolTotalShares', 'poolTotalStake']) assert.equal(await v1[method](pool), total);
  }
  for (let slot = 18; slot <= 40; ++slot) assert.equal(BigInt(await ethers.provider.getStorage(m.stakingProxy, slot)), 0n);
  for (const artifact of Object.values(m.forkArtifacts)) assert.equal(await ethers.provider.getCode(artifact.address), '0x');
  for (const authority of Object.values(m.authority)) assert.equal(ethers.keccak256(await ethers.provider.getCode(authority.address)), authority.codeHash);
  assert.equal(await ethers.provider.getTransactionCount(m.revisionMarker.account), 1);
  for (const name of ['MiningTimelock', 'StakingTimelock']) {
    const address = m.contracts[name], lock = await ethers.getContractAt('GovernanceTimelock', address);
    assert.equal(await lock.governor(), m.contracts.EpochGovernor);
    assert.equal(await lock.getMinDelay(), 5n);
    assert.equal(await lock.hasRole(await lock.DEFAULT_ADMIN_ROLE(), address), true);
    for (const role of ['PROPOSER_ROLE', 'EXECUTOR_ROLE', 'CANCELLER_ROLE']) {
      assert.equal(await lock.hasRole(await lock[role](), m.contracts.EpochGovernor), true);
      assert.equal(await lock.hasRole(await lock[role](), ethers.ZeroAddress), false);
    }
  }
  const governor = await ethers.getContractAt('EpochGovernor', m.contracts.EpochGovernor);
  const policyModule = await ethers.getContractAt('GovernancePolicyModule', m.contracts.GovernancePolicyModule);
  assert.equal(await policyModule.governor(), governor.target);
  assert.equal(await policyModule.controller(), m.contracts.MiningTimelock);
  assert.equal(await policyModule.versionCount(), 1n);
  for (const route of m.routes) {
    const policy = await governor.routes(route.target, route.selector);
    assert.equal(policy.domain, BigInt(route.domain)); assert.equal(policy.tier, BigInt(route.tier));
    assert.equal(await governor.targetOwnerDomain(route.target), BigInt(route.ownerDomain));
  }
  if (revision >= 3) {
    const funding=m.funding;
    assert(funding && funding.nestedProjectEscrowImplementation && funding.veto, 'Missing revision3+ funding manifest');
    if (revision >= 4) assert(funding.deploymentNonces, 'Missing deployment nonce identity');
    for (const name of ['ApprovedDevRegistry','CommunityTreasury','ProjectEscrowFactory','BidBondVault']) {
      const item=funding.contracts[name]; assert(item && item.address === m.contracts[name], `Missing funding ${name}`);
      assert.equal(ethers.keccak256(await ethers.provider.getCode(item.address)),item.codeHash,`${name} runtime`);
    }
    const developerRegistry=await ethers.getContractAt('ApprovedDevRegistry',m.contracts.ApprovedDevRegistry);
    const treasury=await ethers.getContractAt('CommunityTreasury',m.contracts.CommunityTreasury);
    const factory=await ethers.getContractAt('ProjectEscrowFactory',m.contracts.ProjectEscrowFactory);
    const bond=await ethers.getContractAt('BidBondVault',m.contracts.BidBondVault);
    const implementation=await ethers.getContractAt('ProjectEscrow',funding.nestedProjectEscrowImplementation.address);
    assert.equal(await developerRegistry.owner(),m.contracts.StakingTimelock);
    assert.equal(await treasury.owner(),m.contracts.StakingTimelock);assert.equal(await treasury.governor(),m.contracts.EpochGovernor);assert.equal(await treasury.clock(),m.stakingProxy);
    assert.equal(await factory.governance(),m.contracts.StakingTimelock);assert.equal(await factory.registry(),m.contracts.ApprovedDevRegistry);assert.equal(await factory.implementation(),funding.nestedProjectEscrowImplementation.address);
    assert.equal(await factory.challengeSeconds(),28n*24n*60n*60n);
    assert.equal(await bond.governance(),m.contracts.StakingTimelock);assert.equal(await bond.registry(),m.contracts.ApprovedDevRegistry);assert.equal(await bond.treasury(),m.contracts.CommunityTreasury);
    assert.equal(ethers.keccak256(await ethers.provider.getCode(funding.nestedProjectEscrowImplementation.address)),funding.nestedProjectEscrowImplementation.codeHash);
    assert.equal(await implementation.factory(),ethers.ZeroAddress);
    for (const [slot,value] of Object.entries(funding.nestedProjectEscrowImplementation.storage)) assert.equal(await ethers.provider.getStorage(funding.nestedProjectEscrowImplementation.address,slot),value);
    assert.equal(funding.nestedProjectEscrowImplementation.createdBy,m.contracts.ProjectEscrowFactory);assert.equal(funding.nestedProjectEscrowImplementation.traceDepth,2);assert.equal(funding.nestedProjectEscrowImplementation.initializerDisabled,true);
    const factoryNonce=await ethers.provider.getTransactionCount(factory.target),implementationNonce=await ethers.provider.getTransactionCount(funding.nestedProjectEscrowImplementation.address);
    if (revision >= 4) { assert.equal(factoryNonce,2);assert.equal(implementationNonce,1);assert.equal(funding.deploymentNonces.factory,2);assert.equal(funding.nestedProjectEscrowImplementation.nonce,1); }
    const veto=m.routes.find(route=>route.target.toLowerCase()===m.contracts.ProjectEscrowFactory.toLowerCase()&&route.selector===funding.veto.selector);
    assert(veto);assert.equal(veto.domain,3);assert.equal(veto.tier,2);assert.equal(veto.ownerDomain,2);
    if (process.env.BDAG_VERIFY_FIRST_CLONE === '1') {
      const owner=m.contracts.StakingTimelock,latest=await ethers.provider.getBlock('latest'),developer=m.stakers[0].staker,payout=m.stakers[1].staker;
      const projectId=ethers.id(`fresh-import-first-clone-r${revision}`),expectedClone=ethers.getCreateAddress({from:factory.target,nonce:factoryNonce});
      await network.provider.send('hardhat_setBalance',[owner,ethers.toQuantity(ethers.parseEther('1'))]);await network.provider.send('hardhat_impersonateAccount',[owner]);
      try {
        const ownerSigner=await ethers.getSigner(owner);await (await developerRegistry.connect(ownerSigner).setApproval(developer,true,ethers.id(`fresh-import-review-r${revision}`))).wait();
        const receipt=await (await factory.connect(ownerSigner).createProject(projectId,developer,payout,1n,BigInt(latest.timestamp)+600n,0,funding.council,[])).wait();
        const clone=await factory.projects(projectId);assert.equal(clone,expectedClone);assert.notEqual(await ethers.provider.getCode(clone),'0x');
        const escrow=await ethers.getContractAt('ProjectEscrow',clone);assert.equal(await escrow.factory(),factory.target);assert.equal(await escrow.developer(),developer);assert.equal(await escrow.payout(),payout);assert.equal(await escrow.goal(),1n);
        assert(revision >= 4,'Revision3 must fail this actual first-clone import check');assert(receipt.hash);
      } finally { await network.provider.send('hardhat_stopImpersonatingAccount',[owner]); }
    }
  }
  const credentials = await Promise.all(['MiningCredential', 'StakingCredential'].map(name => ethers.getContractAt('EpochCredential', m.contracts[name])));
  for (const credential of credentials) {
    assert.equal(await credential.genesisTime(), BigInt(m.genesisTime));
    assert.equal(await credential.epochDuration(), 60n);
    assert.equal(await credential.votingReady(), false);
  }
  // Local EVM installation verifies exported runtime binding and the new clock
  // interface. This does not substitute for native fork/consensus activation.
  const protectedSlots = await Promise.all([1, 2, 8].map(slot => ethers.provider.getStorage(m.stakingProxy, slot)));
  for (const [name, artifact] of Object.entries(m.forkArtifacts)) {
    const runtime = fs.readFileSync(path.join(dir, name + '.runtime.hex'), 'utf8').trim();
    assert.equal(ethers.keccak256(runtime), artifact.runtimeHash);
    await network.provider.send('hardhat_setCode', [artifact.address, runtime]);
  }
  await network.provider.send('hardhat_setStorageAt', [m.stakingProxy, IMPL, ethers.zeroPadValue(m.forkArtifacts.StakingContractV2Governance.address, 32)]);
  const staking = await ethers.getContractAt('StakingContractV2Devnet', m.stakingProxy);
  const floor = await staking.currentEpoch() + 3n;
  await network.provider.send('hardhat_setStorageAt', [m.reader, '0x0', word(floor)]);
  const reader = await ethers.getContractAt('ChainTallyReader', m.reader);
  assert.equal(await reader.engineEpoch(), floor);
  for (const source of [staking, reader, ...credentials]) assert.equal(await source.currentEpoch(), floor);
  assert.equal(await governor.clock(), floor + 1n);
  assert.deepEqual(await Promise.all([1, 2, 8].map(slot => ethers.provider.getStorage(m.stakingProxy, slot))), protectedSlots);
  for (const p of m.stakers) assert.equal(await staking.stakerPrincipalBDAG(p.staker, p.pool), BigInt(p.principal));
  console.log(JSON.stringify({status: 'passed-local-import', manifestSha256: sha(manifestBytes),
    revision, accounts: Object.keys(alloc).length, positions: m.stakers.length, principal: principal.toString(), routes: m.routes.length,
    nativeEpochFloor: floor.toString(), checks: ['export hashes', 'principal/shares/deposits/pool totals', 'protected storage', 'authority hashes',
      'timelock roles', 'bounded policy module', 'routing', 'inert pre-fork credentials', 'exported runtime bindings and unified native clock', ...(revision >= 3 ? ['funding ownership/code/constructor state and nested implementation allocation', ...(process.env.BDAG_VERIFY_FIRST_CLONE === '1' ? ['actual first factory clone after fresh import'] : [])] : [])],
    limitation: 'Fresh local EVM fixture import and installation only; native activation and live DAO/lifecycle remain pending.'}, null, 2));
}
main().catch(error => { console.error(error); process.exitCode = 1; });
