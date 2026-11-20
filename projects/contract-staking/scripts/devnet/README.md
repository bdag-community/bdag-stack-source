# Devnet V1 -> V2 switchover scripts

Operational scripts for the DigiMount devnet (chain 8008) staking proxy. Every script reads its
parameters from **environment variables only** (Hardhat 2.28 rejects `--` passthrough args) and ends
with one machine-readable line `JSON:{...}` for the bash E2E harness.

```
npm run devnet:seed         scripts/devnet/seed-stakers.js        fund + stake N wallets from a mnemonic
npm run devnet:snapshot     scripts/devnet/snapshot-state.js      pinned-block state dump, or diff two dumps
npm run devnet:upgrade-v2   scripts/devnet/upgrade-to-v2.js       path A: UUPS upgradeToAndCall(initializeV2)
npm run devnet:verify-v2    scripts/devnet/verify-v2.js           read-only PASS/FAIL checks that V2 is live
npm run devnet:init-v2      scripts/devnet/init-v2-after-fork.js  path B: owner calls initializeV2() after the fork swap
```

All npm scripts run with `--network devnet`. Exit codes: `0` ok, `1` assertion / tx failure,
`2` precondition not met (no code at the proxy, missing env, wrong signer). Outputs land in
`out/devnet/` (gitignored); `seed.local.json` holds private keys (mode 0600).

## Environment

| Variable | Used by | Meaning |
| --- | --- | --- |
| `DEVNET_RPC_URL`, `DEVNET_CHAIN_ID` | all | node endpoint (default `http://127.0.0.1:28545`, 8008) |
| `DEVNET_OWNER_KEY` | all | signer[0] = contract owner (upgrade / initializeV2) |
| `DEVNET_FAUCET_KEY` | seed | signer[1] = faucet that funds the seed wallets |
| `STAKING_CONTRACT_ADDRESS` | all | proxy (default `0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7`) |
| `DEVNET_POOL_ADDRESS` | seed, snapshot, verify | mining coinbase = pool the wallets stake into (required for seed) |
| `DEVNET_IMPL_V1_ADDRESS`, `DEVNET_IMPL_V2_ADDRESS` | upgrade (dry run), verify | genesis / fork-table impl addresses (`0x10..11`, `0x10..12`) |
| `SEED_MNEMONIC` | seed | BIP-39 phrase, wallets at `m/44'/60'/0'/0/i` (default: fixed dev phrase in the script) |
| `SEED_COUNT`, `SEED_FUND_BDAG`, `SEED_STAKE_BDAG` | seed | wallets (5), funding per wallet (200), stake per wallet (100) |
| `SEED_OUT_PREFIX` | seed | output name (default `seed` -> `seed.json`, `seed.local.json`) |
| `SNAPSHOT_LABEL`, `SNAPSHOT_BLOCK` | snapshot | file label (`snap`), pinned block (default latest-2; every read uses it) |
| `SNAPSHOT_STAKERS`, `SEED_FILE` | snapshot | extra staker addresses (comma list); staker list source (`out/devnet/seed.json`) |
| `SNAPSHOT_DIFF_A`, `SNAPSHOT_DIFF_B` | snapshot | diff mode: compare two snapshot files and assert invariants |
| `EXPECT_IMPL_CHANGED=1\|0` | snapshot diff | impl slot must differ / must be equal (unset: report only) |
| `DRY_RUN=1`, `DRY_RUN_DEPLOY=1`, `UPGRADE_IMPL_ADDRESS` | upgrade | validate only; deploy a V2 just to estimate; reuse an existing V2 impl |
| `MODE=A\|B` | verify | `A`: `v2ActivatedAt` must be > 0; `B`: reported only (fork swap before init) |
| `EXPECT_IMPL`, `EXPECT_RUNTIME_HASH`, `VERIFY_FROM` | verify | assert impl slot / `keccak256(code)`; `from` for the `stake()` static calls |

Wallet #0 of the seed always stakes exactly `0.0015` BDAG: accepted on V1 (`MIN_STAKE` 0.001),
rejected on V2 (`MIN_STAKE` 0.002). On V2 the script logs it as `EXPECTED_ON_V2` and continues.
Seeding is idempotent: funding is skipped when the wallet holds >= half the target or has already
staked, staking is skipped when `stakerPoolShares(wallet, pool) > 0`.

## Path A: UUPS upgrade (owner tx)

