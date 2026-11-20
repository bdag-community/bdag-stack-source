package params

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"math"
	"math/big"
)

// BDAGSeparateStakingForkConfig selects a new transition without changing the
// shipped governance/timing definitions or their persisted historical state.
type BDAGSeparateStakingForkConfig struct {
	Version              uint32      `json:"version"`
	ArtifactSHA256       common.Hash `json:"artifactSha256"`
	SignalStartLayer     uint64      `json:"signalStartLayer"`
	ActivationLayer      uint64      `json:"activationLayer"`
	ReleaseDistributedAt uint64      `json:"releaseDistributedAt"`
	NoticePublishedAt    uint64      `json:"noticePublishedAt"`
	NotBeforeTimestamp   uint64      `json:"notBeforeTimestamp"`
}

func (c *BDAGSeparateStakingForkConfig) Validate(chainID *big.Int) error {
	if c == nil || chainID == nil || chainID.Cmp(big.NewInt(1043)) != 0 {
		return fmt.Errorf("separate staking rehearsal requires chain 1043")
	}
	if c.Version != 1 || c.ArtifactSHA256 == (common.Hash{}) {
		return fmt.Errorf("invalid separate staking artifact/version")
	}
	if c.SignalStartLayer == 0 || c.SignalStartLayer > math.MaxUint64-3999 || c.ActivationLayer != c.SignalStartLayer+3999 {
		return fmt.Errorf("separate staking requires 2000 signal layers and 2000 delay layers")
	}
	if c.ReleaseDistributedAt == 0 || c.NoticePublishedAt < c.ReleaseDistributedAt || c.NotBeforeTimestamp < c.NoticePublishedAt || c.NotBeforeTimestamp-c.NoticePublishedAt < 86400 {
		return fmt.Errorf("separate staking requires distribution and at least 24 hours notice")
	}
	return nil
}

func separateStakingConfigsEqual(a, b *BDAGSeparateStakingForkConfig) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
