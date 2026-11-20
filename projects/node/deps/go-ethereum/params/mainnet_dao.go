package params

import (
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

const MainnetDAOConfigConflict = "BlockDAG Mainnet DAO manifest"

// BDAGMainnetDAOConfig is a separately named immutable production transition.
// It never changes the definitions or artifacts of earlier network upgrades.
// Executable accounts are bound by ArtifactSHA256 in the compiled release.
type BDAGMainnetDAOConfig struct {
	Version               uint32      `json:"version"`
	ArtifactSHA256        common.Hash `json:"artifactSha256"`
	SignalStartLayer      uint64      `json:"signalStartLayer"`
	ActivationDelayLayers uint64      `json:"activationDelayLayers"`
	EarliestEVMBlock      uint64      `json:"earliestEvmBlock"`
	ReleaseDistributedAt  uint64      `json:"releaseDistributedAt"`
	NoticePublishedAt     uint64      `json:"noticePublishedAt"`
	NotBeforeTimestamp    uint64      `json:"notBeforeTimestamp"`
	LegacyGenesisTime     uint64      `json:"legacyGenesisTime"`
}

func (c *BDAGMainnetDAOConfig) Validate(chainID *big.Int) error {
	if c == nil || chainID == nil || chainID.Cmp(big.NewInt(1404)) != 0 {
		return fmt.Errorf("Mainnet DAO migration requires chain 1404")
	}
	if c.Version != 1 || c.ArtifactSHA256 == (common.Hash{}) || c.EarliestEVMBlock == 0 || c.LegacyGenesisTime == 0 {
		return fmt.Errorf("Mainnet DAO requires version, artifact, EVM floor and original clock")
	}
	if c.SignalStartLayer == 0 || c.ActivationDelayLayers < 2000 || c.SignalStartLayer > math.MaxUint64-99999 ||
		c.SignalStartLayer+99999 > math.MaxUint64-c.ActivationDelayLayers {
		return fmt.Errorf("invalid Mainnet DAO signal window/delay")
	}
	if c.ReleaseDistributedAt == 0 || c.NoticePublishedAt < c.ReleaseDistributedAt ||
		c.NotBeforeTimestamp < c.NoticePublishedAt || c.NotBeforeTimestamp-c.NoticePublishedAt < 604800 ||
		c.NotBeforeTimestamp-c.ReleaseDistributedAt < 3628800 || c.LegacyGenesisTime > c.ReleaseDistributedAt {
		return fmt.Errorf("Mainnet DAO requires six-week distribution and seven-day notice")
	}
	return nil
}

func mainnetDAOFloor(c *BDAGMainnetDAOConfig) *big.Int {
	if c == nil {
		return nil
	}
	return new(big.Int).SetUint64(c.EarliestEVMBlock)
}

func mainnetDAOConfigsEqual(a, b *BDAGMainnetDAOConfig) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
