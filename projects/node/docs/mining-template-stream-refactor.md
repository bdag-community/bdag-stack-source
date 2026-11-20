# Node mining template stream refactor

_July 2026._ This describes the refactors made to the node→pool mining template
stream after it was found discarding ~100% of solved blocks on a live RC. It
spans two repositories: `blockdag-corechain` (the node/miner) and `pool`
(the stratum pool and its new `poolworker` supervisor).

## The pipeline

```
node miner ── builds template ──▶ ws blockTemplate feed ──▶ pool WS consumer
    ▲                                                             │
    │                                                     builds stratum jobs
    │                                                             ▼
    └──── submitBlockHeader ◀── pool ◀── solved share ◀──── ASIC miners
```

The node's miner builds a block template, publishes it on a websocket feed
(`Blockdag_subscribe("blockTemplate")`), the pool turns each template into
stratum jobs for the ASICs, and solved blocks come back via `submitBlockHeader`.
Every stage had a defect that compounded into total block loss.

## Symptom

On the RC, blocks were being found but every submission was rejected by the node
with `code=tip_overdue`. Over a representative 3-minute window: 36 blocks found,
0 accepted. The pool reported itself healthy throughout.

## Root causes and fixes

### 1. The pool consumed the template feed serially (the dominant cause)

**Repo:** `pool` — `internal/pool/server/jobBroadcast.go`

The pool read one websocket notification and then did the full
`processTemplateUpdate` (fetch GBT, build jobs, broadcast to every miner) in the
same loop before reading the next. The node publishes a template on every
rebuild — roughly 1/sec — but the pool drained them at ~0.18/sec. The unread
notifications piled up in the socket buffer, so the pool was always broadcasting
jobs built from templates the node had published **minutes earlier**.

Measured on the live node: publish→pool-receipt delay grew linearly at ~0.81 s/s
from zero at each restart (exactly `1 − 0.18/0.96`), reaching a median of 280s.
This is why acceptances briefly reappeared after every restart and then decayed —
restarting reset the backlog to zero.

The node's own API contract already said subscribers **MUST** discard any push
whose `Seq` is lower than the highest already seen. The pool decoded `Seq` into
`SourceSeq` and never checked it.

**Fix:** drain the socket at line rate in a dedicated goroutine into a one-slot
latest-wins buffer (`queueLatestTemplatePush`), and enforce the monotonic `Seq`
contract. Only the newest template has value; superseded ones are dropped.

### 2. The node's submit-side template match evicted too aggressively

**Repo:** `blockdag-corechain` — `services/miner/miner.go`

`submitBlockHeader` accepts a header only if it still matches the node's current
template or one of a few recently-superseded ones. That retention was a
**count-bounded ring of 6**. Templates are rebuilt on DAG, mempool, and stall
events — far faster than a miner solves a job — so the 6-slot window covered only
~3 seconds. A header solved against a template evicted in the meantime was
rejected as `You're overdue`.

**Fix:** replace the count-bounded ring with a **time-bounded** retention
(`recentTemplateRetention`, 60s) capped by `recentTemplateMaxEntries` (256), with
de-duplication so a rebuild that changes nothing miner-visible refreshes the
existing entry instead of aging out a distinct one. A count-based window shrinks
to nothing exactly when template churn is highest — the wrong behaviour.

### 3. A self-amplifying rebuild on the reject path

**Repo:** `blockdag-corechain` — `services/miner/miner.go`

The `tip_overdue` reject path called `go m.invalidateForSubmitObsoleteDepth(0)`,
force-rebuilding the template. A hardcoded `0` — no evidence the template was
actually stale. Each rejected submit evicted another retained template and
force-notified the pool, invalidating in-flight jobs mid-solve. A cache miss was
being treated as proof of staleness, and any miner could trigger it.

**Fix:** removed. The remaining invalidation in `submitBlock` is driven by a real
obsolete depth parsed from a chain error, which is actual evidence.

