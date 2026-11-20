// Package epochclock describes immutable, boundary-aligned epoch schedules.
// Callers must select only consensus-activated segments. This package neither
// authorizes an upgrade nor changes any network's active configuration.
package epochclock

import (
	"errors"
	"math"
	"sort"
)

var ErrInvalidSchedule = errors.New("invalid epoch schedule")
var ErrOverflow = errors.New("epoch clock overflow")

type Segment struct {
	FirstEpoch uint64
	StartTime  uint64
	Duration   uint64
}

type Status struct {
	Epoch    uint64
	Version  uint64
	Start    uint64
	End      uint64
	Duration uint64
}

type Schedule struct{ segments []Segment }

// New copies and validates a schedule. Every transition must be at the exact
// start of an epoch under the preceding segment, so no reward interval is lost,
// duplicated or relabelled. Genesis and old segments cannot be rewritten by a
// caller mutating the input slice after construction.
func New(segments ...Segment) (Schedule, error) {
	if len(segments) == 0 || segments[0].FirstEpoch != 0 {
		return Schedule{}, ErrInvalidSchedule
	}
	for i, segment := range segments {
		if segment.Duration == 0 {
			return Schedule{}, ErrInvalidSchedule
		}
		if segment.StartTime > math.MaxUint64-segment.Duration {
			return Schedule{}, ErrOverflow
		}
		if i == 0 {
			continue
		}
		previous := segments[i-1]
		if segment.FirstEpoch <= previous.FirstEpoch {
			return Schedule{}, ErrInvalidSchedule
		}
		start, err := boundary(previous, segment.FirstEpoch)
		if err != nil {
			return Schedule{}, err
		}
		if start != segment.StartTime {
			return Schedule{}, ErrInvalidSchedule
		}
	}
	return Schedule{segments: append([]Segment(nil), segments...)}, nil
}

func boundary(segment Segment, epoch uint64) (uint64, error) {
	delta := epoch - segment.FirstEpoch // callers select a segment at/before epoch
	if delta > (math.MaxUint64-segment.StartTime)/segment.Duration {
		return 0, ErrOverflow
	}
	return segment.StartTime + delta*segment.Duration, nil
}

// Bounds returns historical metadata using the policy for that epoch, never
// the current duration applied backwards to genesis.
func (s Schedule) Bounds(epoch uint64) (Status, error) {
	if len(s.segments) == 0 {
		return Status{}, ErrInvalidSchedule
	}
	i := sort.Search(len(s.segments), func(i int) bool { return s.segments[i].FirstEpoch > epoch }) - 1
	segment := s.segments[i]
	start, err := boundary(segment, epoch)
	if err != nil {
		return Status{}, err
	}
	if start > math.MaxUint64-segment.Duration {
		return Status{}, ErrOverflow
	}
	return Status{epoch, uint64(i + 1), start, start + segment.Duration, segment.Duration}, nil
}

// At uses the native persisted floor to avoid reopening an epoch when timestamps
// move backwards. Before genesis epoch zero is reported with its future bounds.
func (s Schedule) At(timestamp, floor uint64) (Status, error) {
	if len(s.segments) == 0 {
		return Status{}, ErrInvalidSchedule
	}
	var epoch uint64
	if timestamp >= s.segments[0].StartTime {
		i := sort.Search(len(s.segments), func(i int) bool { return s.segments[i].StartTime > timestamp }) - 1
		segment := s.segments[i]
		delta := (timestamp - segment.StartTime) / segment.Duration
		if delta > math.MaxUint64-segment.FirstEpoch {
			return Status{}, ErrOverflow
		}
		epoch = segment.FirstEpoch + delta
	}
	if epoch < floor {
		epoch = floor
	}
	return s.Bounds(epoch)
}
