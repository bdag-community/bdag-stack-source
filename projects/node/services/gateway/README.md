# Gateway JSON-RPC Routing

This gateway is the thin HTTP/JSON-RPC layer that sits in front of a BDAG node. It makes each request “sync aware,” forwards calls to the best available peer when the local node is behind, and keeps follow-up calls for a pending transaction pinned to the peer that first accepted it.

## Request Lifecycle
- Pre-flight and guards: `OPTIONS` is answered with CORS headers; optional inbound basic auth (`gateway.inbounduser/pass`); per-IP rate limiting (`gateway.ratelimit`/`gateway.rateburst`); optional max body size (`gateway.maxrequestbytes`).
- Body read/parsing: request body is read (with `RequestTimeout` context) and method/first param are inspected so the gateway can spot transaction hashes early.
- Target selection:
  1) Transaction pinning: if the call carries a known tx hash, the cached peer that saw the tx is reused.  
  2) Sync awareness: if no cache hit and the gateway believes the local node is syncing, it asks the `PeerManager` for the best healthy peer.  
  3) Fallback: otherwise the request goes to the local RPC (`LocalRPC`).
- Proxying: the request is POSTed to the chosen target with upstream basic auth when needed (DAG/peer ports). Response headers are mirrored back, CORS headers are added, and responses are streamed directly to the client to support large block/log responses without memory exhaustion. Errors for streamed responses are logged. Connection reuse is tuned for high RPC volume.

## How “Syncing” Is Decided
- The gateway calls `isCurrent` on the local DAG RPC (`LocalDagURL`). If that returns false or fails, it treats the node as syncing.
- For EVM gateways it also tries `eth_blockNumber` on `LocalRPC`; failure is treated as syncing. When DAG is current and the local height is readable, the gateway assumes “not syncing” (it does not currently compare the local head to peers).

## Peer Discovery and Scoring
- Discovery: every ~30s the `PeerManager` calls `getPeerInfo` on the local DAG RPC, normalizes each peer address (multiaddr or host:port) to a bare host, and builds a host list.
- Probing each host:
  - DAG health: `isCurrent` on the peer’s DAG port (`PeerDagPort`).
  - EVM health (when `IsEVM`): `eth_blockNumber` on `PeerEvmPort` with retries/backoff; the returned height is stored.
  - Latency is measured per probe; peers that are DAG-current and (for EVM) returned a block height are marked active/synced.
  - Concurrency is limited (default 10) to prevent resource exhaustion during probing.
- Picking a peer: `GetBestPeer` sorts active+synced peers by highest block height, then lowest latency. If a proxied call to a peer fails, `ReportFailure` marks it inactive until the next probe cycle.

## Transaction Pinning
- When `eth_sendRawTransaction` succeeds against a remote peer, the tx hash is cached with that peer URL (`TxCacheTTL`, default 5m; `TxCacheMax`, default 10k). A background pruner drops expired/old entries.
- Follow-up calls containing that tx hash (e.g., `eth_getTransactionReceipt`) are routed to the same peer; once a receipt is found remotely, the pin is removed so later calls can go to any node.

## Security, Limits, and Timeouts
- Inbound basic auth is optional; upstream auth is applied to DAG/peer ports when `UpUser/UpPass` are set.
- Per-IP rate limiting via an LRU-backed limiter cache; request body size can be capped.
- HTTP server timeouts default to 10s for read/write/idle; outbound client requests default to 10s and use a high-connection reuse transport to handle bursty traffic.
- CORS is configurable (origins, methods, headers, credentials, max-age).

## Configuration Highlights
- `ListenAddr`: gateway bind address/port.
- `LocalRPC` / `LocalDagURL`: local JSON-RPC endpoints.
- `PeerDagPort` / `PeerEvmPort`: ports the gateway targets on peers.
- `InboundAuth` + `InboundUser/InboundPass`: client→gateway auth.
- `UpUser/UpPass`: gateway→node/peer auth for DAG/EVM ports.
- `RequestTimeout`, `HTTPTimeout`, `MaxRequestBytes`, `RateLimit`, `RateBurst`, `TxCacheTTL`, `TxCacheMax`, and CORS fields tune performance and exposure.

## Operational Notes
- If no healthy peer is available while the node is syncing, the gateway falls back to the local RPC.
- For DAG gateways, startup probes ensure the DAG port is reachable before serving.
