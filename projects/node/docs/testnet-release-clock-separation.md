# Testnet release: reward epochs and preserved financial time

- Status: implemented for qualification, not an available release.
- Decision: retain RC2's block-based native financial clock. Only the separately activated reward-voting ledger uses 60-second testnet epochs.
- Reason: changing the global clock rewrites historical state and can mature old operator locks early. The archived v1.4 snapshot rejects block 1 under the global 60-second clock, while the preserved clock replays its first 4,000 EVM and native blocks exactly.
- Staking: the dedicated testnet artifact reads the native financial epoch for operator claims. New testnet user staking and reward votes use the short clock. Production's seven-day staking artifact and historical fork-53 bytecode remain separate.
- Time regression: both reward chambers use the persisted reward-epoch floor, so an older block timestamp cannot reopen a completed epoch. Voting starts after the first complete post-bootstrap epoch.
- Schedule: the preparation source embeds `null`, not the expired September rehearsal. A distributable candidate requires a newly reviewed, compiled schedule. There is no runtime schedule override. Native signalling and the EVM transition derive from the same schedule and retain 1,500/2,000, 2,000-layer delay and 24-hour testnet notice requirements.
- Artifacts: revision-7 bootstrap allocation and all 19 contract runtimes are hash-pinned. This replaces only the abandoned testnet bootstrap candidate, not the historical private-devnet/fork-53 artifacts. Do not run this build against the archived activated v1.4 databases.
- Alternatives rejected: shorten all historical epochs, remap existing auto-stakes into the fast clock, disable checkpoint validation, or silently reuse an expired activation boundary.
- Verification: `testnet_epoch_transition_test.go`, `testnet_release_test.go`, `archived_v14_clock_replay_test.go`, `consensus/archived_native_replay_test.go`, and the contract clock/storage/legacy-exit tests. Archived/captured replay tests require the documented local evidence environment variables and never contact the network.
- Still required: full post-upgrade native replay, isolated multi-node upgrade, independent review/build comparison, signed publication and operator readiness. Passing these regression tests alone is not release approval.
