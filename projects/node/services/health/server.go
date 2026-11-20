package health

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/log"
	ser "github.com/BlockdagNetworkLabs/bdag/node/service"
)

// Server exposes liveness and readiness endpoints.
type Server struct {
	ser.Service
	Addr  string
	srv   *http.Server
	ready atomic.Bool
}

func New(addr string) *Server {
	return &Server{Addr: addr}
}

func (s *Server) Start() error {
	if err := s.Service.Start(); err != nil {
		return err
	}
	mux := http.NewServeMux()
	liveness := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	readiness := func(w http.ResponseWriter, r *http.Request) {
		if s.ready.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
		} else {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
		}
	}
	// Serve new Kubernetes-style endpoints while keeping legacy paths for compatibility.
	mux.HandleFunc("/healthz", liveness)
	mux.HandleFunc("/live", liveness)
	mux.HandleFunc("/readyz", readiness)
	mux.HandleFunc("/ready", readiness)
	s.srv = &http.Server{Addr: s.Addr, Handler: mux}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("health server failed", "err", err)
		}
	}()
	return nil
}

func (s *Server) Stop() error {
	if err := s.Service.Stop(); err != nil {
		return err
	}
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
	}
	return nil
}

func (s *Server) SetReady(r bool) {
	s.ready.Store(r)
}
