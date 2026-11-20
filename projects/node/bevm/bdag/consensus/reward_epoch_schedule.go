package consensus

import (
	"errors"
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/epochclock"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var errEpochScheduleUnavailable = errors.New("native epoch schedule is not installed")

type epochScheduleState interface {
	GetState(common.Address, common.Hash) common.Hash
}

func rewardScheduleWordSlot(word int64) common.Hash {
	base := crypto.Keccak256Hash(params.SlotRewardEpochScheduleLength.Bytes()).Big()
	return common.BigToHash(new(big.Int).Add(base, big.NewInt(word)))
}

// Candidate-only reader; activation and reward accounting are not wired yet.
func rewardEpochScheduleFromState(s epochScheduleState) (epochclock.Schedule, error) {
	if s == nil {
		return epochclock.Schedule{}, epochclock.ErrInvalidSchedule
	}
	read := func(slot common.Hash) (uint64, error) {
		value := s.GetState(params.ChainTallyAddr, slot).Big()
		if value.BitLen() > 64 {
			return 0, epochclock.ErrOverflow
		}
		return value.Uint64(), nil
	}
	count, err := read(params.SlotRewardEpochScheduleLength)
	if err != nil {
		return epochclock.Schedule{}, err
	}
	if count == 0 {
		return epochclock.Schedule{}, errEpochScheduleUnavailable
	}
	// Bound storage reads before allocation. Same maximum as the Solidity reader.
	if count > 64 {
		return epochclock.Schedule{}, epochclock.ErrInvalidSchedule
	}
	segments := make([]epochclock.Segment, count)
	for i := range segments {
		var words [3]uint64
		for j := range words {
			slot := rewardScheduleWordSlot(int64(3*i + j))
			words[j], err = read(slot)
			if err != nil {
				return epochclock.Schedule{}, err
			}
		}
		segments[i] = epochclock.Segment{FirstEpoch: words[0], StartTime: words[1], Duration: words[2]}
	}
	return epochclock.New(segments...)
}
