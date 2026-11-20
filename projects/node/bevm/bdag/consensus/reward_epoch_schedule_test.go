package consensus

import (
	"errors"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/epochclock"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type scheduleFixture map[common.Hash]common.Hash

func (s scheduleFixture) GetState(address common.Address, slot common.Hash) common.Hash {
	if address != params.ChainTallyAddr {
		panic("wrong native reader address")
	}
	return s[slot]
}

func scheduleWordSlot(i int64) common.Hash {
	base := crypto.Keccak256Hash(common.LeftPadBytes([]byte{8}, 32)).Big()
	return common.BigToHash(new(big.Int).Add(base, big.NewInt(i)))
}

func validScheduleFixture() scheduleFixture {
	s := scheduleFixture{common.BigToHash(big.NewInt(8)): common.BigToHash(big.NewInt(2))}
	for i, word := range []int64{0, 1000, 60, 10, 1600, 1800} {
		s[scheduleWordSlot(int64(i))] = common.BigToHash(big.NewInt(word))
	}
	return s
}

func TestRewardEpochScheduleReadsSolidityLayout(t *testing.T) {
	s := validScheduleFixture()
	for i := int64(0); i < 8; i++ {
		s[common.BigToHash(big.NewInt(i))] = common.BigToHash(big.NewInt(99999))
	}
	clock, err := rewardEpochScheduleFromState(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := clock.At(1599, 11)
	want := epochclock.Status{Epoch: 11, Version: 2, Start: 3400, End: 5200, Duration: 1800}
	if err != nil || got != want {
		t.Fatalf("native/Solidity mismatch: %+v != %+v (%v)", got, want, err)
	}
	old, err := clock.Bounds(9)
	if err != nil || old.Start != 1540 || old.End != 1600 {
		t.Fatalf("history changed: %+v %v", old, err)
	}
}

func TestRewardEpochScheduleMissingAndMalformedState(t *testing.T) {
	if _, err := rewardEpochScheduleFromState(scheduleFixture{}); !errors.Is(err, errEpochScheduleUnavailable) {
		t.Fatalf("absent schedule: %v", err)
	}
	for _, count := range []int64{65, 1000000} {
		s := validScheduleFixture()
		s[params.SlotRewardEpochScheduleLength] = common.BigToHash(big.NewInt(count))
		if _, err := rewardEpochScheduleFromState(s); !errors.Is(err, epochclock.ErrInvalidSchedule) {
			t.Fatalf("unbounded count: %v", err)
		}
	}
	s := validScheduleFixture()
	s[scheduleWordSlot(4)] = common.BigToHash(big.NewInt(1601))
	if _, err := rewardEpochScheduleFromState(s); !errors.Is(err, epochclock.ErrInvalidSchedule) {
		t.Fatalf("discontinuous boundary: %v", err)
	}
}

func TestRewardEpochScheduleRejectsTruncatedNativeWords(t *testing.T) {
	for _, slot := range []common.Hash{params.SlotRewardEpochScheduleLength, scheduleWordSlot(1), scheduleWordSlot(2), scheduleWordSlot(3), scheduleWordSlot(4), scheduleWordSlot(5)} {
		s := validScheduleFixture()
		s[slot] = common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 64))
		if _, err := rewardEpochScheduleFromState(s); !errors.Is(err, epochclock.ErrOverflow) {
			t.Fatalf("truncated word at %s: %v", slot, err)
		}
	}
}
