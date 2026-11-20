package synch

import (
	"context"
	"sync/atomic"
	"testing"

	l "github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/libp2p/go-libp2p/core/network"
)

func TestLazyTraceValueUsesCustomHandlerAfterFiltering(t *testing.T) {
	old := log
	t.Cleanup(func() { UseLogger(old) })

	var evaluated atomic.Int32
	var emitted atomic.Int32
	handler := l.LazyHandler(l.FuncHandler(func(record *l.Record) error {
		if record.Lvl == l.LvlTrace {
			emitted.Add(1)
		}
		return nil
	}))
	logger := l.New()
	logger.SetHandler(handler)
	UseLogger(logger)

	log.Trace("custom trace", "value", lazyTraceValue(func() any {
		evaluated.Add(1)
		return "formatted"
	}))
	if got := evaluated.Load(); got != 1 {
		t.Fatalf("lazy trace evaluations = %d, want 1", got)
	}
	if got := emitted.Load(); got != 1 {
		t.Fatalf("trace records emitted = %d, want 1", got)
	}
}

func TestLazyTraceValueStaysUnevaluatedWhenVmoduleFilters(t *testing.T) {
	old := log
	t.Cleanup(func() { UseLogger(old) })

	var evaluated atomic.Int32
	var emitted atomic.Int32
	origin := l.LazyHandler(l.FuncHandler(func(record *l.Record) error {
		if record.Lvl == l.LvlTrace {
			emitted.Add(1)
		}
		return nil
	}))
	filtered := l.NewGlogHandler(origin)
	filtered.Verbosity(l.LvlInfo)
	if err := filtered.Vmodule("file-that-does-not-match.go=5"); err != nil {
		t.Fatalf("Vmodule: %v", err)
	}
	logger := l.New()
	logger.SetHandler(filtered)
	UseLogger(logger)

	log.Trace("filtered trace", "value", lazyTraceValue(func() any {
		evaluated.Add(1)
		return "formatted"
	}))
	if got := evaluated.Load(); got != 0 {
		t.Fatalf("filtered lazy trace evaluations = %d, want 0", got)
	}
	if got := emitted.Load(); got != 0 {
		t.Fatalf("filtered trace records emitted = %d, want 0", got)
	}
}

func TestHandleRPCOneShotTraceUsesMatchingInfoVmodule(t *testing.T) {
	old := log
	t.Cleanup(func() { UseLogger(old) })

	var traceRecords atomic.Int32
	var evaluatedPeer atomic.Int32
	origin := l.LazyHandler(l.FuncHandler(func(record *l.Record) error {
		if record.Lvl != l.LvlTrace {
			return nil
		}
		traceRecords.Add(1)
		for i := 0; i+1 < len(record.Ctx); i += 2 {
			if record.Ctx[i] == "peer" {
				if value, ok := record.Ctx[i+1].(string); ok && value != "" {
					evaluatedPeer.Add(1)
				}
			}
		}
		return nil
	}))
	filtered := l.NewGlogHandler(origin)
	filtered.Verbosity(l.LvlInfo)
	if err := filtered.Vmodule("sync.go=5"); err != nil {
		t.Fatalf("Vmodule: %v", err)
	}
	logger := l.New()
	logger.SetHandler(filtered)
	UseLogger(logger)

	pe := newProcessRspErrorTestPeer()
	handleRPCOneShot(fakeP2PRPC{}, RPCPingTopic, nil,
		func(context.Context, any, network.Stream, *peers.Peer) *common.Error {
			return nil
		}, &testStream{}, pe)
	if got := traceRecords.Load(); got != 1 {
		t.Fatalf("production trace records = %d, want 1", got)
	}
	if got := evaluatedPeer.Load(); got != 1 {
		t.Fatalf("lazy peer values evaluated = %d, want 1", got)
	}
}
