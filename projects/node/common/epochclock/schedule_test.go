package epochclock

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"testing"
)

func TestSharedSolidityVectors(t *testing.T) {
	var fixture struct {
		Segments []struct{ FirstEpoch, StartTime, Duration string }
		Cases    []struct{ Timestamp, Floor, Epoch, Version, Start, End, Duration string }
	}
	data, err := os.ReadFile("testdata/schedule-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	u := func(value string) uint64 {
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	segments := make([]Segment, len(fixture.Segments))
	for i, s := range fixture.Segments {
		segments[i] = Segment{u(s.FirstEpoch), u(s.StartTime), u(s.Duration)}
	}
	schedule, err := New(segments...)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		got, err := schedule.At(u(c.Timestamp), u(c.Floor))
		want := Status{u(c.Epoch), u(c.Version), u(c.Start), u(c.End), u(c.Duration)}
		if err != nil || got != want {
			t.Fatalf("shared vector: %+v != %+v (%v)", got, want, err)
		}
	}
}

func TestBoundaryTransitionPreservesHistoricalEpochs(t *testing.T) {
	s, err := New(Segment{0, 1000, 60}, Segment{10, 1600, 1800})
	if err != nil {
		t.Fatalf("valid anchored schedule rejected: %v", err)
	}
	cases := []struct{ timestamp, floor, epoch, version, start, end, duration uint64 }{
		{999, 0, 0, 1, 1000, 1060, 60},
		{1000, 0, 0, 1, 1000, 1060, 60},
		{1599, 0, 9, 1, 1540, 1600, 60},
		{1600, 0, 10, 2, 1600, 3400, 1800},
		{3399, 0, 10, 2, 1600, 3400, 1800},
		{3400, 0, 11, 2, 3400, 5200, 1800},
		{1599, 11, 11, 2, 3400, 5200, 1800},
	}
	for _, c := range cases {
		got, err := s.At(c.timestamp, c.floor)
		want := Status{c.epoch, c.version, c.start, c.end, c.duration}
		if err != nil || got != want {
			t.Fatalf("At(%d,%d) = %+v, %v; want %+v", c.timestamp, c.floor, got, err, want)
		}
	}
	old, _ := New(Segment{0, 1000, 60})
	for epoch := uint64(0); epoch < 10; epoch++ {
		before, _ := old.Bounds(epoch)
		after, err := s.Bounds(epoch)
		if err != nil || before != after {
			t.Fatalf("historical epoch %d rewritten: %+v != %+v", epoch, before, after)
		}
	}
}

func TestClockRetainsNativeFloor(t *testing.T) {
	s, err := New(Segment{0, 1000, 60})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.At(1315, 6)
	if err != nil || got.Epoch != 6 || got.End != 1420 {
		t.Fatalf("closed epoch reopened: %+v %v", got, err)
	}
}

func TestInvalidSchedulesFailClosed(t *testing.T) {
	cases := [][]Segment{
		nil, {{0, 1000, 0}}, {{1, 1000, 60}},
		{{0, 1000, 60}, {10, 1601, 1800}},
		{{0, 1000, 60}, {10, 1540, 1800}},
		{{0, 1000, 60}, {0, 1000, 1800}},
		{{0, 1000, 60}, {10, 1600, 0}},
	}
	for _, segments := range cases {
		if _, err := New(segments...); !errors.Is(err, ErrInvalidSchedule) {
			t.Fatalf("accepted malformed schedule %+v: %v", segments, err)
		}
	}
	var zero Schedule
	if _, err := zero.At(1000, 0); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatal("zero schedule accepted")
	}
}

func TestOverflowFailsClosed(t *testing.T) {
	if _, err := New(Segment{0, math.MaxUint64, 60}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing initial boundary: %v", err)
	}
	if _, err := New(Segment{0, 1000, 60}, Segment{math.MaxUint64, 1000, 1800}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing transition: %v", err)
	}
	s, err := New(Segment{0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.At(math.MaxUint64, 0); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing epoch end: %v", err)
	}
	if _, err := s.At(0, math.MaxUint64); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing floor: %v", err)
	}
}

func TestScheduleCopiesInputAndSupportsLaterTransitions(t *testing.T) {
	segments := []Segment{{0, 1000, 60}, {10, 1600, 1800}, {12, 5200, 600}}
	s, err := New(segments...)
	if err != nil {
		t.Fatal(err)
	}
	segments[1].Duration = 1
	for epoch := uint64(0); epoch < 20; epoch++ {
		bounds, err := s.Bounds(epoch)
		if err != nil {
			t.Fatal(err)
		}
		for _, timestamp := range []uint64{bounds.Start, bounds.End - 1} {
			got, err := s.At(timestamp, 0)
			if err != nil || got != bounds {
				t.Fatalf("non-invertible epoch %d: %+v %v", epoch, got, err)
			}
		}
	}
	got, _ := s.At(1600, 0)
	if got.Duration != 1800 {
		t.Fatal("caller changed immutable schedule")
	}
}

func FuzzScheduleRoundTrip(f *testing.F) {
	f.Add(uint64(10), uint64(1800), uint64(23))
	f.Add(uint64(1), uint64(604800), uint64(0))
	f.Fuzz(func(t *testing.T, first, duration, epoch uint64) {
		first = first%1000000 + 1
		duration = duration%604800 + 1
		epoch %= 2000000
		s, err := New(Segment{0, 1000, 60}, Segment{first, 1000 + first*60, duration})
		if err != nil {
			t.Fatal(err)
		}
		bounds, err := s.Bounds(epoch)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.At(bounds.Start, 0)
		if err != nil || got != bounds {
			t.Fatalf("round trip: %+v != %+v, %v", got, bounds, err)
		}
	})
}
