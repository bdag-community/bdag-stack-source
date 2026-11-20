# ADR: epoch policy for new funding campaigns

Status: local candidate design, not installed or approved for public funding.

## Acceptance contract

- New campaigns snapshot their creation epoch and its duration from the native
  versioned clock. Challenge, maximum funding and inactivity periods derive
  from 4, 90/7 and 180/7 epochs respectively, rounding fractional seconds up.
- The challenge must also cover three epochs plus the configured critical
  delay: time to raise/respond, wait for the next voting epoch, vote, and queue.
  Stronger critical policies increase this minimum rather than shrinking it.
- Existing campaign addresses, clone implementation, balances, deadlines,
  contributions and refund rights stay unchanged. Only new factory-created
  clones created by newly approved proposals use the candidate. An action
  approved before migration still creates the original implementation with its
  original 28-day challenge. Factory project lookup storage remains compatible.
- Later clock changes do not rewrite a funded campaign's frozen terms. A future
  governance upgrade must separately prove that it does not extend the DAO
  process past the available challenge for any live tracked campaign. There is
  no discretionary freeze, complaint veto, Guardian or recipient-change path.
- Preserve current governance-only project creation/veto, approved developers,
  independent 2-of-3 review, native-only payments and pro-rata refund rules.
- Prove a real factory runtime replacement leaves an old clone unchanged while
  a new clone uses epoch terms. Check failed funding, partial releases, challenge
  boundaries, veto/resubmission, inactivity reclaim, unauthorized calls, storage,
  London and EIP-170 limits. Run the complete original suite too.

## Design

Keep the existing escrow configuration and factory method ABIs. Introduce
versioned escrow/factory runtimes with immutable clock/timing dependencies.
Small virtual timing hooks retain the original escrow's exact fixed-day rules;
only the candidate freezes epoch-based terms during initialization. The old
exported bytecode is not regenerated. The factory's existing `projects` mapping
stays in slot 0; all new policy wiring is immutable. The factory pins both the
legacy implementation/challenge and the new implementation. It reads provenance
from the bound Governor's transaction-scoped execution context, never from user
input. The old implementation must remain installed for existing and newly
executed legacy-approved clones.

Funding still accepts an explicit deadline in approved calldata. Preparing that
deadline and the separate discussion/bidding windows from canonical epoch policy
is application work, not permission to reinterpret old approved calldata.

No wallet may initialize or rewrite the native clock. Factory/runtime selection,
hash pins and installation belong to the separately gated atomic migration.

## Local verification, 8 October

The new tests initially failed because no epoch-aware factory existed. The
candidate now passes 14 funding-policy tests including London/size checks; the
full contract suite passes 150 tests on Node 24.19.0 and Solidity 0.8.24. Factory
replacement is simulated only in an isolated EVM. There is no live migration or
claim of independent review. The unchanged legacy funding tests also pass.
