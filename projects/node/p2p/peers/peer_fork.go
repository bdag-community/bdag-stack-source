package peers

import (
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

type PeerForkStatus string

const (
	PeerForkStatusUnknown               PeerForkStatus = "unknown"
	PeerForkStatusSameMainChain         PeerForkStatus = "same_main_chain"
	PeerForkStatusDivergentMainChain    PeerForkStatus = "divergent_main_chain"
	PeerForkStatusCommonMainChainPrefix PeerForkStatus = "common_main_chain_prefix"
)

type PeerForkObservation struct {
	Status              PeerForkStatus
	Confidence          string
	Reason              string
	SameNativeForkKnown bool
	SameNativeFork      bool
	LastChecked         time.Time
	LastError           string

	LocalMainOrder uint64
	LocalMainTip   ethcommon.Hash

	MainOrder  uint64
	MainHeight uint64
	Layer      uint64
	Total      uint64
	MainTip    ethcommon.Hash
	Tips       []ethcommon.Hash

	SyncPoint       ethcommon.Hash
	DivergenceOrder uint64

	CommitmentStatus    string
	EVMCheckpointNumber uint64
	EVMCheckpointHash   ethcommon.Hash
	EVMCheckpointRoot   ethcommon.Hash

	SampledBlockHash     ethcommon.Hash
	SampledBlockOrder    uint64
	SampledStateRootGot  ethcommon.Hash
	SampledStateRootWant ethcommon.Hash
}

func (o *PeerForkObservation) Clone() *PeerForkObservation {
	if o == nil {
		return nil
	}
	clone := *o
	if o.Tips != nil {
		clone.Tips = append([]ethcommon.Hash(nil), o.Tips...)
	}
	return &clone
}

func (o *PeerForkObservation) DivergentMainChain() bool {
	return o != nil && o.Status == PeerForkStatusDivergentMainChain
}