### 4. Mempool churn drove excess rebuilds

**Repo:** `blockdag-corechain` — `services/miner/miner.go`

The template was rebuilt whenever the pending-transaction count changed — every
arrival and removal — producing ~112 rebuilds/60s, most of them same-parent
templates the pool was never even told about, each evicting a retention slot.

**Fix:** throttle mempool-driven rebuilds to at most one per
`mempoolTemplateRebuildMinInterval` (500ms). Parent changes, stalls, and forced
rebuilds are never throttled. A deferred rebuild is armed so a throttled change
is still picked up promptly on an otherwise idle chain.

### 5. The submit depth pre-filter was too tight

**Repo:** `blockdag-corechain` — `services/miner/miner.go`
(`CheckSubMainChainTip` / `submitMainTipLagTolerance`)

`CheckSubMainChainTip` refused a submit whose main parent trailed the main chain
tip by more than a tolerance. The original code allowed a lag of **1**. On a
1-second block chain, any miner taking a few seconds to solve is several blocks
behind, so this rejected essentially every pool block. It was first raised to
`StableConfirmations` (10).

Under live load, once the feed backlog (cause 1) was fixed and template age fell
to ~3s, the remaining rejects clustered at **obsolete depth 11–18** — the natural
submit-pipeline latency (template age + ASIC solve + submit) — just above 10.
This is a DAG: a block built on a slightly-older parent is a valid side block,
and this filter only gates the node's own submits; `ProcessBlock` and peers still
validate.

**Fix:** raised `submitMainTipLagTolerance` to **32**, clearing the observed
range with headroom.

### 6. poolworker start-gate deadlock

**Repo:** `pool` — `cmd/poolworker`

`poolworker` (the new in-process pool supervisor; see below) gated startup on the
node reporting `mineable_now=true`. But the node's miner enables **lazily**, on
the first `getBlockTemplate` request (`GBTMining` sets `cfg.Miner=true`). A synced
node that no pool has poked reports `mineable_now=false, reason_code=miner_disabled`.
So the pool waited for the node to be mineable, and the node could not become
mineable until the pool asked for a template — a deadlock. (The old stack avoided
it because the pool started unconditionally and poked the node.)

**Fix:** gate on `get_block_template_ready` instead — the node's low-cost sync
preflight, true once it is synced enough to build templates, computed
independently of the miner-enabled flag. The pool's first template request wakes
the miner.

## Result

On the refactored stack, a representative 3-minute window: **11 blocks found, 11
accepted, 0 rejected, 0 template mismatches**, with template age holding at
2.7–4.2s. From 0% to 100% acceptance.

## Tuning constants

| Constant | Value | Where |
|---|---|---|
| `recentTemplateRetention` | 60s | corechain miner |
| `recentTemplateMaxEntries` | 256 | corechain miner |
| `mempoolTemplateRebuildMinInterval` | 500ms | corechain miner |
| `submitMainTipLagTolerance` | 32 | corechain miner |
| pool WS push buffer | 1 (latest-wins) | pool jobBroadcast |
| poolworker start gate | `get_block_template_ready` | pool poolworker |

## Related supervision change

`poolworker` replaces the external watchdog's pool-start lease and pool restarts.
It runs as PID 1 with `mining-pool` as its child, gates startup on the node's own
`getTemplateHealth`, and probes template freshness by comparing the pool's
current parent against the node's recently-published parents — the one signal
that would have caught the feed backlog, since the pool's own broadcast age
looked healthy throughout. It restarts the child with a graceful stratum drain,
which an external `docker restart` cannot do.

## Open items

- **RPC exposure.** The gateway relays mining methods to peers over HTTP on an
  internet-exposed `rpclisten=0.0.0.0:38131` with a shared credential, drawing a
  constant credential brute-force. Moving peer relay onto the existing
  authenticated libp2p block-broadcast, and binding RPC to localhost, would close
  it. Deferred — needs a coordinated network rollout.
