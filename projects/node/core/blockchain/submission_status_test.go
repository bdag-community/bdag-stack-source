package blockchain

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestBlockSubmissionStatusFromSignals(t *testing.T) {
	tests := []struct {
		name         string
		validKnown   bool
		invalidKnown bool
		processing   bool
		orphan       bool
		want         BlockSubmissionStatus
	}{
		{name: "unknown", want: BlockSubmissionUnknown},
		{name: "processing", processing: true, want: BlockSubmissionQueued},
		{name: "orphan", orphan: true, want: BlockSubmissionQueued},
		{name: "accepted", validKnown: true, want: BlockSubmissionAccepted},
		{name: "rejected", invalidKnown: true, want: BlockSubmissionRejected},
		{name: "processing outranks accepted candidate", validKnown: true, processing: true, want: BlockSubmissionQueued},
		{name: "processing outranks rejected candidate", invalidKnown: true, processing: true, want: BlockSubmissionQueued},
		{name: "rejected outranks orphan", invalidKnown: true, orphan: true, want: BlockSubmissionRejected},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := blockSubmissionStatusFromSignals(test.validKnown, test.invalidKnown, test.processing, test.orphan)
			if got != test.want {
				t.Fatalf("blockSubmissionStatusFromSignals() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestBlockSubmissionStatusTracksProcessingQueue(t *testing.T) {
	chain := &BlockChain{}
	hash := common.HexToHash("0x01")

	if got := chain.BlockSubmissionStatus(nil); got != BlockSubmissionUnknown {
		t.Fatalf("nil hash status = %d, want unknown", got)
	}
	chain.processQueueMap.Store(hash, nil)
	if got := chain.BlockSubmissionStatus(&hash); got != BlockSubmissionQueued {
		t.Fatalf("processing hash status = %d, want queued", got)
	}
	chain.processQueueMap.Delete(hash)
	if got := chain.BlockSubmissionStatus(&hash); got != BlockSubmissionUnknown {
		t.Fatalf("removed hash status = %d, want unknown", got)
	}
}
