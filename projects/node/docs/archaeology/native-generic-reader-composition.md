# Native block decoder reader composition (#636)

Verified base: 62aa55256a06625fc89c8efa846b5c356b590cff, Core PR177.
Relevant discovery: core/types/block.go Block.Decode and vendored
deps/go-ethereum/rlp/decode.go Stream.Reset. Existing TxLoc repair handles
bytes.Buffer; this follow-up covers the public generic io.Reader contract.

The old decoder constructs a fresh RLP stream for every transaction. Stream.Reset
wraps readers lacking ReadByte in bufio.Reader. Its speculative reads consume later
transactions and caller-owned trailing bytes; discarded streams lose those bytes.
Unsigned codec controls reproduce lost suffixes after one transaction and EOF after
the first transaction of mixed records. Existing byte-reader fixtures do not expose
this behavior.

Block.Decode now supplies exact single-byte reads for generic readers and reuses one
RLP stream within the transaction sequence. Existing byte readers retain their own
read behavior. Callers using an existing bufio.Reader must continue using that same
reader to access its buffered suffix. No transaction wire bytes change.

Verification covers zero/one/two/four transactions, vendored types0..3, bytes.Reader,
generic readers, short reads and caller buffering, suffix preservation and two
consecutive blocks. Existing pure count-bound and TxLoc cases remain included.
Normal and race runs use a compiler-only source export with only these pure-library
tests; original peer/storage/renewal tests are excluded.

Limits: no total RLP byte budget, receiver atomicity, chain-validity, blob sidecar,
node/storage/peer runtime, historical import, or protected qualification claim.
The accepted tuple is unchanged until review and qualification. Legacy PoW wire
compatibility (#624) remains a separate unresolved migration contract.
