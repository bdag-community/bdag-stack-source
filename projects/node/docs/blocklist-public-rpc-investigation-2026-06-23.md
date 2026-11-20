# Blocklist Public RPC Investigation - 2026-06-23

## Bottom Line

The blocklist and selective-blocklist upgrades are locked in and the local upgraded node is enforcing them.

The public EVM RPC/explorer view is inconsistent with that enforcement: it returns successful EVM receipts for transactions that the upgraded local native node marks `txsvalid=false` because they involve fully blocked addresses.

This is not explained by the selective whitelist. The reproduced addresses are fully blocked in `BlocklistGovernance`, and full blocking takes precedence over selective-blocklist rules.

## Consensus And Contract Facts

- Upgrade definitions:
  - `WalletBlocklist`: bit `0x1`, start layer `8,692,800`, window `2,000`, threshold `1,500`, activation delay `2,000`.
  - `SelectiveBlocklist`: bit `0x2`, same start/window/threshold/delay.
- Local logs show both upgrades locked in at window `8,746,800-8,748,799` with `1,514` yes layers, above the `1,500` threshold.
- Activation anchor from that earliest lock-in window is `8,750,799`.
- Native blocklist transfer enforcement layer is `11,350,500`, also passed.
- Full blocklist contract:
  - `0xe628505d5cB6F5F4f9a25Cd5c80Caa198cc645a0`
- Selective blocklist contract:
  - `0xc0a42A43A05e39C3a4165CA98440D516959f41D9`

Relevant code:

- `bevm/bdag/governance_admissibility.go`:
  - `ConsensusGovernanceAdmissibilityAtLayer` gates wallet and selective enforcement by layer.
  - `CheckTransaction` rejects blocked sender or blocked recipient before selective whitelist checks.
- `bevm/bdag/blocklist.go`:
  - Consensus reads `blocked`, `restricted`, and `allowedTo` directly from contract storage.

## Public RPC State Checks

At public EVM block `12,071,917` (`0xb833ed`):

- Full blocklist event scan:
  - `20` full-blocklist events total.
  - `14` currently active full-blocked addresses.
- Active full-blocked addresses observed:
  - `0x04dcf6a45e169020ed4ae78a3b627c5aada35355`
  - `0x14143445822f03796a7301b0ce5b437d3070474d`
  - `0x1b277f2edfc4fdf732e4d683f7c12c832a6bcb89`
  - `0x35e7f8f1031b2bf364a88fde7fdda80fce8c3bbe`
  - `0x4938df5b0eaf8130ca128e480812c2cc5c70b6c4`
  - `0x558999486d976ec2ddc18da9a97c7ccb78d9bd18`
  - `0x68f4cf37ce5be16b3076761020f42685a544e2e9`
  - `0x74c0e51805674c4b482f749ebdedf78c816458ec`
  - `0x86790abbaccd1b21f5ecfdaa67ec6282afbf3e83`
  - `0xb685760ebd368a891f27ae547391f4e2a289895b`
  - `0xc0ac84130313a5dd78097c62516a9d1f9f47b204`
  - `0xd8a5d6df7f3a1d57cdc9661f4a8132e9206b00d5`
  - `0xf1c4393fc08a965034063d637e3c965048a0e5f2`
  - `0xf417719986a694a71c3ac3095c103802846c3ff4`

Selective blocklist event scan:

- `Restricted` events: `1`
- `Unrestricted` events: `0`
- `AllowedToSet` events: `8,335`
- Active restricted sender:
  - `0x4f1536fc181c541f3ef766d227373f55d03ce0ba`

The suspect addresses checked in this investigation are full-blocked and not selectively restricted:

- `0xc0Ac84130313a5Dd78097c62516a9d1F9F47b204`: blocked `true`, restricted `false`
- `0x14143445822f03796a7301b0ce5b437d3070474d`: blocked `true`, restricted `false`
- `0xd8A5D6Df7F3A1D57cdc9661F4a8132e9206b00D5`: blocked `true`, restricted `false`
- `0x558999486d976ec2ddc18da9a97c7ccb78d9bd18`: blocked `true`, restricted `false`
- `0x86790abbaccd1b21f5ecfdaa67ec6282afbf3e83`: blocked `true`, restricted `false`
- `0xF1c4393fC08A965034063d637e3C965048A0e5F2`: blocked `true`, restricted `false`
- `0x04dCf6A45E169020ed4AE78A3B627c5aADa35355`: blocked `true`, restricted `false`

