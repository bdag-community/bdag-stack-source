# One-time Testnet timing correction

Status: authorized preparation, not an installed or distributable release.

Francois approved this exception on 8 October 2026. The existing Testnet already
has DAO V3 with fixed-day waits. This one transition may use fresh miner signalling
without first waiting for those old seven-day DAO terms. It is not a Mainnet
exception or a reusable administrative authority.

## Acceptance contract

- Chain 1043 and the existing native Testnet identity only. No genesis reset,
  historical V3 definition/artifact edit, wallet key or callable activation API.
- Distinct `TestnetTimingV1` bit `0x20`, 1,500/2,000 signals in one published
  window, exactly 2,000 additional layers, and at least 24 hours after notice.
  A missed window requires a newly reviewed release, never a sliding manifest.
- Persist the immutable manifest in EVM ChainConfig and native upgrade state.
  Reject changed/removed past policy. Preparation source leaves its schedule null.
- Install all matching runtimes atomically, with exact old/new hash checks and
  empty-address checks before any write. No partial migration can commit.
- Preserve every balance, nonce, old storage slot, restriction, user lock, exit,
  reward record, receipt, ballot, proposal and queued ETA. Keep old funding clones.
- The new reward schedule starts the longer interval at the next untouched old
  epoch boundary derived from the activation block and persisted epoch floor.
  Thus the already-observed epoch closes under its original 60-second terms.
  Keep the financial/operator maturity clock separate.
- Old approved parameter/funding actions retain their original semantics. Only
  the Governor's atomic execution context selects old versus new policy. No
  target, owner key or UI may choose the policy provenance itself.
- Use the new two-second native target only through a history-aware difficulty
  rule shared by mining and import. Do not mutate global historical parameters
  or DAG ordering assumptions. Qualify CPU/ASIC templates and sustained mining.
- Rebuild pinned runtimes/ABIs, clean replay, isolated upgrade/restart/recovery,
  both frontend journeys and reproducible Linux AMD64 output before packaging.

## Scope of the approved hash-power statement

Francois reports majority Testnet hash power through the dedicated X30. Measure
actual accepted signalling blocks before declaring success: a majority is not
necessarily the required 75%. Hardware control never replaces chain validation,
operator readiness, backups, immutable notice or the activation delay.

No live node, miner, funding wallet, release pointer or contract is changed by
this ADR. Future Mainnet release approval and Jeremy's review remain separate.

## Read-only native transition preflight

`planTestnetTimingClock` now checks both independent gates, the exact persisted
definition, prior V3 bootstrap, original eligibility markers and empty new
clock slots. It selects the next old epoch boundary using the greater of the
activation timestamp's epoch and the persisted reward epoch. Tests cover exact
boundaries, backward timestamps, missing votes/notice, Mainnet rejection,
inconsistent definitions, partial state and overflow. Planning makes no writes.

The preflight is deliberately not connected to the block hook. A non-nil plan
does not validate code hashes or permit installation. The full atomic writer
must still check every runtime/authority/storage precondition before committing
the bundle, clock and final marker together. No shipped schedule enables it.
