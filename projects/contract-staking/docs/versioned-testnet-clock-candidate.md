# Versioned testnet staking and credential candidate

Status: local candidate only. No network installation or activation is enabled.

## Scope and invariants

- `VersionedTestnetStaking` reads the append-only native epoch schedule instead
  of dividing all history by the latest duration. Its source and genesis must
  match the canonical reader. There is no wallet-writable schedule setter.
- The candidate appends two packed policy ratios at slots 41 and 42. Existing
  balances, shares, restrictions,
  owner, recorded bonding deadlines and pending withdrawal timestamps remain in
  their original slots. Existing withdrawals can complete on their original
  terms after the transition.
- Only a Governor execution can change candidate staking policy. An action
  approved before migration retains the original seconds-based bounds and
  stored override. Newly approved actions record an epoch ratio. The immutable
  timing module identifies that provenance; callers cannot choose it themselves.
- For positions without a stored bonding end, historical timestamp lookup uses
  the duration that applied to their first-deposit timestamp. New user deposits
  and newly initialized DAO-rescued positions freeze an absolute deadline
  immediately, using the active clock even if the block timestamp moves back.
- New exits align to the real epoch end plus the configured unbonding period.
  A top-up can extend a pending withdrawal but cannot shorten its stored lock.
- Native operator auto-stake maturity continues to read financial epoch slot 0,
  independently of reward/voting epoch slot 4.
- `VersionedEpochCredential` retains storage and source identity. Historical
  receipts, earned weights, registered pools, delegation checkpoints, nonces and
  the EIP-712 domain remain inspectable at the original credential address.
  Current voting/delegation uses the schedule's epoch number, not the retired
  constant-duration calculation. Signature deadlines remain absolute timestamps.
- Base contract hooks preserve the old artifacts' behavior. Shipped historical
  bytecode is not rebuilt or replaced by this source change. New staking
  `epochDuration()` is `view`, not RC.2's specialized `pure`: both remain read-only
  with the same selector. Release consumers require regenerated matching ABIs.

## Local acceptance

Tests use isolated Hardhat state writes to simulate runtime replacement, never
live transactions. They compare existing storage/ABI, preserve old receipts and
signatures, test old withdrawals and new locks, test missing/mismatched clocks,
and check London opcode and EIP-170 size limits. Red/green and full regression
logs are retained in devnet-ops's 8 October v0.2 evidence directory.

## Not completed by these contracts

1. Native reward-ledger integration and the separately approved atomic runtime
   transition. The stored schedule must be identical in Go and Solidity, append
   at a future boundary and preserve already credited rewards.
2. Audit first-deposit initialization alongside the native integration. The
   current engine adds operator auto-stake shares but does not initialize the
   user's first-deposit timestamp. Do not invent timestamps or new maturity
   entitlements for those existing accounts. Any future path that initializes
   a position must also freeze its deadline from the active clock, since a
   backward block timestamp alone does not identify the active clock segment.
3. Native installation of the locally tested Governor/timelock candidates and
   preservation of open proposals' policy, deadlines and grace periods. The
   150-test contract suite covers old/new execution context, but does not prove
   whole-network migration or replay.
4. Runtime derivation at the canonical addresses and complete pre-state/code
   hash checks, including credential constructor identities and EIP-712 names.
   A deploy-and-copy test is not the production consensus migration mechanism.
5. Mainnet policy, two-second native block targeting, replay/recovery, release
   manifest selection, ABI distribution and frontend acceptance. No claim of
   independent security review or release readiness is made.

No network rollback is needed. Revert only the named local candidate commit if
withdrawing this work. Never downgrade an old binary onto upgraded databases.
