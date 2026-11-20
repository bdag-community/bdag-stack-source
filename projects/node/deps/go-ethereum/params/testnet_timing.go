package params

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"math"
	"math/big"
)

// BDAGTestnetTimingForkConfig is a one-time, explicitly approved rehearsal
// exception. It never authorizes Mainnet or a generic administrator upgrade.
// Store it with ChainConfig so a future binary cannot silently rewrite it.
type BDAGTestnetTimingForkConfig struct {
	Version              uint32      `json:"version"`
	ArtifactSHA256       common.Hash `json:"artifactSha256"`
	SignalStartLayer     uint64      `json:"signalStartLayer"`
	ActivationLayer      uint64      `json:"activationLayer"`
	ReleaseDistributedAt uint64      `json:"releaseDistributedAt"`
	NoticePublishedAt    uint64      `json:"noticePublishedAt"`
	NotBeforeTimestamp   uint64      `json:"notBeforeTimestamp"`
	EpochSeconds         uint64      `json:"epochSeconds"`
	TargetBlockSeconds   uint64      `json:"targetBlockSeconds"`
}

func (c *BDAGTestnetTimingForkConfig) Validate(chainID *big.Int) error {
	if c == nil || chainID == nil || chainID.Cmp(big.NewInt(1043)) != 0 {
		return fmt.Errorf("timing correction is restricted to testnet chain 1043")
	}
	if c.Version != 1 || c.ArtifactSHA256 == (common.Hash{}) || c.EpochSeconds != 1800 || c.TargetBlockSeconds != 2 {
		return fmt.Errorf("invalid one-time testnet timing policy or artifact commitment")
	}
	// This release permits one 2,000-layer window, with exactly 2,000 additional
	// delay layers. A missed window requires a new reviewed candidate, not sliding
	// the published schedule or changing the historical V3 definition.
	if c.SignalStartLayer == 0 || c.SignalStartLayer > math.MaxUint64-3999 || c.ActivationLayer != c.SignalStartLayer+3999 {
		return fmt.Errorf("timing correction requires its exact signal window and delay anchor")
	}
	if c.ReleaseDistributedAt == 0 || c.NoticePublishedAt < c.ReleaseDistributedAt ||
		c.NotBeforeTimestamp < c.NoticePublishedAt || c.NotBeforeTimestamp-c.NoticePublishedAt < 86400 {
		return fmt.Errorf("timing correction requires release distribution and at least 24 hours notice")
	}
	return nil
}

func testnetTimingConfigsEqual(a, b *BDAGTestnetTimingForkConfig) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
