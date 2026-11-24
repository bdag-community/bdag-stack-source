package miner

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/metrics"
)

type captureTimer struct{ duration time.Duration }

func (c *captureTimer) Snapshot() metrics.TimerSnapshot { return nil }
func (c *captureTimer) Stop()                           {}
func (c *captureTimer) Time(fn func())                  { fn() }
func (c *captureTimer) UpdateSince(time.Time)           {}
func (c *captureTimer) Update(d time.Duration)          { c.duration = d }

func TestTemplateTimersKeepMilliseconds(t *testing.T) {
	oldRequest, oldBuild := gbtRequestDuration, gbtDuration
	request, build := &captureTimer{}, &captureTimer{}
	gbtRequestDuration, gbtDuration = request, build
	defer func() { gbtRequestDuration, gbtDuration = oldRequest, oldBuild }()
	m := &Miner{}
	m.StatsGbtRequest(760, 1, "")
	m.StatsGbt(710, 1)
	if request.duration != 760*time.Millisecond || build.duration != 710*time.Millisecond {
		t.Fatalf("request %s build %s", request.duration, build.duration)
	}
}
