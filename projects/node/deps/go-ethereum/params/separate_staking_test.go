package params

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestSeparateStakingPolicyIsPersistedAndNotMainnet(t *testing.T) {
	raw := []byte(`{"chainId":1043,"bdagSeparateStakingBlock":800000,"bdagSeparateStaking":{"version":1,"artifactSha256":"0x0000000000000000000000000000000000000000000000000000000000000001","signalStartLayer":700000,"activationLayer":703999,"releaseDistributedAt":200000,"noticePublishedAt":200000,"notBeforeTimestamp":286400}}`)
	var config ChainConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(&config)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(encoded, &fields)
	if fields["bdagSeparateStaking"] == nil || fields["bdagSeparateStakingBlock"] == nil {
		t.Fatal("separate staking policy silently dropped from persisted ChainConfig")
	}
	if config.BdagSeparateStaking.Validate(big.NewInt(1404)) == nil {
		t.Fatal("testnet transition must reject Mainnet")
	}
}

func TestSeparateStakingReplacementIsCompatibleOnlyBeforeTheEVMFloor(t *testing.T) {
	old := &ChainConfig{ChainID: big.NewInt(1043), BdagSeparateStakingBlock: big.NewInt(620000), BdagSeparateStaking: &BDAGSeparateStakingForkConfig{Version: 1, ArtifactSHA256: [32]byte{1}, SignalStartLayer: 605000, ActivationLayer: 608999, ReleaseDistributedAt: 1793386800, NoticePublishedAt: 1793386800, NotBeforeTimestamp: 1793473200}}
	replacement := *old
	policy := *old.BdagSeparateStaking
	policy.SignalStartLayer, policy.ActivationLayer = 562000, 565999
	policy.ReleaseDistributedAt, policy.NoticePublishedAt, policy.NotBeforeTimestamp = 1793403000, 1793403000, 1793489400
	replacement.BdagSeparateStaking = &policy
	for _, height := range []uint64{537090, 603000, 619999} {
		if err := old.CheckCompatible(&replacement, height, 0); err != nil {
			t.Fatalf("future schedule at %d requests a rewind: %v", height, err)
		}
	}
	for _, height := range []uint64{620000, 630000} {
		if err := old.CheckCompatible(&replacement, height, 0); err == nil {
			t.Fatalf("changed schedule at or after floor %d was accepted", height)
		}
	}
	if err := replacement.BdagSeparateStaking.Validate(big.NewInt(1043)); err != nil {
		t.Fatal(err)
	}
	short := policy
	short.NotBeforeTimestamp = short.NoticePublishedAt + 86399
	if err := short.Validate(big.NewInt(1043)); err == nil {
		t.Fatal("replacement accepted a shortened notice period")
	}
}
