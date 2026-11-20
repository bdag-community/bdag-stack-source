# Core HTTP/2 dependency graph qualification

System51 #49/#564/#81. Parent is private SDK candidate c1883ab6304ae3b7015080734049a8a14bfd98bb, Go1.26.9 and language floor1.26.0. This candidate removes affected x/net version metadata without claiming that earlier module-only warnings proved a reachable HTTP/2 defect.

## Discovery and exact graph
Authenticated official public proxy/sumdb modules: x/net0.60, crypto0.57, sys0.48, term0.46, text0.42, mod0.41, sync0.23. x/text's requirements account for the last two; all seven require Go1.26.0. Existing source go.mod/toolchain lines and all old go.sum entries retained,14 exact authenticated sum lines added. No replace override, tidy-driven dependency removal, source/runtime policy change or scanner waiver.

Before remediation the all-command source scan at parent c1883ab covers31 packages/22 main commands:5 HTTP/2 advisories and OpenPGP are MODULE-only, DHT has95 conservative symbol traces; no source/runtime/security clearance. The frozen official feed lists x/net0.60 as fixed for the5 HTTP/2 ranges. Full source tests/build/architecture/library scope remain separate.

## Affected contracts
Public module file comparison finds crypto implementation bytes identical (only mod/sum changed). Core uses sha3/blake2b/ripemd160/pbkdf2/scrypt across hash, identities, blockchain/EVM and key helpers. Existing hash/crypto/wire/type tests qualify vectors and serialization; no operational wallet, signer or key custody is introduced.

Core's direct x/net/context imports in mining/crawler preserve unchanged context package bodies. x/net also changes DNS/IDNA/HTTP/WebSocket/transport implementations; command loading/linking and fresh isolated native lifecycle must verify the actual tuple. Static callgraphs alone do not prove networking behavior.

x/sys changes CPU initialization and Unix implementations/definitions. Existing native hash, descriptor/recovery/shutdown/crawler seed controls are affected. Linux/amd64 qualification does not clear other architectures.

x/text changes normalization composition/transform/table handling. Local EVM scwallet Pair consumes NFKD for pairing inputs. Qualification must compare a declared public Unicode corpus across both exact module graphs and preserve any differences; it does not authorize hardware access or prove every existing pairing identity. No production password/key is used.

x/sync NewWeighted now panics for negative constructor size; valid-weight synchronization tests and static consumer/config review must remain explicit. x/mod changes module/workfile parsing; cmd/ngen is included in command type/load qualification. Terminal editor code changes require API compatibility but do not imply interactive terminal/hardware acceptance.

## Required qualification and limits
Run source-bound isolated selected full/race hash/crypto/wire/type/descriptor/mining/crawler regression, public normalization/semaphore tests, explicit Unicode differential corpus, exact command source/static analysis and compiler/native/SBOM/binary advisory qualification. Each proof identifies actual source/graph, SDK, hashes, isolation, failures/skips and cleanup. No broad Core gate or parent p2p test package: it contains the previously denied45-second renewal action. That missing broader acceptance is not waived.

DHT censorship/unmaintained OpenPGP/local modified EVM identity, populated history/currentness/positiveR2/mining readiness, quality/protected review/operator/release obligations remain open. No live peers, operational data/key, original checkout or broker mount, upstream/default/accepted branch change, production/public/paid/signing-custody/DAO/final release.
