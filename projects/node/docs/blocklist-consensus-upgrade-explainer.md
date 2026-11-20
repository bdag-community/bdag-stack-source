# The blocklist consensus upgrade, in plain language

## What it is

BlockDAG has an on-chain "blocklist": a list of wallet addresses that the
network refuses to let move funds. This isn't enforced by a single admin
flipping a switch — it's enforced by every node's consensus rules, the same
rules that decide whether any other transaction or block is valid. That
means an address on the blocklist can't send or receive funds *anywhere on
the network*, not just on one exchange or one RPC endpoint.

Because rejecting transactions is a consensus rule, turning it on had to go
through the chain's normal upgrade process rather than just being deployed
like ordinary software.

## How a consensus upgrade gets turned on

Think of it like a network-wide vote that miners run automatically:

1. **The rule is defined.** Someone writes the new rule (here: "reject
   transactions to/from blocklisted addresses") and gives it a name, a
   unique ID bit, and a target start point in the chain.
2. **Miners signal support.** Once the chain reaches that start point,
   miners that have upgraded their node software start flipping a bit in
   the blocks they mine, signaling "I support this rule."
3. **The network counts votes.** Every node counts how many of the last
   2,000 blocks had that bit set. If 1,500 or more (75%) did, the upgrade
   is considered **locked in**.
4. **A grace period follows.** Locking in doesn't mean instant enforcement —
   there's a delay (2,000 more blocks) so every node on the network has
   time to catch up before the rule starts being enforced.
5. **Enforcement begins.** After the delay, every node — upgraded or not —
   treats the rule as active. From this point on, a transaction touching a
   blocklisted address is simply invalid, the same way a double-spend is
   invalid.

This is a "soft fork" style rollout: it only tightens what's allowed, so
even nodes that haven't upgraded keep working, they just won't enforce the
new rule themselves. Two related upgrades were rolled out this way:

- **Wallet Blocklist** — fully blocks an address from sending or receiving.
- **Selective Blocklist** — a lighter tier: a "restricted" address can only
  send to a specific pre-approved allow-list of destinations. A full block
  always overrides a selective restriction if an address is on both.

## Who actually maintains the list

The list of blocked/restricted addresses itself lives in a small governance
smart contract, controlled by a designated owner key. Adding or removing an
address is an owner-only transaction on that contract — it doesn't require
another network-wide upgrade vote each time, only the *rule that enforces
the list* needed that.

Nodes don't call into the contract like a normal app would (no ABI
decoding) — they read its storage directly for speed and determinism. That
means the contract's internal storage layout is itself consensus-critical:
if it ever changed shape, every node would need to agree on the new layout
at the same time, so it's pinned by an automated test that must pass before
any change to the contract is accepted.

## Why it's built this way

- **No single point of control.** Because activation requires broad miner
  support before it counts, no one party can unilaterally impose a new
  consensus rule on the network.
- **Deterministic, not "best effort."** The chain always asks "has this
  rule's activation point already passed?" rather than "is this rule
  currently marked active?" — the former gives the same answer no matter
  when you ask it (even replaying history from scratch), which matters
  because nodes sometimes need to resync from genesis and must recompute
  the exact same history.
- **Gradual, reversible-until-locked-in rollout.** Signaling can accumulate
  or stall without breaking anything; only crossing the vote threshold has
  real consequences, and even then there's a buffer period before
  enforcement starts.

## In practice

When these upgrades activated, a real gap showed up: the enforcing node
correctly marked blocklisted transactions invalid, but a separate public
RPC/explorer service wasn't honoring that flag and was still showing them
as "successful." That turned out to be a bug in the RPC/indexing layer, not
in the consensus rule itself — a reminder that the blocklist rule and any
service reporting on it are two different things, and both need to agree
for the block to actually feel enforced end-to-end.
