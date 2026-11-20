package gateway

import (
	"context"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"strconv"
	"time"

	"strings"

	cache "github.com/BlockdagNetworkLabs/bdag/common/lrucache"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	"golang.org/x/time/rate"
)

// New creates a new Gateway with the provided configuration.
func New(cfg Config) (*Gateway, error) {
	cfg.ListenAddr = strings.TrimSpace(cfg.ListenAddr)
	cfg.WSListenAddr = strings.TrimSpace(cfg.WSListenAddr)

	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = l.New(l.Ctx{"module": "gateway"})
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = DefaultHTTPTimeout
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.CertifiedEVMHeadWait == 0 {
		cfg.CertifiedEVMHeadWait = DefaultCertifiedEVMHeadWait
	}
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = DefaultMaxConcurrentRequests
	}
	if cfg.CriticalConcurrentRequests <= 0 {
		cfg.CriticalConcurrentRequests = DefaultCriticalConcurrentRequests
	}
	config := cache.CacheConfig{
		MaxSize:         100,
		DefaultTTL:      2 * time.Minute,
		CleanupInterval: 30 * time.Second,
		EvictionPolicy:  cache.LFU,
		EnableMetrics:   true,
	}

	// initialize rate-limiter cache
	limiterCache, err := cache.NewCache[*rate.Limiter](config)
	if err != nil {
		return nil, fmt.Errorf("failed to create rate-limiter cache: %w", err)
	}

	// Reuse connections, but cap per-peer fanout. Under sync pressure a high
	// per-host limit can pile hundreds of pending RPC dials onto the same peer.
	dialer := &net.Dialer{
		Timeout:   750 * time.Millisecond,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 8,
		MaxConnsPerHost:     8,
		IdleConnTimeout:     30 * time.Second,
	}
	probeDialer := &net.Dialer{
		Timeout:   350 * time.Millisecond,
		KeepAlive: 15 * time.Second,
	}
	probeTransport := &http.Transport{
		DialContext:         probeDialer.DialContext,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 2,
		MaxConnsPerHost:     2,
		IdleConnTimeout:     10 * time.Second,
		DisableKeepAlives:   true,
	}

	// initialize template cache (2 min TTL, same as rate limiter for consistency)
	tplCache, err := cache.NewCache[*TemplateCacheEntry](config)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	g := &Gateway{
		cfg: cfg,
		client: &http.Client{
			Timeout:   cfg.HTTPTimeout,
			Transport: transport,
		},
		probeClient: &http.Client{
			Timeout:   1200 * time.Millisecond,
			Transport: probeTransport,
		},
		primaryAuthDigest: authDigest(cfg.InboundUser, cfg.InboundPass),
		limitedAuthDigest: authDigest(cfg.InboundLimitUser, cfg.InboundLimitPass),
		logger:            cfg.Logger,
		limiters:          make(map[string]*rate.Limiter),
		limiterS:          limiterCache,
		tplCache:          tplCache,
		authPorts: map[string]bool{
			strconv.Itoa(cfg.PeerDagPort): true,
			strconv.Itoa(cfg.PeerEvmPort): true,
		},
	}

	if cfg.GlobalRateLimit <= 0 {
		cfg.GlobalRateLimit = 2000
	}
	if cfg.GlobalRateBurst <= 0 {
		cfg.GlobalRateBurst = 2000
	}
	g.globalLimiter = rate.NewLimiter(rate.Limit(cfg.GlobalRateLimit), cfg.GlobalRateBurst)

	g.sem = make(chan struct{}, cfg.MaxConcurrentRequests)
	g.criticalSem = make(chan struct{}, cfg.CriticalConcurrentRequests)

	g.peerManager = NewPeerManager(g)
	return g, nil
}

// NewEVM creates a new EVM gateway.
func NewEVM(cfg Config) (*EVM, error) {
	cfg.IsEVM = true
	g, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &EVM{Gateway: g}, nil
}

// NewDAG creates a new DAG gateway.
func NewDAG(cfg Config) (*DAG, error) {
	cfg.IsEVM = false
	g, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &DAG{Gateway: g}, nil
}

func (g *Gateway) MaxPeerHeight() uint64 {
	if g == nil || g.peerManager == nil {
		return 0
	}
	if g.cfg.IsEVM {
		target, _, _ := g.evmSyncTarget()
		return target
	}
	return g.peerManager.GetMaxHeight()
}

func (g *Gateway) evmSyncTarget() (target, peerMax, native uint64) {
	if g == nil {
		return 0, 0, 0
	}
	if g.peerManager != nil {
		peerMax = g.peerManager.GetMaxHeight()
	}
	target = peerMax
	if g.SyncChecker != nil {
		if provider, ok := g.SyncChecker.(nativeEVMHeightProvider); ok {
			native = provider.NativeEVMHeight()
			if native > 0 && (target == 0 || target > native) {
				target = native
			}
		}
	}
	return target, peerMax, native
}

// Start launches the HTTP server.
func (g *Gateway) Start() error {
	if err := g.Service.Start(); err != nil {
		return err
	}
	g.peerManager.Start()
	mux := http.NewServeMux()
	mux.HandleFunc("/", g.handle)
	g.srv = &http.Server{
		Addr:         g.cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  g.cfg.ReadTimeout,
		WriteTimeout: g.cfg.WriteTimeout,
		IdleTimeout:  g.cfg.IdleTimeout,
	}
	ln, err := net.Listen("tcp", g.cfg.ListenAddr)
	if err != nil {
		return err
	}

	go func() {
		if err := g.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			// ignore error logging here; gateway is lightweight
		}
	}()

	// Start secondary WebSocket/RPC listener if configured
	if g.cfg.WSListenAddr != "" && g.cfg.WSListenAddr != g.cfg.ListenAddr {
		g.wsSrv = &http.Server{
			Addr:         g.cfg.WSListenAddr,
			Handler:      mux, // Reuse same handler
			ReadTimeout:  g.cfg.ReadTimeout,
			WriteTimeout: g.cfg.WriteTimeout,
			IdleTimeout:  g.cfg.IdleTimeout,
		}
		wsLn, err := net.Listen("tcp", g.cfg.WSListenAddr)
		if err != nil {
			return err
		}
		go func() {
			if err := g.wsSrv.Serve(wsLn); err != nil && err != http.ErrServerClosed {
				g.logger.Warn("WS listener failed", "addr", g.cfg.WSListenAddr, "err", err)
			}
		}()
	}

	if !g.cfg.IsEVM {
		host, _, err := net.SplitHostPort(g.cfg.ListenAddr)
		if err != nil {
			stdlog.Fatalf("invalid listen addr %s: %v", g.cfg.ListenAddr, err)
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		addr := net.JoinHostPort(host, strconv.Itoa(g.cfg.PeerDagPort))
		var probeErr error
		for i := 0; i < 5; i++ {
			var conn net.Conn
			conn, probeErr = net.DialTimeout("tcp", addr, time.Second)
			if probeErr == nil {
				conn.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if probeErr != nil {
			stdlog.Fatalf("DAG gateway port probe failed for %s: %v", addr, probeErr)
		}
	}
	return nil
}

// Stop stops the HTTP server.
func (g *Gateway) Stop() error {
	if err := g.Service.Stop(); err != nil {
		return err
	}
	g.peerManager.Stop()
	if g.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = g.srv.Shutdown(ctx)
	}
	if g.wsSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = g.wsSrv.Shutdown(ctx)
	}
	return nil
}
