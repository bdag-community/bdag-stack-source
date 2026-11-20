# ADR: preserve proposal timing across the testnet clock transition

Status: local implementation decision within the approved v0.2 preparation.
Not an activation approval or claim of release readiness.

## Context

The current Governor is 24,143 bytes, close to the 24,576-byte runtime limit.
Existing proposal deadlines, queue delays and grace periods derive from its
60-second clock. Replacing that constant changes already agreed terms. Native
block timestamps may go backwards, so wall time alone can reopen voting.

## Decision

- Keep the Governor's identity, ballots, deposits and policy snapshots.
- Use a separate immutable-bound timing module for newly created proposals.
  Freeze absolute start/end times, delay and grace when each proposal is made.
- Missing timing records identify original proposals and retain their original
  60-second voting windows, delays and two-epoch grace. Never treat a missing
  record as permission to apply more generous new terms.
- Append a native monotonic timestamp floor at ChainTally slot 9, alongside the
  candidate schedule at slot 8. Slots 0–7 and reward attribution do not move.
- All timing-module writes require the bound Governor. There are no EOA,
  Guardian or wallet-callable migration/configuration setters.
- Francois superseded the fixed-second candidate policy on 8 October. Ordinary
  actions wait **3/7 of an epoch**, critical actions **one epoch**, and action
  grace retains the original **two epochs**. Mainnet's seven-day epoch therefore
  still yields three-/seven-day waits. The proposed 30-minute Testnet epoch yields
  772/1,800 seconds, with 3,600 seconds of action grace. Fractional conversion
  rounds up, never down. Network-specific epoch length belongs in the canonical
  schedule, not duplicated day constants in business rules.
- The module accepts an immutable epoch policy, rejects zero/weaker/inverted
  minima, and snapshots converted terms using the proposal's voting epoch.
  Later clock segments cannot change those terms. The approved release values
  are `{ordinaryNumerator:3, ordinaryDenominator:7, criticalEpochs:1, graceEpochs:2}`.
- Replace both owner timelock runtimes atomically with the versioned Governor.
  An immutable-bound `VersionedGovernanceTimelock` enforces the proposal-specific
  delay while the Governor queues, not a retired fixed `_minDelay` or today's
  possibly different epoch length. An EOA cannot configure that delay.
- Queue context lives in the new timing module. Only the bound Governor can
  begin/end it, around the original queue/classification/state checks. Failure
  rolls back the entire context and operations. Scheduling requires that
  context and the existing Governor proposer role. Both timelocks use the same
  module. No target is executed during scheduling. Outside queueing, the minimum
  getter reports the current ordinary epoch-policy delay.
- Keep timelock storage, roles, salts, pending operation IDs and exact release
  timestamps. The old `_minDelay` storage word is retained but is not the new
  policy authority. Existing queued operations cannot execute early. Guardian,
  EOA executor, role-administration and mutable-delay paths remain unavailable.
- Execution context also lives in the new timing module. The Governor opens
  it around its original execution checks and atomically clears it afterward.
  An existing proposal has no new timing record and therefore retains old
  staking/funding action semantics. Newly recorded proposals use epoch policy.
  Targets and wallets cannot select the provenance or nest executions. A failed
  action rolls the context back with the rest of the transaction.

## Consequences and alternatives

The extra module and native timestamp floor need atomic migration, hash pins,
storage-layout tests and full Governor integration tests before distribution.
Timing-module unit tests alone do not establish that integration.
The 8 October local suite now exercises real versioned Governor/timelock
queue/execution, old floor storage, frozen queued timestamps, changed epoch
lengths, mixed-owner atomicity and unauthorized scheduling. Native installation,
full network acceptance and independent security review remain separate gates.

The separate versioned Governor is compiled with Solidity 0.8.24, London,
optimizer 200 runs and viaIR. Its conventional pipeline exceeded EIP-170
(25,228 bytes); lowering runs alone remained over the limit (24,710 bytes).
The IR candidate fits below the limit without removing authority checks or
relaxing the EVM size limit. Only this new source has a compiler override.
Historical release artifacts and the original native manifest stay pinned.
The candidate retains scalar timing getters for ABI compatibility, describing
its constructor configuration. Legacy values are pinned independently in the
timing module. New clients must use proposalTiming(id) for each proposal and
the versioned epoch interface for live countdowns. Runtime construction at the
native cutover must use the new active duration and delays, not require the
new credentials to expose their retired duration.

Rejected: changing global constants, assuming nobody proposes near cutover,
discarding existing proposals, clearing ballots, or expanding the Governor
past the normal EVM code-size limit.

Also rejected: simply lowering a stored timelock number, allowing public delay
updates, or using the current epoch's length to rewrite a frozen proposal delay.
The contextual floor adds a Governor/module/timelock coupling, so all immutable
addresses and runtime hashes must be checked together at atomic activation.

## Remaining epoch-policy consumers

Prospective staking overrides are now covered locally. The versioned staking
runtime appends two packed ratios at slots 41 and 42. Its existing owner-only,
seconds-input ABI records the ratio to the execution epoch and future policy
reads scale that ratio, rounding up. Existing deposit/exit deadlines and native
operator maturity do not change. An untouched pre-migration override remains
on its original terms until the DAO explicitly replaces it. The full local
suite passes 150 tests, including exact prefix layout, old approved actions and
unauthorized calls. Old approved setters still use the old fixed-second bounds;
their execution cannot unexpectedly become invalid under a longer epoch.
This candidate is not installed and historical exported runtimes are unchanged.

This change fixes the new governance candidate, not every existing contract or
application. Before v0.2 is called epoch-consistent, complete these consumers:

- Funding candidates now snapshot 4, 90/7 and 180/7 epoch policy for new
  campaigns and retain old clone terms. Stronger critical policy increases the
  challenge minimum. See `adr-versioned-funding-timing.md`. Native selection,
  corresponding UI and funding's separate legal/operational gates remain open.
- Development: discussion, bidding and funding windows currently use 3/14/30
  day defaults or local-mode shortcuts. Snapshot 3/7, 2 and 30/7 epoch policy
  against verified canonical epoch metadata, not browser/server wall time.
- Both frontends/API: use canonical versioned epoch bounds and proposal timing,
  return integer durations/deadlines, and show both epoch units and their actual
  duration. Formatting a duration in days is not itself a hard-coded policy.
- Historical exports, original V1/legacy staking and published V3 definitions
  remain immutable history. Do not make clean replay execute new timing rules
  retroactively. A legacy-fixture acceleration would be a separate fixture change.
- Native signalling still counts native layers/blocks. Transport timeouts and
  operational human notice are not financial/governance epochs. Do not silently
  reinterpret those independent safety controls as part of this code change.

The deployed testnet still has its old fixed delays. These locally tested
runtime replacements are not a live administrative bypass. The transition
authority and qualification must be explicit before confirming a signalling or
activation appointment; this ADR does not select a date or change live state.
