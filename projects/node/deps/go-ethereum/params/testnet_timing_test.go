package params

import (
	"github.com/ethereum/go-ethereum/common"
	"math"
	"math/big"
	"testing"
)

func timingFixture() *BDAGTestnetTimingForkConfig {
	return &BDAGTestnetTimingForkConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"),
		SignalStartLayer: 100000, ActivationLayer: 103999, ReleaseDistributedAt: 200000,
		NoticePublishedAt: 200000, NotBeforeTimestamp: 286400, EpochSeconds: 1800, TargetBlockSeconds: 2}
}
func TestTestnetTimingPolicy(t *testing.T) {
	c := timingFixture()
	if err := c.Validate(big.NewInt(1043)); err != nil {
		t.Fatal(err)
	}
	for _, chain := range []*big.Int{nil, big.NewInt(1404), big.NewInt(8008), big.NewInt(1)} {
		if c.Validate(chain) == nil {
			t.Fatalf("timing exception accepted chain %v", chain)
		}
	}
	for name, change := range map[string]func(*BDAGTestnetTimingForkConfig){
		"version":      func(c *BDAGTestnetTimingForkConfig) { c.Version = 2 },
		"artifact":     func(c *BDAGTestnetTimingForkConfig) { c.ArtifactSHA256 = common.Hash{} },
		"start":        func(c *BDAGTestnetTimingForkConfig) { c.SignalStartLayer = 0 },
		"overflow":     func(c *BDAGTestnetTimingForkConfig) { c.SignalStartLayer = math.MaxUint64 - 2000 },
		"early anchor": func(c *BDAGTestnetTimingForkConfig) { c.ActivationLayer-- },
		"moved anchor": func(c *BDAGTestnetTimingForkConfig) { c.ActivationLayer++ },
		"no notice":    func(c *BDAGTestnetTimingForkConfig) { c.NoticePublishedAt = 0 },
		"short notice": func(c *BDAGTestnetTimingForkConfig) { c.NotBeforeTimestamp-- },
		"distribution": func(c *BDAGTestnetTimingForkConfig) { c.ReleaseDistributedAt++ },
		"epoch":        func(c *BDAGTestnetTimingForkConfig) { c.EpochSeconds = 60 },
		"target":       func(c *BDAGTestnetTimingForkConfig) { c.TargetBlockSeconds = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *c
			change(&candidate)
			if candidate.Validate(big.NewInt(1043)) == nil {
				t.Fatal("invalid timing accepted")
			}
		})
	}
}
func TestTestnetTimingCompatibility(t *testing.T) {
	a := &ChainConfig{ChainID: big.NewInt(1043), BdagTestnetTimingBlock: big.NewInt(100), BdagTestnetTiming: timingFixture()}
	b := *a
	changed := *a.BdagTestnetTiming
	changed.NotBeforeTimestamp++
	b.BdagTestnetTiming = &changed
	if a.CheckCompatible(&b, 99, 0) != nil {
		t.Fatal("future replacement rejected")
	}
	if a.CheckCompatible(&b, 100, 0) == nil {
		t.Fatal("active policy rewrite accepted")
	}
	b = *a
	b.BdagTestnetTiming = nil
	if a.CheckCompatible(&b, 100, 0) == nil {
		t.Fatal("active policy removal accepted")
	}
	b = *a
	b.BdagTestnetTimingBlock = nil
	if a.CheckCompatible(&b, 100, 0) == nil {
		t.Fatal("active boundary removal accepted")
	}
}
