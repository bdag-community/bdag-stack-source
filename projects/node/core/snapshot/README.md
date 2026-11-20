# Node Bootstrap Snapshots

The `core/snapshot` package provides **fast bootstrap snapshots** for BDAG
nodes. A snapshot is a chunked, hash-verified archive of *every database*
a node needs to come online at a given tip — both the BDAG chain DB
(blocks, DAG metadata, indexes) and the embedded EVM chain data (state
trie, account snapshot, header store). A fresh node can import a
trusted archive and start accepting work without replaying a single
block.

## Sources

A snapshot bundles one or more logical databases, each identified by a
stable name. The built-in names are:

- `chain` — the BDAG chain DB (`BdagChain` sub-dir)
- `evm`   — the embedded go-ethereum chain data (`bdageth` sub-dir)

Each source contributes a contiguous range of chunks. The manifest
records those ranges so the importer can route each chunk to the
correct destination DB. A chunk never spans two sources, so partial
imports are always safe to discard.

> Why include EVM state? Without it, a freshly-imported node has every
> block but an empty EVM trie; it would still need to replay every
> block to rebuild account balances, contract storage, and the state
> root — which is exactly what the snapshot is meant to avoid.

## File format

Every archive is a self-describing `.bdsnap` file:

```
+-------------------------------------------------------------+
|  Magic       (8  bytes)  "BDAGSNP\x00"                      |
|  Version     (4  bytes)  uint32 BE                          |
|  ManifestLen (4  bytes)  uint32 BE                          |
|  Reserved    (16 bytes)  zero-padded                        |
+-------------------------------------------------------------+
|  Manifest    (ManifestLen bytes)  JSON-encoded Manifest     |
+-------------------------------------------------------------+
|  Chunks ...  repeated N times                               |
|    ChunkID        (4  bytes) uint32 BE                      |
|    UncompressedSz (4  bytes) uint32 BE                      |
|    CompressedSz   (4  bytes) uint32 BE                      |
|    SHA256         (32 bytes) of uncompressed body           |
|    CompressedBody (CompressedSz bytes) gzip-encoded records |
+-------------------------------------------------------------+
```

Each chunk body (after decompression) is a sequence of records:

```
KeyLen (4 BE) | Key | ValLen (4 BE) | Val
```

The manifest enumerates every chunk together with its sha256, plus
chain anchors (tip hash, tip order, block total, state root, network,
…). The manifest itself is summarized by a single `root_hash`: the
sha256 of every per-chunk digest concatenated in id order. A trusted
source can publish just that root hash to vouch for an entire archive.

## CLI workflow

### Produce a snapshot

Stop the source node, then:

```
bdag snap export --datadir /var/lib/bdag --path /var/lib/bdag/snapshot.bdsnap
```

`--datadir` and the network selectors (`--testnet`, `--devnet`, …) are
accepted both *before* the subcommand (where they also apply to the
running node) and *after* it. The two forms below are equivalent:

```
bdag --datadir /var/lib/bdag --testnet snap export --path /tmp/snap.bdsnap
bdag snap export --datadir /var/lib/bdag --testnet --path /tmp/snap.bdsnap
```

By default the exporter bundles both the chain DB and the EVM chain
data. Pass `--skip-evm` to produce a chain-only archive (smaller, but
the importer will have to replay blocks to rebuild EVM state).

The exporter writes both the archive and a side-car
`snapshot.bdsnap.manifest.json`. The side-car is what peers fetch when
they want to inspect a snapshot's metadata without downloading chunks.

### Verify a snapshot

```
bdag snap verify --path snapshot.bdsnap
```

Runs a full re-hash pass without touching any database. Use this on
both ends when handing off a snapshot between operators.

### Import a snapshot

Wipe the destination data dir first (snapshots refuse to overwrite
existing state):

```
bdag --cleanup --datadir /var/lib/bdag
bdag snap import --datadir /var/lib/bdag --path snapshot.bdsnap
```

