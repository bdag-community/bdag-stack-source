package p2p

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/synch"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
)

type shutdownObservedHost struct {
	host.Host
	calls       atomic.Int32
	closeError  error
	beforeClose func()
}

func (h *shutdownObservedHost) Close() error {
	h.calls.Add(1)
	if h.beforeClose != nil {
		h.beforeClose()
	}
	return errors.Join(h.Host.Close(), h.closeError)
}

func newShutdownService(t *testing.T, static bool) (*Service, *shutdownObservedHost) {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal("generate disposable identity:", err)
	}
	s := &Service{cfg: &common.Config{StaticPeersOnly: static}, sy: &synch.Sync{}}
	s.InitContext()
	s.rebroadcast = NewRebroadcast(s)
	h, err := libp2p.New(s.buildOptions(net.ParseIP("127.0.0.1"), key)...)
	if err != nil {
		t.Fatal("construct loopback host:", err)
	}
	observed := &shutdownObservedHost{Host: h}
	s.host = observed
	observed.beforeClose = func() {
		if s.kademliaDHT != nil && s.kademliaDHT.Context().Err() != nil {
			t.Error("DHT closed before host shutdown")
		}
	}
	t.Cleanup(func() {
		// Close the original host first, even when the baseline Service leaks DHT.
		if err := h.Close(); err != nil {
			t.Error("fixture host cleanup:", err)
		}
		if s.kademliaDHT != nil {
			if err := s.kademliaDHT.Close(); err != nil {
				t.Error("fixture DHT cleanup:", err)
			}
		}
		_ = s.Service.Stop()
	})
	if static == (s.kademliaDHT != nil) {
		t.Fatal("fixture static/dynamic constructor does not match configuration")
	}
	return s, observed
}

func assertShutdownResources(t *testing.T, s *Service, h host.Host) {
	t.Helper()
	if s.kademliaDHT != nil && !errors.Is(s.kademliaDHT.Context().Err(), context.Canceled) {
		t.Error("owned DHT lifetime remains active after shutdown")
	}
	if len(h.Network().ListenAddresses()) != 0 {
		t.Error("host listener remains open after shutdown")
	}
}

func TestCloseHostOwnsDHTLifetime(t *testing.T) {
	for _, tc := range []struct {
		name      string
		static    bool
		failClose bool
	}{
		{name: "dynamic"},
		{name: "static", static: true},
		{name: "host_error", failClose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := newShutdownService(t, tc.static)
			if tc.failClose {
				h.closeError = errors.New("host close failure probe")
			}
			var wg sync.WaitGroup
			results := make(chan error, 4)
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results <- s.closeHost()
				}()
			}
			wg.Wait()
			close(results)
			for err := range results {
				if !errors.Is(err, h.closeError) {
					t.Errorf("close result = %v; want host error %v", err, h.closeError)
				}
			}
			if h.calls.Load() != 1 {
				t.Errorf("host close calls = %d; want exactly one", h.calls.Load())
			}
			assertShutdownResources(t, s, h)
		})
	}
}

func TestCloseHostOwnsDHTWithoutHostField(t *testing.T) {
	s, h := newShutdownService(t, false)
	if err := h.Host.Close(); err != nil {
		t.Fatal(err)
	}
	s.host = nil
	if err := s.closeHost(); err != nil {
		t.Fatal("close separately owned DHT:", err)
	}
	assertShutdownResources(t, s, h)
}

func TestCloseHostWithoutResources(t *testing.T) {
	s := &Service{}
	for range 2 {
		if err := s.closeHost(); err != nil {
			t.Fatal(err)
		}
	}
}

type shutdownFailingChild struct{ service.Service }

func (s *shutdownFailingChild) Stop() error {
	_ = s.Service.Stop()
	return errors.New("child stop failure probe")
}

func TestServiceStopClosesDiscoveryResources(t *testing.T) {
	for _, childFailure := range []bool{false, true} {
		name := "normal"
		if childFailure {
			name = "child_failure"
		}
		t.Run(name, func(t *testing.T) {
			s, h := newShutdownService(t, false)
			if childFailure {
				s.InitServices()
				if err := s.Services().RegisterService(&shutdownFailingChild{}); err != nil {
					t.Fatal(err)
				}
			}
			// Start only the base lifecycle, without chain, sync or discovery loops.
			if err := s.Service.Start(); err != nil {
				t.Fatal(err)
			}
			err := s.Stop()
			if childFailure {
				if err == nil || !strings.Contains(err.Error(), "shutdownFailingChild") {
					t.Errorf("expected original registry stop error; got %v", err)
				}
			} else if err != nil {
				t.Fatal("stop:", err)
			}
			if !errors.Is(s.Context().Err(), context.Canceled) {
				t.Error("base context was not cancelled")
			}
			select {
			case <-s.rebroadcast.quit:
			default:
				t.Error("rebroadcast stop skipped after earlier stop failure")
			}
			assertShutdownResources(t, s, h)
		})
	}
}
