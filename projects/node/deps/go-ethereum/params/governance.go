package params

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
)

// BDAGGovernanceForkConfig is persisted with ChainConfig. All executable artifacts
// and authorities are pinned; execution must never consult process environment.
type BDAGGovernanceForkConfig struct {
	// TestnetRehearsal permits the explicitly shorter public rehearsal notice
	// gate. Callers must additionally restrict this flag to chain ID 1043.
	TestnetRehearsal            bool           `json:"testnetRehearsal,omitempty"`
	StakingTimelock             common.Address `json:"stakingTimelock"`
	MiningTimelock              common.Address `json:"miningTimelock"`
	Governor                    common.Address `json:"governor"`
	ManifestHash                common.Hash    `json:"manifestHash"`
	SignalWindowStartLayer      uint64         `json:"signalWindowStartLayer"`
	SignalWindowLayers          uint64         `json:"signalWindowLayers"`
	SignalThresholdLayers       uint64         `json:"signalThresholdLayers"`
	ActivationDelayLayers       uint64         `json:"activationDelayLayers"`
	ActivationLayer             uint64         `json:"activationLayer"`
	InitialReleaseDistributedAt uint64         `json:"initialReleaseDistributedAt"`
	NoticePublishedAt           uint64         `json:"noticePublishedAt"`
	NotBeforeTimestamp          uint64         `json:"notBeforeTimestamp"`
	StakingTimelockCodeHash     common.Hash    `json:"stakingTimelockCodeHash"`
	MiningTimelockCodeHash      common.Hash    `json:"miningTimelockCodeHash"`
	GovernorCodeHash            common.Hash    `json:"governorCodeHash"`
	StakingCreationHash         common.Hash    `json:"stakingCreationHash"`
	StakingRuntimeHash          common.Hash    `json:"stakingRuntimeHash"`
	ReaderCreationHash          common.Hash    `json:"readerCreationHash"`
	ReaderRuntimeHash           common.Hash    `json:"readerRuntimeHash"`
	HubCreationHash             common.Hash    `json:"hubCreationHash"`
	HubRuntimeHash              common.Hash    `json:"hubRuntimeHash"`
	LegacyProxyCreationHash     common.Hash    `json:"legacyProxyCreationHash"`
	LegacyProxyRuntimeHash      common.Hash    `json:"legacyProxyRuntimeHash"`
}

func (c *BDAGGovernanceForkConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("governance fork config is required")
	}
	seen := make(map[common.Address]bool)
	for _, a := range []common.Address{c.StakingTimelock, c.MiningTimelock, c.Governor} {
		if a == (common.Address{}) || seen[a] {
			return fmt.Errorf("governance authority addresses must be nonzero and distinct")
		}
		seen[a] = true
	}
	if c.SignalWindowLayers != 2000 || c.SignalThresholdLayers != 1500 || c.ActivationDelayLayers < 2000 {
		return fmt.Errorf("governance signalling must use 1500 of 2000 layers and at least 2000 activation-delay layers")
	}
	windowEndExclusive := c.SignalWindowStartLayer + c.SignalWindowLayers
	if windowEndExclusive < c.SignalWindowStartLayer {
		return fmt.Errorf("governance signalling window overflows")
	}
	thresholdLayer := windowEndExclusive - 1
	if c.ActivationLayer < thresholdLayer || c.ActivationLayer-thresholdLayer < c.ActivationDelayLayers {
		return fmt.Errorf("governance activation layer does not satisfy its signalling delay")
	}
	if c.TestnetRehearsal {
		const oneDay = uint64(24 * 60 * 60)
		if c.NoticePublishedAt == 0 || c.NotBeforeTimestamp < c.NoticePublishedAt || c.NotBeforeTimestamp-c.NoticePublishedAt < oneDay {
			return fmt.Errorf("testnet governance activation requires at least 24 hours public notice")
		}
		if c.InitialReleaseDistributedAt == 0 || c.NoticePublishedAt < c.InitialReleaseDistributedAt {
			return fmt.Errorf("testnet governance notice cannot predate release distribution")
		}
	} else {
		const sevenDays = uint64(7 * 24 * 60 * 60)
		if c.NoticePublishedAt == 0 || c.NotBeforeTimestamp < c.NoticePublishedAt || c.NotBeforeTimestamp-c.NoticePublishedAt < sevenDays {
			return fmt.Errorf("governance activation requires at least seven days public notice")
		}
		const sixWeeks = uint64(6 * 7 * 24 * 60 * 60)
		if c.InitialReleaseDistributedAt == 0 || c.NotBeforeTimestamp < c.InitialReleaseDistributedAt || c.NotBeforeTimestamp-c.InitialReleaseDistributedAt < sixWeeks {
			return fmt.Errorf("governance activation requires at least six weeks after initial release distribution")
		}
	}
	for _, h := range []common.Hash{c.ManifestHash, c.StakingTimelockCodeHash, c.MiningTimelockCodeHash, c.GovernorCodeHash, c.StakingCreationHash, c.StakingRuntimeHash, c.ReaderCreationHash, c.ReaderRuntimeHash, c.HubCreationHash, c.HubRuntimeHash, c.LegacyProxyCreationHash, c.LegacyProxyRuntimeHash} {
		if h == (common.Hash{}) {
			return fmt.Errorf("every governance code hash is required")
		}
	}
	return nil
}
func governanceConfigsEqual(a, b *BDAGGovernanceForkConfig) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
