package blockchain

import "github.com/ethereum/go-ethereum/common"

// BlockSubmissionStatus is the authoritative local disposition of an exact
// block hash after or during ProcessBlock. It is intentionally narrower than
// general chain health: callers use it only to distinguish a final accepted or
// rejected block from work that is still processing or retained as an orphan.
type BlockSubmissionStatus uint8

const (
	BlockSubmissionUnknown BlockSubmissionStatus = iota
	BlockSubmissionQueued
	BlockSubmissionAccepted
	BlockSubmissionRejected
)

func blockSubmissionStatusFromSignals(validKnown, invalidKnown, processing, orphan bool) BlockSubmissionStatus {
	switch {
	case processing:
		return BlockSubmissionQueued
	case validKnown:
		return BlockSubmissionAccepted
	case invalidKnown:
		return BlockSubmissionRejected
	case orphan:
		return BlockSubmissionQueued
	default:
		return BlockSubmissionUnknown
	}
}

// BlockSubmissionStatus returns the authoritative local disposition for hash.
// An active processing marker takes precedence over a DAG entry because the
// DAG exposes a candidate before all connection work has finished. An orphan
// remains queued because it can still connect when its missing parent arrives.
func (b *BlockChain) BlockSubmissionStatus(hash *common.Hash) BlockSubmissionStatus {
	if b == nil || hash == nil {
		return BlockSubmissionUnknown
	}

	// Sample twice to close both handoffs: queue-to-DAG when a block finishes
	// and unknown-to-queue when another submission starts concurrently.
	for range 2 {
		validKnown, invalidKnown := b.blockSubmissionValidity(hash)
		_, processing := b.processQueueMap.Load(*hash)
		orphan := b.IsOrphan(hash)
		status := blockSubmissionStatusFromSignals(validKnown, invalidKnown, processing, orphan)
		if status != BlockSubmissionUnknown {
			return status
		}
	}
	return BlockSubmissionUnknown
}

func (b *BlockChain) blockSubmissionValidity(hash *common.Hash) (validKnown, invalidKnown bool) {
	if b == nil || b.bd == nil || hash == nil {
		return false, false
	}
	block := b.bd.GetBlock(hash)
	if block == nil || block.GetState() == nil {
		return false, false
	}
	if block.GetState().GetStatus().KnownInvalid() {
		return false, true
	}
	return true, false
}
