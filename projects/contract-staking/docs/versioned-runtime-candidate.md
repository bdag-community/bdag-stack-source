# Canonical v0.2 runtime candidate

Status: locally tested, uninstalled and not an operator release.

`scripts/phase1/export-versioned-runtime.cjs` builds only
`out/testnet-timing-v1-candidate`. Use the local Hardhat configuration with
`BDAG_FIXTURE_CHAIN_ID=1043` and the supported pinned Node/compiler toolchain.
It never connects to a live RPC or rewrites the historical export directory.

- Input is the exact historical 19-account allocation, SHA-256
  `304aeee52c451e383f1960bdc32d0415d1ad9bc3e0fe19dff95af197bc82d199`.
- Eight runtimes replace code at the same addresses: staking implementation,
  reader, both credentials, both owner timelocks, Governor and escrow factory.
  No existing storage or deployment nonce is included as a replacement write.
- Two new candidate destinations are reserved: timing module
  `0x1000000000000000000000000000000000000018` and escrow implementation
  `0x1000000000000000000000000000000000000019`. Native installation must prove
  they are empty. The new escrow implementation has disabled initializers.
- The other eleven runtimes stay byte-for-byte identical, including the old
  escrow implementation used by old clones and old approved project actions.
- Compiler AST immutable references bind canonical addresses and the original
  chain-1043 signing domains. Every immutable must have an explicit value.
  No missing reference is silently left zero. All runtimes fit EIP-170/London.
- The original epoch finishes at 60 seconds. Governor scalar constructor
  getters therefore describe 60/26/60 seconds, not the upcoming 30-minute
  policy. APIs must use canonical epoch bounds and each proposal's frozen terms.
  Legacy terms are separately pinned at 60/259200/604800 in the timing module.

Isolated tests import the original allocation, replace only these runtimes and
the new clock words, then compare every original allocation storage word. They
check roles, bindings, old funding implementation, both epoch lengths and a
signature prepared before replacement, including nonce-replay rejection.
The chain-1043 test explicitly exercises the cached signature domain. Full
contract regression passes 152 tests. This is not a live-state reconciliation,
independent build comparison, constructor sandbox proof or native replay test.

The factory's new implementation immutable intentionally points to the fresh
reserved address, not a new child created from the existing factory nonce.
Its original nonce and project mapping must not be reset. Constructor sandbox
verification must account for that relocation and preserve the old child code.

The bundle declares `installationAuthorized: false` and has no activation
schedule. Next: native atomic writer with exact code/authority preconditions,
two-second target rule, application timing consumers, isolated full upgrade and
replay/recovery, reproducible build and signed private operator packaging.
