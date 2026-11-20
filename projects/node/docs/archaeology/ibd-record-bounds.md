# Issue624 IBD length-prefixed native record boundary

Status: verified bounded codec defect and isolated candidate repair. Parent Core191 ae7ee46ef4a5d061de4b668e63bd6e4d09ecbafa. System51 #624/#636; full program #49/#564.

## Discovery and compatibility

Complete Git-blob/Go-AST caller catalog covers the twelve fixed historical inputs from native-pow-format-compatibility.md plus the current parent. All thirteen have the same IBDBlock.Decode function identity. Historical applications were not executed. Current Decode reads a four-byte uint32 body length, then the serialized native block; ImportBlock advances by four plus that length. The top-level import order prefix and operational chain import are outside this bounded execution.

Actual parent execution with tiny synthetic fixtures verifies three failures: truncated prefix/body panic, wrapping or oversized uint32 length panic, and successful body decoding beyond the supplied slice length when backing capacity contains a complete frame. Zero/one/two unsigned-transaction valid records and concatenated frames remain positive controls. No PoW solving, node startup, real database, peer action or operational data is involved.

## Contract and repair

Require a complete four-byte prefix, then compare uint32 length to the logical remaining input length using uint64 before any conversion/addition. Return io.ErrUnexpectedEOF for incomplete framing. Only then slice exactly the declared record and call the existing strict NewBlockFromBytes. Valid bytes, native hashes, length prefix, concatenated following record, transaction semantics and caller input remain unchanged. There is no new consensus limit, nonce/header interpretation, activation rule, hash normalization or Encode behavior.

The existing initial length assignment after a complete prefix and successful b.blk assignment are retained. This change does not claim transactional receiver reset on errors or a new whole-file trailing/count policy.

## Verification and limits

Four bounded TestIBDRecordBounds tests exercise every truncation of one valid frame, five extreme/nonzero lengths, logical-length versus capacity, and zero/one/two transaction concatenation. Actual original production method fails the first three tests while valid framing passes; candidate passes normal and race modes in network-none containers with only admitted source, SDK, public modules and compiler cache mounted. Initial fixture import-path compilation error is retained separately and is not counted as behavioral red.

The parser tests do not execute ImportBlock, node/main, live peers, PoW validation or historical applications. The separate unchecked whole-file order-count prefix remains source-observed and requires a bounded pure-helper contract before any further change. Historical legacy/Mixnet layout, whole-root quality/security/coverage and independent acceptance gates remain open; passing this repair does not qualify or release the full project.
