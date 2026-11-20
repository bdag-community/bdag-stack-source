package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"strings"

	cache "github.com/BlockdagNetworkLabs/bdag/common/lrucache"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	ser "github.com/BlockdagNetworkLabs/bdag/node/service"
	ethcommon "github.com/ethereum/go-ethereum/common"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"golang.org/x/time/rate"
)

// Config holds the settings for a Gateway instance.
type Config struct {
	// ListenAddr is the address to listen on, e.g. ":8545".
	ListenAddr string
	// WSListenAddr is the address to listen on for WebSockets. If empty, WS runs on ListenAddr.
	WSListenAddr string
	// PeerDagPort defines the RPC port peers expose for DAG RPC.
	PeerDagPort int
	// PeerEvmPort defines the RPC port peers expose for EVM RPC.
	PeerEvmPort int
	// InboundAuth enables basic authentication for client->gateway requests.
	InboundAuth bool
	// InboundUser and InboundPass are the credentials required from clients
	// when InboundAuth is true.
	InboundUser string
	InboundPass string
	// InboundLimitUser and InboundLimitPass are the credentials for the
	// read-only limited role when InboundAuth is enabled.
	InboundLimitUser string
	InboundLimitPass string
	// UpUser and UpPass specify optional basic-auth credentials used when the
	// gateway makes upstream RPC calls to the local node or peers.
	UpUser string
	UpPass string
	// UpLimitUser and UpLimitPass are the credentials used when a verified
	// limited request is relayed to a peer.
	UpLimitUser string
	UpLimitPass string
	// AuthorizeLimitedRequest applies the native limited-request policy before
	// this gateway parses, dispatches, relays, or batches a limited request.
	AuthorizeLimitedRequest func(http.ResponseWriter, *http.Request) bool
	// IsEVM indicates whether this gateway handles EVM requests. If false,
	// it handles DAG requests.
	IsEVM bool
	// HTTPTimeout defines the timeout for outbound HTTP requests.
	HTTPTimeout time.Duration
	// RequestTimeout defines the timeout for handling incoming requests.
	RequestTimeout time.Duration
	// CertifiedEVMHeadWait bounds how long a state-dependent EVM request may
	// wait for an in-progress canonical head mutation to finish. A zero value
	// is replaced with DefaultCertifiedEVMHeadWait by New.
	CertifiedEVMHeadWait time.Duration
	// Logger allows injecting a logging implementation.
	Logger l.Logger
	// MaxRequestBytes caps the size of incoming requests. Zero means no limit.
	MaxRequestBytes int64
	// RateLimit defines the maximum requests per second from a single client IP.
	// Zero disables rate limiting.
	RateLimit float64
	// RateBurst defines the allowed burst size for the per-IP rate limiter.
	RateBurst int
	// GlobalRateLimit defines the maximum total requests per second for the gateway.
	GlobalRateLimit float64
	// GlobalRateBurst defines the allowed burst size for the global rate limiter.
	GlobalRateBurst int
	// MaxConcurrentRequests limits the number of requests processed simultaneously.
	MaxConcurrentRequests int
	// CriticalConcurrentRequests reserves request slots for critical local RPCs
	// such as mining health and submit-header calls.
	CriticalConcurrentRequests int
	// ReadTimeout is the maximum duration for reading the entire
	// request, including the body. Defaults to 30s when unset.
	ReadTimeout time.Duration
	// WriteTimeout is the maximum duration before timing out writes of
	// the response. Defaults to 30s when unset.
	WriteTimeout time.Duration
	// IdleTimeout is the maximum amount of time to wait for the next
	// request when keep-alives are enabled. Defaults to 30s when
	// unset.
	IdleTimeout time.Duration
	// AllowedOrigins defines which origins may access the gateway via CORS.
	// An empty list allows all origins.
	AllowedOrigins []string
	// AllowedMethods defines which HTTP methods are allowed for CORS
	// requests. If empty, it defaults to POST and OPTIONS.
	AllowedMethods []string
	// AllowedHeaders defines the headers permitted in CORS requests. If
	// empty, the handler will echo the Access-Control-Request-Headers value
	// from the preflight request when present.
	AllowedHeaders []string
	// AllowCredentials indicates whether the gateway permits credentials in
	// cross-origin requests.
	AllowCredentials bool
	// CORSMaxAge defines how long the results of a preflight request can be
	// cached. Zero disables the header.
	CORSMaxAge time.Duration
	// MiningAddrs defines the reward addresses for patching templates.
	MiningAddrs []string
}

