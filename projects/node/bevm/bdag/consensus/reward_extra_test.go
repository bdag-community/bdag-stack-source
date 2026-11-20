package consensus

import (
	"encoding/binary"
	"math/big"
	"testing"
)

func TestRewardExtraRoundTrip(t *testing.T) {
	const reward = uint64(25127015282)

	extra := EncodeRewardExtra(reward)
	if len(extra) != RewardExtraLength {
		t.Fatalf("encoded reward extra length = %d, want %d", len(extra), RewardExtraLength)
	}

	decoded, err := DecodeRewardExtra(extra)
	if err != nil {
		t.Fatalf("DecodeRewardExtra() error = %v", err)
	}
	if decoded != reward {
		t.Fatalf("DecodeRewardExtra() = %d, want %d", decoded, reward)
	}
	if err := ValidateRewardExtra(extra, reward); err != nil {
		t.Fatalf("ValidateRewardExtra() error = %v", err)
	}
}

func TestGovernanceRewardExtraBindsNativeLayer(t *testing.T) {
	const reward = uint64(25127015282)
	const layer = uint64(4321)

	extra := EncodeRewardExtraWithLayer(reward, layer)
	if len(extra) != RewardExtraWithLayerLength {
		t.Fatalf("encoded governance extra length = %d, want %d", len(extra), RewardExtraWithLayerLength)
	}
	decodedReward, decodedLayer, err := DecodeRewardExtraWithLayer(extra)
	if err != nil {
		t.Fatalf("DecodeRewardExtraWithLayer() error = %v", err)
	}
	if decodedReward != reward || decodedLayer != layer {
		t.Fatalf("decoded governance extra = (%d, %d), want (%d, %d)", decodedReward, decodedLayer, reward, layer)
	}
	if err := ValidateRewardExtraAtLayer(extra, reward, layer, true); err != nil {
		t.Fatalf("ValidateRewardExtraAtLayer() error = %v", err)
	}
	if err := ValidateHeaderRewardExtra(big.NewInt(1), extra); err != nil {
		t.Fatalf("ValidateHeaderRewardExtra() rejected governance format: %v", err)
	}
	if _, err := RewardExtraToWei(extra); err != nil {
		t.Fatalf("RewardExtraToWei() rejected governance format: %v", err)
	}

	for _, test := range []struct {
		name       string
		extra      []byte
		layer      uint64
		governance bool
	}{
		{name: "legacy format after activation", extra: EncodeRewardExtra(reward), layer: layer, governance: true},
		{name: "governance format before activation", extra: extra, layer: layer, governance: false},
		{name: "wrong native layer", extra: extra, layer: layer + 1, governance: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRewardExtraAtLayer(test.extra, reward, test.layer, test.governance); err == nil {
				t.Fatal("expected governance reward-extra validation error")
			}
		})
	}
}

func TestValidateRewardExtraRejectsMismatch(t *testing.T) {
	const reward = uint64(100)
	tests := []struct {
		name  string
		extra uint64
	}{
		{name: "extra greater than reward", extra: reward + 1},
		{name: "extra less than reward", extra: reward - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra := make([]byte, RewardExtraLength)
			binary.BigEndian.PutUint64(extra, tt.extra)
			if err := ValidateRewardExtra(extra, reward); err == nil {
				t.Fatal("expected mismatch error")
			}
		})
	}
}

func TestDecodeRewardExtraRejectsMalformedLengths(t *testing.T) {
	tests := []struct {
		name  string
		extra []byte
	}{
		{name: "empty", extra: nil},
		{name: "short", extra: []byte{1, 2, 3, 4}},
		{name: "oversized", extra: []byte{0, 0, 0, 0, 0, 0, 0, 1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeRewardExtra(tt.extra); err == nil {
				t.Fatal("expected length error")
			}
			if err := ValidateRewardExtra(tt.extra, 1); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateHeaderRewardExtraPreservesGenesisExtra(t *testing.T) {
	if err := ValidateHeaderRewardExtra(big.NewInt(0), []byte{0}); err != nil {
		t.Fatalf("ValidateHeaderRewardExtra(genesis) error = %v", err)
	}
	if err := ValidateHeaderRewardExtra(big.NewInt(1), []byte{0}); err == nil {
		t.Fatal("expected non-genesis malformed extra error")
	}
}

func TestRewardExtraToWei(t *testing.T) {
	const reward = uint64(123)
	got, err := RewardExtraToWei(EncodeRewardExtra(reward))
	if err != nil {
		t.Fatalf("RewardExtraToWei() error = %v", err)
	}

	want := new(big.Int).Mul(new(big.Int).SetUint64(reward), new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil))
	if got.Cmp(want) != 0 {
		t.Fatalf("RewardExtraToWei() = %s, want %s", got, want)
	}
}
