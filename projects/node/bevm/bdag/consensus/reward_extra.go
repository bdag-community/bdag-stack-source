package consensus

import (
	"encoding/binary"
	"fmt"
	"math/big"
)

const (
	RewardExtraLength          = 8
	RewardExtraWithLayerLength = RewardExtraLength * 2
)

var rewardExtraWeiMultiplier = new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil)

// EncodeRewardExtra serializes the DAG reward into the exact EVM header.Extra
// representation consumed by the BDAG EVM consensus engine.
func EncodeRewardExtra(reward uint64) []byte {
	extra := make([]byte, RewardExtraLength)
	binary.BigEndian.PutUint64(extra, reward)
	return extra
}

// EncodeRewardExtraWithLayer binds the validated DAG reward to the native
// layer that produced the EVM block. The extended format is activated by the
// native governance upgrade and is deliberately distinct from legacy blocks.
func EncodeRewardExtraWithLayer(reward, layer uint64) []byte {
	extra := make([]byte, RewardExtraWithLayerLength)
	binary.BigEndian.PutUint64(extra[:RewardExtraLength], reward)
	binary.BigEndian.PutUint64(extra[RewardExtraLength:], layer)
	return extra
}

// DecodeRewardExtra parses the BDAG reward stored in EVM header.Extra.
func DecodeRewardExtra(extra []byte) (uint64, error) {
	if len(extra) != RewardExtraLength && len(extra) != RewardExtraWithLayerLength {
		return 0, fmt.Errorf("invalid reward extra length %d, want %d or %d", len(extra), RewardExtraLength, RewardExtraWithLayerLength)
	}
	return binary.BigEndian.Uint64(extra[:RewardExtraLength]), nil
}

// DecodeRewardExtraWithLayer parses the post-activation reward and native
// layer binding. Legacy reward-only headers are rejected.
func DecodeRewardExtraWithLayer(extra []byte) (uint64, uint64, error) {
	if len(extra) != RewardExtraWithLayerLength {
		return 0, 0, fmt.Errorf("invalid governance reward extra length %d, want %d", len(extra), RewardExtraWithLayerLength)
	}
	return binary.BigEndian.Uint64(extra[:RewardExtraLength]), binary.BigEndian.Uint64(extra[RewardExtraLength:]), nil
}

func RewardExtraRequired(blockNumber *big.Int) bool {
	return blockNumber != nil && blockNumber.Sign() > 0
}

// ValidateHeaderRewardExtra enforces the reward encoding on all non-genesis
// headers while preserving the legacy genesis extra data.
func ValidateHeaderRewardExtra(blockNumber *big.Int, extra []byte) error {
	if !RewardExtraRequired(blockNumber) {
		return nil
	}
	_, err := DecodeRewardExtra(extra)
	return err
}

// ValidateRewardExtra verifies that EVM header.Extra cannot diverge from the
// already-validated DAG reward.
func ValidateRewardExtra(extra []byte, reward uint64) error {
	extraReward, err := DecodeRewardExtra(extra)
	if err != nil {
		return err
	}
	if extraReward != reward {
		return fmt.Errorf("reward extra %d does not match block reward %d", extraReward, reward)
	}
	return nil
}

// ValidateRewardExtraAtLayer enforces the exact header.Extra format on either
// side of the native governance activation boundary. This prevents an EVM
// block number from standing in for the native layer used by signalling.
func ValidateRewardExtraAtLayer(extra []byte, reward, layer uint64, governance bool) error {
	if !governance {
		if len(extra) != RewardExtraLength {
			return fmt.Errorf("legacy reward extra length %d, want %d", len(extra), RewardExtraLength)
		}
		return ValidateRewardExtra(extra, reward)
	}

	extraReward, extraLayer, err := DecodeRewardExtraWithLayer(extra)
	if err != nil {
		return err
	}
	if extraReward != reward {
		return fmt.Errorf("reward extra %d does not match block reward %d", extraReward, reward)
	}
	if extraLayer != layer {
		return fmt.Errorf("reward extra native layer %d does not match block layer %d", extraLayer, layer)
	}
	return nil
}

// RewardExtraToWei converts an encoded DAG reward to the EVM balance unit.
func RewardExtraToWei(extra []byte) (*big.Int, error) {
	reward, err := DecodeRewardExtra(extra)
	if err != nil {
		return nil, err
	}
	amount := new(big.Int).SetUint64(reward)
	amount.Mul(amount, rewardExtraWeiMultiplier)
	return amount, nil
}
