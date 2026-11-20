# Mainnet contract review candidate — 2 November 2026

Branch: `mainnet-dao-stakingv2`. This is private review source, not an approved deployment or sealed native migration.

- `MainnetLegacyStaking` preserves original V1's complete storage and public ABI. Historical manager entries no longer grant rescue powers. An additional narrowly scoped exit repair allows native-auto-stake-only operators, whose historical credits have no first-deposit timestamp, to claim only their own matured operator credits. The original 52-epoch operator lock remains; no timestamp is invented, and ordinary principal/pending exits cannot use that exception. The native migration atomically changes the original proxy's implementation and owner while retaining its address, backing, positions, old four-week user bond, three-week unbonding and saved exit dates. New V1 deposits remain technically possible; production UI hides them.
- `MainnetStakingV2` is a separate fresh proxy/implementation candidate. It reads the native versioned epoch clock and its configured reward destination. It does not call the V2-only restriction/period getters absent from original Mainnet V1. Native full/selective blocking remains required. Fresh stakes start a new four-epoch bond. New epoch-ratio policy writes never change saved user bond or pending exit dates.
- V2 and protected V1 ownership remain Governor/timelock-controlled. There is no additional multisig approval of a passed DAO action. Any genuinely required human owner/admin role needs the separately assigned three-of-five arrangement; no shared Testnet key is a production authority.
- The original `StakingPool.sol`, deployed Testnet contracts and historical runtime bundles remain unchanged. New candidate bytecode is not added to a live/default network configuration.

## Reproduce

Use Node 24.19.0, `npm ci`, then `npm test` (194 tests) and `npm run test:mainnet-migration` (four additional tests on an isolated in-process chain 1404). All public historical fixtures required by these tests are tracked and checksum-pinned; a sibling core checkout or ignored `out/` directory is unnecessary. Do not run these fixtures against an external RPC.

`node scripts/release/audit-mainnet-readonly.cjs /private/path/audit.json` performs whitelisted public reads against canonical-history-checked providers. It sends no transaction. Its14 historical blocklist keys are not a complete position/liability or selective-allowlist census.

## Mainnet-specific runtime and state qualification

- Two canonical-history-checked providers supplied identical original implementation code. `verify-mainnet-v1-source.cjs` binds UUPS `__self` to the real implementation and compares all executable/constant-table bytes. Only the exact Solidity 0.8.24 IPFS/CBOR trailer differs; the complete canonical code hash remains separately pinned. This is executable equivalence, not a claim that the full bytecode hashes are identical.
- `mainnet-migration-bundle.cjs` executes real constructors at reproducible final addresses inside the isolated EVM. It exports 19 accounts with code hashes, constructor state and nonces, including the factory's constructor-created escrow. It has no external RPC/signing configuration. Fixture reviewer identities are explicitly marked; they are not production assignments.
- `export-mainnet-migration-rehearsal.cjs` produces **review/test data only**. It never rewrites the compiled core production profile. Reproduce with `MAINNET_REHEARSAL_OUTPUT=/private/output node_modules/.bin/hardhat run --config hardhat.mainnet-rehearsal.config.cjs scripts/release/export-mainnet-migration-rehearsal.cjs` using the pinned Node toolchain.
- Matching native tests live in the core branch. They use the real old runtime and compiled new contracts on synthetic financial state: exact Legacy/restriction storage-root preservation; ordinary and operator exits; independent V2 deposits/rewards/claims; earned both-chamber voting; queue, seven-day delay, execution and proposer refund; and disk-backed EVM state/config reopen. They do not replay a complete current Mainnet native/EVM database.
- The core now wires the separately gated production installer, reward/credential/clock routing, native-layer headers and fail-closed startup/recovery behavior. New native V2-only auto-stake records its actual first-credit timestamp without rewriting Legacy or historical Testnet dates.

## Still required before release packaging and activation

The compiled Mainnet schedule and runtime bundle remain `null`, so preparation builds cannot activate this transition. Final production addresses/reviewer and human-wallet assignments, collision/current-state checks, immutable distribution/notice/layer schedule, independently reproduced Linux AMD64 artifacts and Jeremy/independent review remain required. Do not copy the test fixture into a production profile or use its reviewer accounts.

Complete canonical native/EVM clean/late replay and paired recovery still need authenticated state inputs; public RPC or this synthetic fixture is not that evidence. An incompatible late install now refuses automatic EVM rewind and requires a qualified coordinated recovery. The first production pool's 48-hour canary is a post-release/pre-adoption gate; it must finish before the published signalling start. The native miner signals registered definitions automatically inside their announced windows.

Five individual public Mainnet signer addresses will form the agreed human 3-of-5 wallet. Its eventual contract address is distinct from its signers. This does not replace Governor-owned timelocks or add a second approval step to DAO decisions; no signer is automatically assigned a registry-reviewer or bidder role.
