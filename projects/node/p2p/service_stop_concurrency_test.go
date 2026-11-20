package p2p

import (
	"sync"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/node/service"
)

type shutdownBlockingChild struct {
	service.Service
	entered chan struct{}
	release chan struct{}
	stopped chan struct{}
}

func (s *shutdownBlockingChild) Stop() error {
	close(s.entered)
	<-s.release
	err := s.Service.Stop()
	close(s.stopped)
	return err
}

func TestServiceStopWaitsForConcurrentShutdown(t *testing.T) {
	s, h := newShutdownService(t, false)
	child := &shutdownBlockingChild{
		entered: make(chan struct{}), release: make(chan struct{}), stopped: make(chan struct{}),
	}
	s.InitServices()
	if err := s.Services().RegisterService(child); err != nil {
		t.Fatal(err)
	}
	if err := s.Service.Start(); err != nil {
		t.Fatal(err)
	}
	prior := h.beforeClose
	h.beforeClose = func() {
		prior()
		select {
		case <-child.stopped:
		default:
			t.Error("host closure bypassed the active child shutdown")
		}
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(child.release) }) }
	var wg sync.WaitGroup
	t.Cleanup(func() { release(); wg.Wait() })
	first, second := make(chan error, 1), make(chan error, 1)
	wg.Add(1)
	go func() { defer wg.Done(); first <- s.Stop() }()
	select {
	case <-child.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first shutdown did not enter the controlled child")
	}
	wg.Add(1)
	go func() { defer wg.Done(); second <- s.Stop() }()
	var secondErr error
	returned := false
	select {
	case secondErr = <-second:
		returned = true
		t.Error("concurrent Stop returned before child shutdown completed")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-first; err != nil {
		t.Error("first shutdown:", err)
	}
	if !returned {
		secondErr = <-second
	}
	if secondErr != nil {
		t.Error("concurrent caller did not observe the same completed result:", secondErr)
	}
	if err := s.Stop(); err != nil {
		t.Error("repeated shutdown:", err)
	}
	if h.calls.Load() != 1 {
		t.Errorf("host close calls = %d; want one", h.calls.Load())
	}
	assertShutdownResources(t, s, h)
}
