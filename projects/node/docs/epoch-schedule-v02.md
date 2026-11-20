# v0.2 reward clock: implementation boundary

Status: atomic native runtime/clock writer and prospective difficulty tested,
**not an enabled or complete upgrade**.

## Decision

Keep historical epoch numbers and boundaries. A schedule is an ordered list of
`(firstEpoch, startTime, duration)` segments. Its first entry describes the old
clock. Every later entry starts exactly at an old-policy epoch boundary. It may
change subsequent durations, never renumber or reinterpret earlier epochs.

`common/epochclock` owns pure, checked uint64 arithmetic. Both the native engine
and the Solidity `EpochSchedule` library use the same JSON vectors. The persisted
reward epoch is a floor when native timestamps move backwards. Historical
financial/operator epochs are a separate clock and are not changed here.

## Storage and contract boundary

- ChainTally slots 0–3: existing historical financial tally, unchanged.
- Slots 4–7: existing reward epoch, bootstrap/first-complete markers and reward
  totals mapping, unchanged.
- Slot 8: reserved for the length of an append-only dynamic schedule array.
- Array data: `keccak256(uint256(8)) + 3*i`, followed by the next two words.
  Each segment uses three full uint256 words. Values outside uint64 are rejected,
  not truncated. At most 64 segments may be loaded.
- `rewardEpochScheduleFromState` reads this layout. The pre-block hook now uses
  it only when the distinct testnet timing migration marker exists. No shipped
   manifest selects that writer yet. Missing/malformed activated state
  fails closed, never falls back to the old clock.
- The matching candidate `VersionedChainTallyReader` preserves the old reader
  layout, exposes `epochStatus()` and `epochBounds(epoch)`, and has no mutable
  wallet/administrator entrypoint. It is not included in the old bootstrap.
- Historical bootstrap allocations, bytecode and RewardGovernanceV3 definition
  remain pinned to their shipped contents. Recompilation of source is not licence
  to overwrite those historical artifacts.
- Slot 9 persists the monotonic governance timestamp. The marker-gated pre-block
  hook validates the entire clock/eligibility state before any write, then
  advances the existing reward epoch/first-complete marker and the time floor.
  Contracts and both reward chambers therefore observe one rollover. Finalize
  revalidates idempotently before balance changes. Unmarked historical blocks
  retain their original accounting. Financial/operator epochs stay unchanged.

## API boundary

The backend's new, explicit version-3 reader obtains epoch/start/end/duration and
schedule version at one fixed block, then verifies the block hash again. It does
not infer a new deadline from `genesis + epoch * currentDuration`. Invalid, expired
or unavailable metadata is rejected. It must not fall back to an old clock after
a failed new-interface read. Existing API consumers still use version 2 until a
qualified release manifest selects the new contract interface.

## Required before activation or distribution

1. A distinct, immutable native transition. On 8 October Francois explicitly
   approved preparing a one-time **Testnet-only timing correction through miner
   signalling**, instead of waiting for the old deployed seven-day DAO delay.
   This narrowly scoped rehearsal exception must never apply on Mainnet or
   become a reusable administrator bypass. Retain fresh 1,500/2,000 signalling,
   at least 2,000 delay layers and 24-hour notice. Do not repeat V3's bootstrap
   or change its historical definition. No installation or signal is performed
   by this preparation approval.
2. Atomic schedule/runtime installation, with old-state/code checks before any
   writes. Choose a future epoch boundary that cannot reassign already credited
   rewards. Test missed boundaries, rewinds and restart/replay.
3. Wire both reward chambers, credentials, staking and Governor to the same
   schedule. Freeze existing proposal deadlines and existing lock/exit
   entitlements. New testnet policies must not authorize their own transition.
4. Implement the two-second native target through history-aware difficulty and
   DAG/template handling. Do not edit global historical timing constants.
5. Wire manifest-selected APIs/frontends, regenerate new ABIs, and qualify the
   whole isolated upgrade, including CPU/ASIC mining and historical replay.

Francois' 8 October correction replaces the earlier fixed-second delay proposal:
all new governance waits derive from epochs. Ordinary delay is 3/7 of an epoch
(rounded up), critical delay is one epoch, and action grace is two epochs. With
the proposed 1,800-second Testnet epoch these are 772/1,800/3,600 seconds. With
the seven-day Mainnet epoch they retain three-/seven-day delays and fourteen-day
grace. Existing proposals, queued timestamps and financial entitlements retain
their original terms. The candidate Governor and both owner timelocks must be
replaced atomically with the clock; changing a network configuration alone is
insufficient. These policies are not enabled on any live service by this change.

## Native-clock implementation contract, 8 October

The new marker is `keccak256("bdag.testnet.timing.v1.activation")` at ChainTally.
This is an internal consensus marker, not a wallet-settable activation switch.
The transition that writes it is still required and must atomically pin/check
all contract runtimes, old authority and native signalling/notice gates. The
one-time Testnet exception above replaces the old-policy executed approval only
for this specific timing transition. A marker without the schedule fails
validation. Existing proposals, queued timestamps and financial rights remain
unchanged, and further/Mainnet releases still need their normal authorization.

Local checks cover pre-transaction rollover, both reward ledgers, exact mining
attribution and staking residual conservation, original eligibility and operator
maturity, backward timestamps, invalid/overflowing state before mutation,
idempotent replay, and candidate-state rewind. These do not replace full
native activation, P2P, crash/restart or independent review acceptance.

## Native candidate integration, 8 October

The shared builder/importer pre-block path now invokes the one-time writer.
It validates the pinned runtime-bundle SHA-256, all 19 historical code hashes,
owner/proxy/retired-key storage, empty new destinations and both activation
gates before writing. It replaces eight runtimes, creates two at reserved
addresses and appends the clock boundary without changing existing balances,
nonces, user storage or timelock operations. A separate manifest commitment
prevents a repeated call with changed policy. No shipped schedule selects it.

Local tests compare builder/importer roots, parent replay and rewind; execute
canonical runtime views in the native EVM; and verify 60 to 1,800 seconds at the
next boundary, with new ordinary/critical/grace waits of 772/1,800/3,600 seconds.
Constructor-equivalence and full isolated upgrade qualification remain required.

Prospective native difficulty uses an immutable copy of Testnet parameters only
after the persisted native anchor and the selected parent's median timestamp
both qualify. Templates and importers call the same function. Historical global
parameters and DAG initialization are unchanged. The target becomes two seconds,
not a promise that every block arrives exactly two seconds apart. Retarget tests
prove twice the work on a one-second history and no history mutation. Full
CPU/ASIC and multi-node activation acceptance remains outstanding.

An inherited Scrypt `CalcEasiestDifficulty` checkpoint sanity path divides by
zero. This testnet has no native PoW checkpoints and does not enter that path;
adding such checkpoints requires a separately reviewed repair. This does not
disable or change native/EVM state-root validation. The finding is retained in
the release issue log, not counted as a successful checkpoint-path test.
