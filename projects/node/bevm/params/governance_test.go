package params

import (
	"encoding/json"
	ethparams "github.com/ethereum/go-ethereum/params"
	"math/big"
	"strings"
	"testing"
)

func TestGovernanceForkStrictConfig(t *testing.T) {
	if BdagMainnetChainConfig.BdagGovernanceForkBlock != nil || BdagMainnetChainConfig.BdagGovernanceFork != nil {
		t.Fatal("mainnet governance fork must remain disabled")
	}
	candidateConfig := *BdagTestnetChainConfig
	candidateConfig.BdagGovernanceForkBlock, candidateConfig.BdagGovernanceFork = mustTestnetGovernanceSchedule([]byte(`{"earliestEvmBlock":50000,"signalStartLayer":50000,"activationLayer":53999,"releaseDistributedAt":100,"noticePublishedAt":100,"notBeforeTimestamp":86500}`))
	if !candidateConfig.BdagGovernanceFork.TestnetRehearsal || candidateConfig.BdagGovernanceFork.ActivationLayer != 53_999 {
		t.Fatal("unexpected testnet governance rehearsal policy")
	}
	if err := candidateConfig.BdagGovernanceFork.Validate(); err != nil {
		t.Fatalf("invalid persisted testnet governance config: %v", err)
	}
	baselineConfig := *BdagTestnetChainConfig
	baselineConfig.BdagGovernanceForkBlock = nil
	baselineConfig.BdagGovernanceFork = nil
	if err := baselineConfig.CheckCompatible(&candidateConfig, 49_999, 0); err != nil {
		t.Fatalf("candidate cannot be introduced at the preserved baseline checkpoint: %v", err)
	}
	if err := baselineConfig.CheckCompatible(&candidateConfig, 50_000, 0); err == nil {
		t.Fatal("accepted candidate introduction after its EVM fork boundary")
	}
	cfg := &ethparams.BDAGGovernanceForkConfig{}
	// Every address/hash is required; a parseable empty object must fail closed.
	for _, input := range []string{"", "{}", "null", "{\"unknown\":1}", "{} {}"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("accepted enabled fork config %q", input)
				}
			}()
			parseGovernanceForkConfig(big.NewInt(20), input)
		}()
	}
	if parseGovernanceForkConfig(nil, "") != nil {
		t.Fatal("disabled config")
	}
	// Build a structurally complete fixture through JSON, independent of field names.
	raw, _ := json.Marshal(cfg)
	var fields map[string]interface{}
	json.Unmarshal(raw, &fields)
	for k, v := range fields {
		if _, ok := v.(string); ok {
			size := 64
			if strings.Contains(strings.ToLower(k), "hash") == false {
				size = 40
			}
			fields[k] = "0x" + strings.Repeat("1", size)
		} else if _, ok := v.(bool); ok {
			fields[k] = false
		} else {
			fields[k] = 60
		}
	}
	// Distinct authority addresses and exact constitutional signalling bounds.
	fields["stakingTimelock"] = "0x" + strings.Repeat("2", 40)
	fields["miningTimelock"] = "0x" + strings.Repeat("3", 40)
	fields["governor"] = "0x" + strings.Repeat("4", 40)
	fields["signalWindowStartLayer"] = 1_000
	fields["signalWindowLayers"] = 2_000
	fields["signalThresholdLayers"] = 1_500
	fields["activationDelayLayers"] = 2_000
	// The first window covers layers 1,000 through 2,999 inclusive. A
	// 2,000-layer delay therefore activates at layer 4,999.
	fields["activationLayer"] = 4_999
	fields["initialReleaseDistributedAt"] = 100
	fields["noticePublishedAt"] = 3_024_100
	fields["notBeforeTimestamp"] = 3_628_900
	raw, _ = json.Marshal(fields)
	got := parseGovernanceForkConfig(big.NewInt(20), string(raw))
	if got == nil {
		t.Fatal("valid config rejected")
	}
	tooSoon := *got
	tooSoon.InitialReleaseDistributedAt++
	if err := tooSoon.Validate(); err == nil || !strings.Contains(err.Error(), "six weeks") {
		t.Fatalf("accepted short initial distribution lead time: %v", err)
	}
	testnetRehearsal := *got
	testnetRehearsal.TestnetRehearsal = true
	testnetRehearsal.InitialReleaseDistributedAt = 100
	testnetRehearsal.NoticePublishedAt = 100
	testnetRehearsal.NotBeforeTimestamp = 86_500
	if err := testnetRehearsal.Validate(); err != nil {
		t.Fatalf("valid 24-hour testnet rehearsal gate rejected: %v", err)
	}
	testnetTooSoon := testnetRehearsal
	testnetTooSoon.NotBeforeTimestamp--
	if err := testnetTooSoon.Validate(); err == nil || !strings.Contains(err.Error(), "24 hours") {
		t.Fatalf("accepted short testnet notice gate: %v", err)
	}
	productionTooSoon := testnetRehearsal
	productionTooSoon.TestnetRehearsal = false
	if err := productionTooSoon.Validate(); err == nil || !strings.Contains(err.Error(), "seven days") {
		t.Fatalf("testnet policy weakened production notice: %v", err)
	}
	encoded, _ := json.Marshal(&ethparams.ChainConfig{BdagGovernanceForkBlock: big.NewInt(20), BdagGovernanceFork: got})
	var roundtrip ethparams.ChainConfig
	if err := json.Unmarshal(encoded, &roundtrip); err != nil || *roundtrip.BdagGovernanceFork != *got {
		t.Fatalf("config did not persist: %v", err)
	}
}