// Validate verifies the configuration values.
// It ensures URLs are non-empty and ports are positive.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		return fmt.Errorf("listen address must be non-empty")
	}
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address %s: %w", c.ListenAddr, err)
	}
	if !isSaneListenHost(host) {
		return fmt.Errorf("invalid listen host %s: must be a valid IP, 'localhost', or empty", host)
	}

	if strings.TrimSpace(c.WSListenAddr) != "" {
		host, _, err := net.SplitHostPort(c.WSListenAddr)
		if err != nil {
			return fmt.Errorf("invalid WS listen address %s: %w", c.WSListenAddr, err)
		}
		if !isSaneListenHost(host) {
			return fmt.Errorf("invalid WS listen host %s: must be a valid IP, 'localhost', or empty", host)
		}
	}

	if c.PeerDagPort <= 0 || c.PeerDagPort > 65535 {
		return fmt.Errorf("peer DAG port must be between 1 and 65535")
	}
	if c.PeerEvmPort <= 0 || c.PeerEvmPort > 65535 {
		return fmt.Errorf("peer EVM port must be between 1 and 65535")
	}
	for _, credential := range []struct {
		name string
		user string
		pass string
	}{
		{"inbound", c.InboundUser, c.InboundPass},
		{"inbound limited", c.InboundLimitUser, c.InboundLimitPass},
		{"upstream", c.UpUser, c.UpPass},
		{"upstream limited", c.UpLimitUser, c.UpLimitPass},
	} {
		if (credential.user == "") != (credential.pass == "") {
			return fmt.Errorf("%s credentials must be provided as a complete pair", credential.name)
		}
	}
	if credentialsConfigured(c.InboundUser, c.InboundPass) &&
		credentialsConfigured(c.InboundLimitUser, c.InboundLimitPass) &&
		credentialsEqual(c.InboundUser, c.InboundPass, c.InboundLimitUser, c.InboundLimitPass) {
		return fmt.Errorf("inbound limited credentials must differ from primary credentials")
	}
	if credentialsConfigured(c.UpUser, c.UpPass) &&
		credentialsConfigured(c.UpLimitUser, c.UpLimitPass) &&
		credentialsEqual(c.UpUser, c.UpPass, c.UpLimitUser, c.UpLimitPass) {
		return fmt.Errorf("upstream limited credentials must differ from primary credentials")
	}
	if c.InboundAuth && !credentialsConfigured(c.InboundUser, c.InboundPass) {
		return fmt.Errorf("inbound authentication requires a valid primary credential pair")
	}
	return nil
}

func credentialsConfigured(user, pass string) bool {
	return user != "" && pass != ""
}

func credentialsEqual(userA, passA, userB, passB string) bool {
	a := authDigest(userA, passA)
	b := authDigest(userB, passB)
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func authDigest(user, pass string) [sha256.Size]byte {
	return sha256.Sum256([]byte("Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))))
}

// Gateway implements a JSON-RPC proxy with sync awareness.
type Gateway struct {
	ser.Service
	cfg Config
	// EvmHandler is the in-process handler for EVM requests.
	EvmHandler http.Handler
	// EvmWSHandler is the in-process handler for EVM WebSocket requests.
	EvmWSHandler http.Handler
	// DagHandler is the in-process handler for DAG requests.
	DagHandler http.Handler
	// SyncChecker provides access to the node's sync status.
	SyncChecker SyncChecker

	// PeerManager handles peer discovery and selection.
	peerManager *PeerManager

	// Legacy fields (kept for peer forwarding logic)
	client      *http.Client
	probeClient *http.Client
	srv         *http.Server
	wsSrv       *http.Server

	logger l.Logger

	limitMu  sync.Mutex
	limiters map[string]*rate.Limiter
	limiterS *cache.Cache[*rate.Limiter]

	// Global rate limiter for the entire gateway.
	globalLimiter *rate.Limiter
	// Semaphore to limit concurrent requests.
	sem         chan struct{}
	criticalSem chan struct{}

	// tplCache stores recently patched block templates for reconstruction.
	tplCache *cache.Cache[*TemplateCacheEntry]

	// authPorts stores the set of ports that require authentication.
	authPorts map[string]bool

	primaryAuthDigest [sha256.Size]byte
	limitedAuthDigest [sha256.Size]byte
}

type gatewayRole uint8

const (
	roleUnknown gatewayRole = iota
	rolePrimary
	roleLimited
	roleInternal
)

// TemplateCacheEntry holds the necessary data to reconstruct a full block.
type TemplateCacheEntry struct {
	Transactions []*etypes.Transaction
	Header       types.BlockHeader
	Parents      []*ethcommon.Hash
	Height       int64
	Reward       uint64
}

type SyncChecker interface {
	IsSyncing() bool
	EvmHeight() uint64
	EVMHead() (height uint64, visible bool)
	WithCommittedEVMHead(read func()) bool
	DagHeight() uint64
	CalculateStateRoot(block *types.Block) *ethcommon.Hash
}

type nativeEVMHeightProvider interface {
	NativeEVMHeight() uint64
}

const (
	// DefaultHTTPTimeout is used if HTTPTimeout is not specified.
	DefaultHTTPTimeout = 30 * time.Second
	// DefaultRequestTimeout is used if RequestTimeout is not specified.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultCertifiedEVMHeadWait lets long ordered EVM commit batches settle
	// within the public RPC request deadline without weakening the committed
	// native/EVM generation check.
	DefaultCertifiedEVMHeadWait = 30 * time.Second
	// DefaultMaxConcurrentRequests is used if MaxConcurrentRequests is not specified.
	DefaultMaxConcurrentRequests = 1000
	// DefaultCriticalConcurrentRequests is used if CriticalConcurrentRequests is not specified.
	DefaultCriticalConcurrentRequests = 16
)

// EVM wraps Gateway to give it a distinct service type for EVM calls.
type EVM struct {
	*Gateway
}

// DAG wraps Gateway to give it a distinct service type for DAG calls.
type DAG struct {
	*Gateway
}

func isSaneListenHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	return net.ParseIP(host) != nil
}
