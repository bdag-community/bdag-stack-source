# P2P goodbye reason codes

The goodbye RPC (`/Blockdag/req/goodbye/1`) carries a `uint64` `ErrorCode`.

| Code | Name | Typical meaning when used as goodbye |
|---:|---|---|
| 0 | No error and success | Unused as a disconnect reason |
| 1 | ErrStreamWrite | Stream write failure (usually not goodbye) |
| 2 | ErrStreamRead | Stream read failure (usually not goodbye) |
| 3 | ErrStreamBase | Base stream failure (usually not goodbye) |
| 4 | ErrPeerUnknown | Unknown / unusable peer |
| 5 | ErrBadPeer | Locally classified bad peer |
| 6 | ErrDAGConsensus | Chain-state / EVM checkpoint / protocol consensus reject |
| 7 | ErrMessage | Malformed message |
| 8 | ErrGeneric | Generic protocol reject |
| 9 | ErrConnectFrequent | Connect rate / frequency reject |
| 10 | ErrSequence | Sequence / ordering reject |
| 11 | ErrRevalidate | Revalidation failure |
| 12 | ErrLibp2pConnect | libp2p connect failure |

Runtime also records `no_goodbye` when libp2p drops a session with no goodbye RPC in either direction within a short grace window.

Query live records:

```bash
# via node RPC
getGoodbyeLog
getGoodbyeLog 100
```

Logs emit `Peer goodbye` / `Peer disconnect without goodbye` at INFO.
Metrics: `p2p/goodbye/<direction>/<code>`.
