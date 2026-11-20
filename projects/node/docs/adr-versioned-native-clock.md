# ADR: Prepare the reward clock before transactions

## Status

Implemented as inactive candidate code. No release manifest enables it yet.

## Context

The reward clock is moving from 60-second to 1,800-second testnet epochs.
Historical rewards must retain their epoch numbers. Native block timestamps
can move backwards, and staking transactions checkpoint accrued rewards before
the block's native mining/staking credits are finalized.

## Decision

After a separately authorized atomic migration installs a new marker, prepare
the versioned clock in the shared builder/importer pre-block hook. Validate the
schedule, network, prior bootstrap and bounded eligibility markers before any
mutation. Advance the same reward-epoch state used by both reward chambers and
persist a monotonic timestamp floor in append-only ChainTally slot 9.

Keep the original full-subsidy tally and operator financial maturity clock
unchanged. Reuse the existing eligibility-advance implementation rather than
resetting the first eligible epoch. Revalidate at Finalize before balance writes.
The historical unmarked path is unchanged. Invalid activated state follows the
engine's existing fail-stop pre-block policy, not a fallback or skipped block.

## Consequences

- Contract transactions and native reward credits agree on the active epoch.
- A timestamp rewind cannot reopen completed epochs or action windows.
- Reorgs restore the clock with the rest of the candidate StateDB.
- The small bounded schedule is checked twice per block. Avoid caching across
  states/forks unless later profiling and tests justify a safely keyed cache.
- Atomic migration, manifest-pinned runtime/authority checks and full upgrade
  acceptance are still necessary. This marker must not be installed manually.

## Alternatives

- Replacing the global duration would reinterpret historical rewards and locks.
- Updating only at Finalize would let earlier transactions observe a different
  epoch at rollover or on backwards-time blocks.
- A wall-clock/process cache would make replay and competing branches ambiguous.

## Validation

`versioned_reward_clock_test.go` covers the integrated ledgers, malformed-state
atomicity, inactive-state preservation, idempotence and snapshot rewind. Existing
financial-clock and governance-bootstrap tests remain unchanged and pass.