```bash
npm run devnet:seed                                   # V1: wallet #0 (0.0015) is accepted
SNAPSHOT_LABEL=pre npm run devnet:snapshot            # -> out/devnet/snapshot-pre-<block>.json
DRY_RUN=1 npm run devnet:upgrade-v2                   # OZ validateImplementation + validateUpgrade, no tx
npm run devnet:upgrade-v2                             # deploy V2, upgradeToAndCall(initializeV2), check events
SNAPSHOT_LABEL=post npm run devnet:snapshot
SNAPSHOT_DIFF_A=snapshot-pre-<b1>.json SNAPSHOT_DIFF_B=snapshot-post-<b2>.json EXPECT_IMPL_CHANGED=1 npm run devnet:snapshot
MODE=A npm run devnet:verify-v2                       # version 2.0.0, MIN_STAKE 2e15, 0.0015 -> BelowMinStake, v2ActivatedAt > 0
SEED_MNEMONIC="<other phrase>" SEED_OUT_PREFIX=seed-post npm run devnet:seed   # live demo: wallet #0 -> EXPECTED_ON_V2
```

`upgrade-to-v2.js` refuses to run unless signer[0] equals `owner()`, never skips the OZ storage
check, and writes `out/devnet/upgrade-v2.json` (old/new impl, tx, block, events).

## Path B: node fork-height code swap

The node installs the V2 runtime at `DEVNET_IMPL_V2_ADDRESS` and rewrites the proxy's ERC-1967 slot
at the fork block; no reinitializer runs, so `v2ActivatedAt` stays 0 until the owner calls
`initializeV2()`.

```bash
npm run devnet:seed
SNAPSHOT_LABEL=pre npm run devnet:snapshot            # before the fork block
# ... wait for the fork block ...
SNAPSHOT_LABEL=post npm run devnet:snapshot
SNAPSHOT_DIFF_A=snapshot-pre-<b1>.json SNAPSHOT_DIFF_B=snapshot-post-<b2>.json EXPECT_IMPL_CHANGED=1 npm run devnet:snapshot
MODE=B EXPECT_IMPL=$DEVNET_IMPL_V2_ADDRESS npm run devnet:verify-v2   # runtime hash checked against out/devnet/fork-swap.v2.json
npm run devnet:init-v2                                # owner: initializeV2() -> Initialized(2) + StakingVersion(2)
MODE=A npm run devnet:verify-v2
```

`verify-v2.js` compares `keccak256(eth_getCode(impl))` with `expectedRuntimeHash` from
`out/devnet/fork-swap.v2.json` automatically when the impl slot equals its `implAddress`
(generate the file with `npm run export:artifacts`). `init-v2` is idempotent: if `v2ActivatedAt > 0`
it exits 0 without sending.

## Snapshot diff invariants

`owner`, `minerSplit`, `stakerSplit`, `genesisTime` (views and raw slots 0/1/2/8) unchanged; for every
staker in both snapshots: `stakerPoolShares` (slot 3), `stakerPrincipalBDAG` (14),
`stakerUnbondingBDAG` (17) and the unbonding requests unchanged; `poolTotalShares`,
`poolTotalAssets` and the proxy balance non-decreasing (the engine credits the pool every block);
proxy code hash unchanged; impl slot per `EXPECT_IMPL_CHANGED`. Any violation prints a `FAIL` line and
exits 1. Locked shares (slot 12), `version()`, `v2ActivatedAt()` and the impl code hash are reported.

## Local rehearsal

The whole flow can be rehearsed against a local `npx hardhat node --port 8545` by pointing the devnet
network config at it:

```bash
export DEVNET_RPC_URL=http://127.0.0.1:8545 DEVNET_CHAIN_ID=1337
export DEVNET_OWNER_KEY=<hardhat account 0 key> DEVNET_FAUCET_KEY=<hardhat account 1 key>
export DEVNET_POOL_ADDRESS=<any hardhat account>
npx hardhat run scripts/staking/deploy-proxy.js --network devnet    # prints the V1 proxy
export STAKING_CONTRACT_ADDRESS=<printed proxy>
npm run devnet:seed && SNAPSHOT_LABEL=pre npm run devnet:snapshot && ...
```

Without `--network devnet` the scripts run on the in-process hardhat network, find no code at the
proxy and exit 2 (the upgrade dry run still performs the OZ validation first).