## Reproduced Public RPC Contradictions

### Transaction `0xd9399019f6dc3dff1822a48b445f37ea9059e91e55ed17830b03146fb59b7c07`

Public EVM RPC:

- Receipt status: `0x1`
- EVM block: `12,071,015` (`0xb83067`)
- From: `0xc0ac84130313a5dd78097c62516a9d1f9f47b204`
- To: `0x998c8a9a78c0d9071844882a40b69a5f3ea2ea25`
- Public `eth_call` to `isBlocked(from)` at block `0xb83067`: `true`

Local upgraded native node:

- Native order: `12,427,460`
- Native block hash: `0xb601f68ed635e3a2fa7f6c33ba10193d28d01dfab7e63b137e2e95413ec4b0cc`
- Timestamp: `2026-06-23T21:21:33Z`
- `txsvalid`: `false`
- Local log: `address is blocked by on-chain governance blocklist: sender 0xc0Ac...b204 ... order=12427460`

### Transaction `0xbdf7c709e556bef893da914f9d4af9ef7437c81382adc00de1852d4e45295ea3`

Public EVM RPC:

- Receipt status: `0x1`
- EVM block: `12,070,002` (`0xb82c72`)
- From: `0xc0ac84130313a5dd78097c62516a9d1f9f47b204`
- To: `0x643c14ae99677378ccd543a91cee384654c9ad84`
- Public `eth_call` to `isBlocked(from)` at block `0xb82c72`: `true`

Local upgraded native node:

- Native order: `12,426,418`
- Native block hash: `0xd4f1c31469c6377407495d06de51ce7e4d8c27c791f9e7a47a7fd25ce70913e7`
- Timestamp: `2026-06-23T21:07:07Z`
- `txsvalid`: `false`
- Local log: `address is blocked by on-chain governance blocklist: sender 0xc0Ac...b204 ... order=12426418`

## Diagnosis

The report's "0 included failed outgoing transactions" finding is expected and is not a valid test of blocklist enforcement. Consensus or admissibility rejections do not become included EVM receipts with `status=0`; included failed receipts are EVM execution failures.

The stronger report finding is the successful public outgoing activity. That is reproduced and confirmed:

- Public RPC/explorer has `status=1` receipts from full-blocked addresses.
- The blocklist contract state says those senders are blocked at the exact EVM block of the receipts.
- The local upgraded native node marks the corresponding native blocks as `txsvalid=false`.

So the likely fault is not the governance contract state and not the local upgraded enforcement code. The fault is a split between the public EVM RPC/indexer view and upgraded native consensus enforcement.

Likely operational causes to verify:

1. Public RPC/indexer fleet is not running the enforcing binary or not reading the enforcing native validity state.
2. Public EVM indexer is indexing transaction payloads from native blocks even when `txsvalid=false`.
3. Some block producers are still producing blocks containing blocked-address payloads, and upgraded nodes invalidate the payloads while public EVM still surfaces them as successful receipts.

## Recommended Next Steps

1. Confirm the exact binary/version running behind `rpc.bdagscan.com` and its indexer.
2. Make the EVM RPC/indexer honor native `txsvalid=false`; invalidated tx payloads must not appear as successful EVM receipts.
3. Rebuild/reindex the public EVM data from at least the blocklist/native enforcement activation region, or from a checkpoint before `11,350,500`.
4. Verify block producers and template-generation nodes are upgraded and reject blocked senders/recipients before block submission.
5. Keep using native RPC `txsvalid` as the consensus-validity source while the local EVM RPC is still syncing.

## Local RPC Caveat

The local EVM RPC was still syncing during this investigation:

- Local EVM `currentBlock`: `0xb8270d`
- Local EVM `highestBlock`: `0xb83400`

Because of that, local EVM latest reads are not authoritative yet. The local native RPC block/order checks are the relevant consensus-validity checks used above.
