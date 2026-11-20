# DAO governance replacement candidate

Status: local implementation and private-fixture candidate. It is not a production approval, deployment instruction or claim of independent review.

## Voting and proposal lifecycle

`EpochGovernor` coordinates Mining and Staking owner timelocks. Every vote is sourced from the previous complete native reward epoch. Claims are optional, non-transferable visibility receipts and never gate voting.

- Mining weight is the native liquid miner reward plus operator auto-stake credit.
- Staking weight is native pool yield attributed by reward-per-share checkpoints, including explicit operator units.
- Deposits, historical principal and transfers do not create voting power.
- Weight is uncapped and expires after its voting epoch. A replacement ballot replaces the prior ballot for the same voter, chamber and proposal.
- CORE normalizes votes cast in each chamber, then applies the exact 70% Mining and 30% Staking score.
- Ordinary CORE requires Mining turnout 15%, Staking turnout 10%, at least 40% yes in each chamber, and a weighted score above 50%.
- Critical CORE requires Mining turnout 20%, Staking turnout 15%, more than 50% yes in each chamber, and a weighted score of at least 60%.
- Specialist ordinary decisions require more than 50% yes. Specialist critical decisions require at least 60% yes. Abstain contributes to turnout and cast weight, not yes support.

Proposal state is create, pending, active, succeeded or defeated, queued, then executed, canceled or expired. Queue and execution are public Governor entrypoints once their conditions are met. Ordinary actions wait at least 72 hours in production and critical actions wait at least seven days. The private fixture uses intentionally short delays.

## Authority and policy

There is no Guardian, bootstrap coordinator, emergency EOA, open timelock executor or mutable timelock administrator. Only the Governor has proposer, executor and canceller roles on both timelocks. Generic relay, timelock replacement, role mutation and delay mutation are disabled.

Routes bind every reviewed target and selector to a domain, tier and owner timelock. Unknown selectors and native-value calls are rejected. Upgrade, ownership, role, release, blocklist, selective-routing, staking-restriction, pause and delivery-resolution selectors are forced to critical CORE policy.

`GovernancePolicyModule` stores versioned threshold rules. An amendment is accepted only while the Mining timelock executes a critical CORE proposal. It must take effect in a future epoch and cannot weaken the agreed constitutional lower bounds. Each proposal snapshots one policy version, so a later amendment cannot rewrite an open or completed vote.

Mixed-owner batches must list Mining-owned actions before Staking-owned actions. Both batches receive one ETA and execute atomically in one transaction. Failure in either batch reverts the entire execution.

## Activation and native state

Activation is performed once from a reviewed immutable node manifest. The engine validates authority code hashes and bound creation/runtime hashes, preserves existing restrictions and staking state, installs the reward reader and implementations, rotates owners to the timelocks, clears retired emergency-key storage, sets the DAO activation flag and writes the manifest marker last.

There is no partially observed bootstrap reward. The first complete native reward epoch becomes the first voting source. Mining totals close without wallet registration or keeper iteration. Staking share checkpoints are written around every share-changing action while consensus maintains pool accumulator, residual, total and operator units.

The production signalling definition is 1,500 signalling main-chain blocks in a 2,000-layer window, followed by at least 2,000 layers of activation delay, seven days of public notice and six weeks from initial release distribution. A DAO release announcement does not itself change consensus, and node operators retain refusal rights.

## Funding

Funding is native BDAG only. Fixed payout remains the default. Tracked milestones require independent two-of-three review and a 28-day challenge period. Only an executed DAO decision can veto release. The bond vault keeps the selected developer's rounded-up 2% bond locked until DAO delivery resolution, returns unsuccessful bonds after final selection and sends a rejected-delivery forfeiture only to the Community Treasury.

## Verification

Run the isolated London suite with a supported Node release:

```sh
BDAG_BUILD_LABEL=governance npx hardhat test --config hardhat.phase1.config.cjs
```

The suite checks exact thresholds, absent-chamber defeat, replacement ballots, native reward attribution, no principal voting, delegation, receipt soulbinding, policy snapshots, timelock authority, atomic execution, funding/refunds/bonds, storage layout, EIP-170 size and post-London opcode compatibility.

Private fixture revision 5 is exported with `BDAG_FIXTURE_REVISION=5` and independently re-imported by `scripts/phase1/verify-export.cjs`. A local fixture is evidence of deterministic construction only. Production still requires complete core recovery/replay testing, public-testnet rehearsals, matched builds, archival reconciliation, independent contract/core review and explicit release authorization.
