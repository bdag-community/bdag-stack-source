package params

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	eparams "github.com/ethereum/go-ethereum/params"
)

// No runtime flag or environment variable can replace the release schedule.
// null deliberately disables an unqualified preparation build. Release tooling
// must compile a reviewed future schedule, then pin the resulting binary.
//
//go:embed testnet-governance-schedule.json
var testnetGovernanceScheduleJSON []byte

type testnetGovernanceSchedule struct {
	EarliestEVMBlock     uint64 `json:"earliestEvmBlock"`
	SignalStartLayer     uint64 `json:"signalStartLayer"`
	ActivationLayer      uint64 `json:"activationLayer"`
	ReleaseDistributedAt uint64 `json:"releaseDistributedAt"`
	NoticePublishedAt    uint64 `json:"noticePublishedAt"`
	NotBeforeTimestamp   uint64 `json:"notBeforeTimestamp"`
}

func mustTestnetGovernanceSchedule(raw []byte) (*big.Int, *eparams.BDAGGovernanceForkConfig) {
	var schedule *testnetGovernanceSchedule
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		panic(fmt.Sprintf("invalid compiled testnet schedule: %v", err))
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		panic("testnet schedule must contain exactly one object or null")
	}
	if schedule == nil {
		return nil, nil
	}
	if schedule.EarliestEVMBlock == 0 || schedule.SignalStartLayer == 0 {
		panic("compiled testnet schedule requires positive start boundaries")
	}
	g := TestnetGovernanceArtifactTemplate()
	g.SignalWindowStartLayer = schedule.SignalStartLayer
	g.ActivationLayer = schedule.ActivationLayer
	g.InitialReleaseDistributedAt = schedule.ReleaseDistributedAt
	g.NoticePublishedAt = schedule.NoticePublishedAt
	g.NotBeforeTimestamp = schedule.NotBeforeTimestamp
	if err := g.Validate(); err != nil {
		panic(fmt.Sprintf("invalid compiled testnet schedule: %v", err))
	}
	return new(big.Int).SetUint64(schedule.EarliestEVMBlock), g
}

// TestnetGovernanceArtifactTemplate pins only revision-7 testnet artifacts.
// All schedule fields are absent until the reviewed immutable schedule is set.
// Historical private-devnet and fork-53 artifacts remain separately embedded.
func TestnetGovernanceArtifactTemplate() *eparams.BDAGGovernanceForkConfig {
	return &eparams.BDAGGovernanceForkConfig{
		TestnetRehearsal:   true,
		StakingTimelock:    common.HexToAddress("0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9"),
		MiningTimelock:     common.HexToAddress("0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0"),
		Governor:           common.HexToAddress("0x5FC8d32690cc91D4c39d9d3abcBD16989F875707"),
		ManifestHash:       common.HexToHash("0x2974bfdce5e574db5df0a2ca49c4fd8d5f8f78adb9845ba3f741125a0d0c7529"),
		SignalWindowLayers: 2000, SignalThresholdLayers: 1500, ActivationDelayLayers: 2000,
		StakingTimelockCodeHash: common.HexToHash("0x87574befb7aa7ecdbd0260281877ecce38d322c629fa002ad15aaf1696a8bdfe"),
		MiningTimelockCodeHash:  common.HexToHash("0x87574befb7aa7ecdbd0260281877ecce38d322c629fa002ad15aaf1696a8bdfe"),
		GovernorCodeHash:        common.HexToHash("0x90173eec057a41ccbadc337eca6d943c9458c4138ca9fd0e2414709b7a146e1a"),
		StakingCreationHash:     common.HexToHash("0x211961dd73e815ba943cef27e575b3c673b8b12c10a97fc6eff07d2d87eaf132"),
		StakingRuntimeHash:      common.HexToHash("0xe1d4979ce8e864616b885890c4cbc97bfbdf80c7832690dbac08a5f570143ea7"),
		ReaderCreationHash:      common.HexToHash("0x36f1fba4037676480c2f3f0c456354906000a7e78dc355fd70a954fa24abdfb6"),
		ReaderRuntimeHash:       common.HexToHash("0x7e8995c8a0a5c440b11024be018680b23d8faea686cd52bdbbd8e1d998ae4786"),
		HubCreationHash:         common.HexToHash("0x512668666f83c0d6ba6ccc6984c88ee7b893f89fc6125eeff1f1f0948daff4e5"),
		HubRuntimeHash:          common.HexToHash("0x7ca835855124bc9078955258d9229a70d2426265ce87a8409a334ed1290cefc9"),
		LegacyProxyCreationHash: common.HexToHash("0xe26ae18ca3524ca816bba4a77eb909af5b1566bfcf8b4c0f0a663512f8a86399"),
		LegacyProxyRuntimeHash:  common.HexToHash("0x75498dc0fa24902bbd116019b098f2e3c8474c5c1f26213d56594a6457e3a7ed"),
	}
}