The importer peeks at the manifest up front: if the archive contains
an `evm` source it opens the EVM chain DB in-place; if not, it only
touches the chain DB. This means a chain-only snapshot never creates
an unnecessary `bdageth` sub-directory.

The importer refuses to proceed if:

- the manifest's network does not match `params.ActiveNetParams.Name`
  (override with `--no-network-check` during testing),
- any destination DB is non-empty (wipe the data dir first), or
- the manifest names a source the importer does not know how to route
  (future-proofing against new source types).

### Live, supervised node

The workflow above requires stopping the node process yourself first. When
the node is running under `nodeworker` (the standard pool-stack deployment),
`nodeworker` can pause and resume it for you instead:

```
nodeworker snap export --path /var/lib/bdag/snapshot.bdsnap
nodeworker snap import --path /var/lib/bdag/snapshot.bdsnap
```

These are client commands: they connect to the control socket of an
already-running `nodeworker` process (`<persist-root>/control.sock` by
default, override with `--control.socket`), which stops the supervised
`bdag` child, runs the equivalent `bdag snap export`/`import` against the
now-idle data directory, then restarts the child and waits for it to become
ready again — the same cycle `nodeworker`'s automatic hourly snapshot loop
(`--snapshot.enable`) already performs on a timer, triggered on demand
instead. The command blocks until the whole cycle finishes, which can take
minutes for a large chain DB.

`nodeworker stop` is the same idea for a plain, guaranteed-clean shutdown
with no export/import and no restart afterward:

```
nodeworker stop
```

It's for callers that need to tear the whole node down externally (e.g. a
container orchestrator stopping the container) but still want the chain
database closed cleanly first. Sending `SIGTERM`/`docker stop` straight to
the container races nodeworker's own internal shutdown grace period against
whatever (often shorter) grace period the outer caller uses — if the outer
timeout fires first, the node gets hard-killed mid-shutdown, and any
database state that hadn't been flushed to disk yet at that instant is
lost. `nodeworker stop` avoids that race entirely: it blocks until
nodeworker's own `Stop()` has actually finished (SIGTERM to the child, full
shutdown budget, hard-kill fallback only past that), so by the time the
command returns the database is known to be closed — only then is it safe
for the caller to stop or remove the container itself.

### Inspect a manifest

```
bdag snap info --path snapshot.bdsnap
bdag snap info --path snapshot.bdsnap.manifest.json
```

Both archive headers and stand-alone JSON manifests are accepted.

## Discovery surface

Local snapshot archive discovery over node RPC is retired. Chain archive
discovery for recovery/install flows belongs to the IPFS/rawdatadir sidecar
manifest, where append-only segments can be verified before they are used to
seed local chain data.

## P2P archive surface

Node-to-node archive and range-transfer bootstrap is retired. Recovery/install
flows belong to the IPFS/rawdatadir sidecar path, where signed append-only
segments are verified before they are used to seed local chain data.

## Design notes

- **Integrity is per-chunk.** Chunks can be downloaded in any order from
  multiple peers and verified incrementally. A single bad chunk never
  contaminates the destination DB because the importer refuses to apply
  a mismatched chunk.
- **Root digest is deterministic.** Two nodes producing a snapshot at
  the same tip on the same network will produce archives with
  identical root digests (assuming the same chunk target and
  compression level). This makes snapshot reproducibility a first-class
  property.
- **Destination safety.** Imports require an empty destination by
  default so we never write partial state over a healthy DB.
- **Point-in-time.** The exporter iterates the KV space of a quiesced
  DB. The caller is responsible for ensuring the node is stopped, or
  that the DB is read-only, while `bdag snap export` runs.

## Future work

- Incremental snapshots: emit only the diff between two tips to
  accelerate subsequent bootstraps.
- Pruned EVM snapshots: currently the `evm` source is a full key-space
  dump of the EVM chain DB. For path-scheme state we could instead
  export only the reachable trie + flat snapshot at the target state
  root (à la Ethereum snap sync) to cut archive size dramatically.
