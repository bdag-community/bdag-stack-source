# Native PoW format compatibility

System51 #624 within complete Core delivery #564 and release #49. Production base
149368c29f94dfe001732ca8e85b5964e6992e63. This adds characterization and a mode-eligibility
regression; no serializer, nonce, hash, algorithm, consensus rule or activation changes.

## Evidence and actual behavior

Verified195 selected complete Git blobs across the twelve fixed #564 inputs plus
current candidate. A Go AST metadata utility identifies1470 complete declaration
spans;27 of28 selected codec/function/configuration identities are byte-identical
across all thirteen revisions. Block.Decode has three bodies: original unbounded
transaction allocation, RC68 bound, current retained bound and single exact-byte RLP
reader. All six PoW Bytes/BlockData implementations, factory, primitive decoder,
header encoder/decoder/hash and all five PowConfig literals remain identical.
The current SCRYPT file differs only in Verify's185-byte short-input guard, not encoding.

An offline pure-library test executes real Block.Serialize/Deserialize, hashes,
complete transaction/parent identity, exact reserialization and reader suffixes.
Eight boundary nonce and extra-nonce values, two bounded proof patterns,
zero/one/two unsigned synthetic transactions and ordinary/generic readers give
8448 complete-body cases:768per algorithm ID. No key generation, PoW solving,
node startup, chain-validity/admission, operational chainstate or network execution.

|ID|Factory|Accepted/rejected|Complete round trips|Accepted hash changes|
|---:|---|---:|---:|---:|
|0|*pow.Blake2bd|768/0|12|672|
|1|*pow.Blake2bd|768/0|12|672|
|2|*pow.Blake2bd|768/0|12|672|
|3|*pow.Blake2bd|768/0|12|672|
|4|*pow.Blake2bd|768/0|12|672|
|5|*pow.Blake2bd|768/0|12|672|
|6|*pow.BlockdagKeccak256|768/0|12|672|
|7|*pow.CryptoNight|768/0|12|672|
|8|*pow.bdagXKeccakV1|768/0|12|672|
|9|*pow.Blake2s|384/384|0|336|
|10|*pow.Scrypt|768/0|768|0|

Verified SCRYPT:768/768 complete rounds retain nonce, extra-nonce, proof, complete
wire bytes, block hash, parents, unsigned transactions and suffix. This is a codec
claim, not valid proof-of-work, mining, persisted history or complete consensus proof.

IDs0-8 encode nonce into the extra-nonce half; current decoder produces original
nonce as extra-nonce and zero nonce.672/768accepted records per ID change hash.
Only12/768 cases preserve all fields/bytes. IDs1-5 fall back to Blake2bd rather than
a distinct implementation. Existing declaration alone never qualified them.

BLAKE2S ID9 emits185PoW bytes versus decoder178:384reject;384decode but change
proof, transaction/parent interpretation and trailing bytes. Zero full round trips.
The matrix proves these outcomes on synthetic bodies; remote exploitability and
existing deployed/historical prevalence remain unknown.

Actual Pow.CheckAvailable is checked at nine heights for every ID across six
declared mode sets (594checks). Mainnet/mainnet-isolated/testnet/privnet/devnet
declare only SCRYPT. Mixnet declares BDAGXKECCAKV1 ID8. Source selectors exist in
services/common/load.go and cmd/nodeworker/run.go; no deployed-network observation
is claimed. Mixnet is therefore not dismissed as a dormant enum. Its complete-body
codec cannot be qualified as identity-preserving on this evidence.

## Compatibility, migration and refusal requirements

- Retain the exact historical bytes and algorithm IDs. Do not silently relocate
  nonce fields, replace fallback types, normalize legacy records or change hashes.
- SCRYPT qualification applies to the sampled bounded codec combinations. Keep
  count/short-read/malformed envelope and actual admission/storage tests separate.
- A future legacy/Mixnet repair must specify version/discrimination, old/new
  header/body/hash identity and historical-read refusal/migration before changing
  production codecs. Prove signatures/authorization, wire/storage/import,
  transaction locations, parent/transaction tails and both reader interfaces
  against the exact candidate; preserve rejected original bytes and old identity.
- Check deployed source/format and historical prevalence only through separately
  authorized evidence. No production access, peer renewal, signing custody or
  activation is implied. Mainnet codec success does not clear Mixnet.
- Dataset/currentness acceptance must retain checked complete-record identity and
  refuse unsupported/non-roundtrip records; passing checksums or zero-nonce
  fixtures alone cannot establish general legacy compatibility.
- #624 remains open pending affected-caller/historical-record disposition,
  any required scoped repair and protected accepted-source qualification.
  Whole #564/#49 and original A1-A3 engineering remain incomplete.

## Fixed input custody

|Input|Exact commit|
|---|---|
|develop-20260908|32266dbe91489d1068484ae43d3f7082e956dd24|
|consolidated|60798818d274697d865973b9a5f3bd6a64d58255|
|owner-portable-dataset|59fe706cb9c4c96b9b2cb247692af9b94939d77f|
|canonical-template-latch|95b667c267822b738d0907c30274d71c0a556af2|
|child-lifecycle|5923ae69a862643f37504cd7230da570f4858ee6|
|gateway-observer-auth|a95adf65d633aa20e8b07caf158440c70c6eba39|
|hash-state-snapshot|53d23127d734477d5b7cbff1a280c0d769bfb2b8|
|mining-continuity|cef8422f84b6d28e86457e89aedebdbf50d07444|
|reorg-state-supervision|504b617c5becf7961ed1c23667d7c9ac9db4920b|
|rc68|f07e084c06d758bc92377fc18cb5a0130fe6f89c|
|rc75|bc3b8ca840836cc901547b10459292d07e0d38e5|
|unincorporated|5610c571b3fb46587ac17d656c75714a5c123c48|
|current|149368c29f94dfe001732ca8e85b5964e6992e63|

All selected source blobs are retained by full digest in the issue-bound evidence.
Function-byte equality is static evidence, not execution of all historical packages.
No historical source, consensus defaults, release refs, approved source scope,
private protections or gate requirements are changed.

## Complete byte-record constructor boundary

Follow-up #624/#636: NewBlockFromBytes previously accepted a valid block plus
unconsumed bytes and cached the entire buffer under its decoded header's hash.
Actual old-source suffix regression fails. Checked recovery presence already
has independent root/canonical/full-consumption guards; those stay unchanged.

The byte constructor now returns a fixed error and nil block when bytes remain.
NewBlockFromReader retains stream composition and leaves the next record/suffix
for its caller. Fourteen production call sites were inspected: database bodies,
miner submission, individual framed IBD payload, probes, canonical segments,
peer envelopes and challenges. All pass one block payload, with no intentional
multi-record byte-constructor use found. One commented testutils call and the
constructor declaration are excluded from the count. Peer review is static.

Actual public-API regressions cover0/1/2unsigned synthetic transactions,
byte/multi-byte/second-block suffixes, all truncated prefixes, record/hash/cached
byte identity and unchanged reader boundaries. Nonce/layout/hash semantics,
valid wire bytes, consensus defaults and size limits remain unchanged.
Input cache aliasing, outer allocation bounds, valid PoW/persisted history and
legacy/Mixnet format risks remain distinct requirements. Retain typed TxLoc,
generic/short/caller-buffered composition, count and byte-budget regressions,
actual exact-source compiler and native qualification. #624 remains open.
