package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	ser "github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/rpc/websocket"
	mapset "github.com/deckarep/golang-set"
)

// RpcServer provides a concurrent safe RPC server to a chain server.
type RpcServer struct {
	ser.Service
	wg         sync.WaitGroup
	quit       chan int
	statusLock sync.RWMutex

	config *config.Config

	rpcSvcRegistry serviceRegistry

	codecsMu sync.Mutex
	codecs   mapset.Set

	authsha            [sha256.Size]byte
	authLimitSha       [sha256.Size]byte
	limitedAuthEnabled bool
	numClients         int32
	clientsMu          sync.Mutex
	clients            map[string]int
	statusLines        map[int]string
	requestSem         semaphore
	criticalRequestSem semaphore
	classificationSem  semaphore
	normalRequests     int32
	criticalRequests   int32
	withRequestTimeout func(context.Context, time.Duration) (context.Context, context.CancelFunc)

	requestProcessShutdown chan struct{}

	ReqStatus     map[string]*RequestStatus
	reqStatusLock sync.RWMutex

	ntfnMgr     *wsNotificationManager
	BC          *blockchain.BlockChain
	ChainParams *params.Params
	listeners   []net.Listener
	consensus   model.Consensus
}

// service represents a registered object
type service struct {
	svcNamespace  string        //the name space for service
	svcType       reflect.Type  // receiver type
	callbacks     callbacks     // registered service method
	subscriptions subscriptions // available subscriptions/notifications
}

// callback is a method callback which was registered in the server
type callback struct {
	receiver    reflect.Value  // receiver of method
	method      reflect.Method // callback
	argTypes    []reflect.Type // input argument types
	hasCtx      bool           // method's first argument is a context (not included in argTypes)
	errPos      int            // err return idx, of -1 when method cannot return error
	isSubscribe bool           // indication if the callback is a subscription
}

// serviceRegistry is the collection of services by namespace
type serviceRegistry map[string]*service

// callbacks is the collection of RPC callbacks
type callbacks map[string]*callback

// subscriptions is the collection of subscription callbacks
type subscriptions map[string]*callback

// rpcRequest represents a raw incoming RPC request
type rpcRequest struct {
	service  string
	method   string
	id       any
	isPubSub bool
	params   any
	err      Error // invalid batch element
}

// serverRequest is an incoming request
type serverRequest struct {
	id            any
	svcname       string
	callb         *callback
	args          []reflect.Value
	isUnsubscribe bool
	err           Error
	time          time.Time
}

// newRPCServer returns a new instance of the rpcServer struct.
func NewRPCServer(cfg *config.Config, consensus model.Consensus) (*RpcServer, error) {
	if err := cfg.ValidateRPCAuth(); err != nil {
		return nil, err
	}
	rpc := RpcServer{
		consensus:          consensus,
		config:             cfg,
		rpcSvcRegistry:     make(serviceRegistry),
		codecs:             mapset.NewSet(),
		clients:            make(map[string]int),
		withRequestTimeout: context.WithTimeout,

		statusLines:            make(map[int]string),
		requestProcessShutdown: make(chan struct{}),
		quit:                   make(chan int),
		ReqStatus:              map[string]*RequestStatus{},
	}
	if cfg.RPCMaxConcurrent > 0 {
		rpc.requestSem = makeSemaphore(cfg.RPCMaxConcurrent)
	}
	if cfg.RPCCriticalConcurrent > 0 {
		rpc.criticalRequestSem = makeSemaphore(cfg.RPCCriticalConcurrent)
		rpc.classificationSem = makeSemaphore(cfg.RPCCriticalConcurrent)
	}
	rpcClientCapacityMetric.Update(int64(cfg.RPCMaxClients))
	rpcClientPerHostCapacityMetric.Update(int64(cfg.RPCMaxClientsPerHost))
	rpcNormalRequestCapacityMetric.Update(int64(cfg.RPCMaxConcurrent))
	rpcCriticalRequestCapacityMetric.Update(int64(cfg.RPCCriticalConcurrent))
	rpcClientCriticalReserveCapacityMetric.Update(int64(cfg.RPCCriticalConcurrent))
	rpcClassificationCapacityMetric.Update(int64(cfg.RPCCriticalConcurrent))

	if cfg.RPCUser != "" && cfg.RPCPass != "" {
		login := cfg.RPCUser + ":" + cfg.RPCPass
		auth := "Basic " +
			base64.StdEncoding.EncodeToString([]byte(login))
		rpc.authsha = sha256.Sum256([]byte(auth))
	}
	if cfg.RPCLimitUser != "" && cfg.RPCLimitPass != "" {
		login := cfg.RPCLimitUser + ":" + cfg.RPCLimitPass
		auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(login))
		rpc.authLimitSha = sha256.Sum256([]byte(auth))
		rpc.limitedAuthEnabled = true
	}
	rpc.ntfnMgr = newWsNotificationManager(&rpc)
	if consensus != nil {
		rpc.subscribe(consensus.Events())
	}

	return &rpc, nil
}

func (s *RpcServer) Start() error {
	if err := s.Service.Start(); err != nil {
		return err
	}
	err := s.startHTTP(s.config.RPCListeners)
	if err != nil {
		return err
	}
	s.ntfnMgr.Start()
	return nil
}

// Stop will stop reading new requests, wait for stopPendingRequestTimeout to allow pending requests to finish,
// close all codecs which will cancel pending requests/subscriptions.
func (s *RpcServer) Stop() error {
	if err := s.Service.Stop(); err != nil {
		return err
	}
	log.Debug("RPC Server is stopping")

	for _, listener := range s.listeners {
		err := listener.Close()
		if err != nil {
			log.Error(fmt.Sprintf("Problem shutting down rpc: %v", err))
		}
	}

	s.codecsMu.Lock()
	defer s.codecsMu.Unlock()
	s.codecs.Each(func(c any) bool {
		c.(ServerCodec).Close()
		return true
	})

	s.ntfnMgr.Stop()

	close(s.quit)
	s.wg.Wait()

	return nil
}

const (
	// rpcAuthTimeoutSeconds is the number of seconds a connection to the
	// RPC server is allowed to stay open without authenticating before it
	// is closed.
	rpcAuthTimeoutSeconds = 10
	rpcReadHeaderTimeout  = 5 * time.Second
	rpcReadTimeout        = 30 * time.Second
	rpcIdleTimeout        = 10 * time.Second
)

func (s *RpcServer) Handler() http.Handler {
	rpcServeMux := http.NewServeMux()
	rpcServeMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		w.Header().Set("Content-Type", "application/json")
		r.Close = true

		// Apply the server request deadline to the complete handler lifetime so
		// client leases are recycled even when a connected caller invokes a
		// legacy callback that does not accept a context.
		if s.config.RPCRequestTimeout > 0 {
			withTimeout := s.withRequestTimeout
			if withTimeout == nil {
				withTimeout = context.WithTimeout
			}
			ctx, cancel := withTimeout(r.Context(), s.config.RPCRequestTimeout)
			defer cancel()
			r = r.WithContext(ctx)
		}

		// Admit normal clients without reading their bodies. If the normal
		// client budget is already full, authenticate and classify the bounded
		// request before allowing only block submission into the protected
		// overflow lease budget.
		method := ""
		critical := false
		authenticated := false
		admin := false
		clientLimit, acquiredClient := s.tryAcquireClient(r.RemoteAddr, 0)
		if !acquiredClient {
			classificationLimit, acquiredClassifier := s.tryAcquireClassifier()
			if !acquiredClassifier {
				s.rejectClient(w, r.RemoteAddr, "", false, classificationLimit)
				return
			}
			classifierHeld := true
			defer func() {
				if classifierHeld {
					s.releaseClassifier()
				}
			}()
			var err error
			admin, err = s.checkAuth(r, true)
			if err != nil {
				jsonAuthFail(w)
				return
			}
			authenticated = true
			if !admin {
				method, err = s.authorizeLimitedRequest(r)
				if err != nil {
					rejectLimitedRequest(w, err)
					return
				}
			}
			if method == "" {
				method = rpcRequestMethod(r)
			}
			critical = s.criticalRequestSem != nil && isCriticalRPCMethod(method)
			if critical {
				clientLimit, acquiredClient = s.tryAcquireClient(r.RemoteAddr, s.config.RPCCriticalConcurrent)
			}
			s.releaseClassifier()
			classifierHeld = false
			if !acquiredClient {
				s.rejectClient(w, r.RemoteAddr, method, critical, clientLimit)
				return
			}
		}
		// A callback that ignores its request context can outlive the HTTP
		// client that invoked it. Release the connection slot as soon as the
		// client disconnects instead of retaining it until that callback
		// eventually returns. requestSem continues to bound callbacks that
		// remain in flight after their clients have gone away.
		var releaseOnce sync.Once
		releaseClient := func() {
			releaseOnce.Do(func() {
				s.releaseClient(r.RemoteAddr)
			})
		}
		stopReleaseOnDisconnect := context.AfterFunc(r.Context(), releaseClient)
		defer stopReleaseOnDisconnect()
		defer releaseClient()
		if !authenticated {
			var err error
			admin, err = s.checkAuth(r, true)
			if err != nil {
				jsonAuthFail(w)
				return
			}
		}
		if !admin && method == "" {
			var err error
			method, err = s.authorizeLimitedRequest(r)
			if err != nil {
				rejectLimitedRequest(w, err)
				return
			}
		}
		if method == "" {
			method = rpcRequestMethod(r)
			critical = s.criticalRequestSem != nil && isCriticalRPCMethod(method)
		}
		if err := r.Context().Err(); err != nil {
			http.Error(w, err.Error(), http.StatusGatewayTimeout)
			return
		}
		if !s.acquireRequest(w, r.RemoteAddr, method, critical) {
			return
		}
		defer s.releaseRequest(critical)
		// Read and respond to the request.
		s.jsonRPCRead(w, r)
	})

	// Websocket endpoint.
	rpcServeMux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		isAdmin, err := s.checkAuth(r, true)
		if err != nil {
			jsonAuthFail(w)
			return
		}
		if !isAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// Attempt to upgrade the connection to a websocket connection
		// using the default size for read/write buffers.
		ws, err := websocket.Upgrade(w, r, nil, 0, 0)
		if err != nil {
			if _, ok := err.(websocket.HandshakeError); !ok {
				log.Error(fmt.Sprintf("Unexpected websocket error: %v", err))
			}
			http.Error(w, "400 Bad Request.", http.StatusBadRequest)
			return
		}
		s.WebsocketHandler(ws, r.RemoteAddr, isAdmin)
	})
	return rpcServeMux
}

func (s *RpcServer) startHTTP(listenAddrs []string) error {
	handler := s.Handler()
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: rpcReadHeaderTimeout,
		ReadTimeout:       rpcReadTimeout,
		WriteTimeout:      s.rpcWriteTimeout(),
		IdleTimeout:       rpcIdleTimeout,
	}

	listeners, err := parseListeners(s.config, listenAddrs)
	if err != nil {
		return err
	}
	s.listeners = listeners
	for _, listener := range listeners {
		s.wg.Add(1)
		go func(listener net.Listener) {
			log.Info(fmt.Sprintf("RPC server listening on addr:%v", listener.Addr()))
			if err := httpServer.Serve(listener); err != nil {
				log.Error(fmt.Sprintf("Error while serving listener %v: %v", listener.Addr(), err))
			}
			log.Info(fmt.Sprintf("RPC listener done for %v", listener.Addr()))
			s.wg.Done()
		}(listener)
	}
	return nil
}

func (s *RpcServer) rpcWriteTimeout() time.Duration {
	if s.config.RPCRequestTimeout <= 0 {
		return 0
	}
	return s.config.RPCRequestTimeout + 5*time.Second
}

const rpcMethodPeekBytes = 64 << 10

type bufferedRequestBody struct {
	*bufio.Reader
	io.Closer
}

// rpcRequestMethod extracts only the top-level method while leaving the body
// untouched for the normal JSON-RPC decoder. Requests whose method is not in
// the bounded prefix and batches are conservatively treated as normal work.
func rpcRequestMethod(r *http.Request) string {
	if r == nil || r.Body == nil {
		return ""
	}
	reader := bufio.NewReaderSize(r.Body, rpcMethodPeekBytes)
	r.Body = &bufferedRequestBody{Reader: reader, Closer: r.Body}

	peekBytes := rpcMethodPeekBytes
	if r.ContentLength >= 0 && r.ContentLength < int64(peekBytes) {
		peekBytes = int(r.ContentLength)
	}
	if peekBytes == 0 {
		return ""
	}
	prefix, peekErr := reader.Peek(peekBytes)
	complete := r.ContentLength >= 0 && r.ContentLength <= rpcMethodPeekBytes
	if r.ContentLength < 0 && peekErr == io.EOF {
		complete = true
	}
	if complete {
		if err := rejectDuplicateJSONField(prefix, "method"); err != nil {
			return ""
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(prefix, &request); err != nil {
			return ""
		}
		return request.Method
	}

	decoder := json.NewDecoder(bytes.NewReader(prefix))
	token, err := decoder.Token()
	if err != nil {
		return ""
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return ""
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ""
		}
		key, ok := keyToken.(string)
		if !ok {
			return ""
		}
		if strings.EqualFold(key, "method") {
			var method string
			if err := decoder.Decode(&method); err == nil {
				return method
			}
			return ""
		}
		var ignored json.RawMessage
		if err := decoder.Decode(&ignored); err != nil {
			return ""
		}
	}
	return ""
}

var errLimitedMethodForbidden = errors.New("limited rpc method forbidden")

// limitedRPCMethods is intentionally an exact, namespace-free allowlist. The
// request parser below examines the same bytes that the normal handler later
// receives, while rejecting ambiguous envelopes before dispatch.
var limitedRPCMethods = map[string]struct{}{
	"getBlockCount":          {},
	"getBlock":               {},
	"getBlockByOrder":        {},
	"tips":                   {},
	"getCoinbaseAddress":     {},
	"isCurrent":              {},
	"getPeerInfo":            {},
	"getPeerForks":           {},
	"getPeerList":            {},
	"getPeerDiscoveryStatus": {},
}

// authorizeLimitedRequest buffers and restores the complete request body so
// authorization cannot inspect different bytes than JSON-RPC dispatch.
func (s *RpcServer) authorizeLimitedRequest(r *http.Request) (string, error) {
	if r == nil || r.Body == nil {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestContentLength+1))
	if err != nil {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if int64(len(body)) > maxRequestContentLength {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	if delim == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			return "", fmt.Errorf("malformed limited rpc request")
		}
		return "", errLimitedMethodForbidden
	}
	if delim != '{' {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	seen := make(map[string]struct{})
	method := ""
	methodPresent := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("malformed limited rpc request")
		}
		key, ok := keyToken.(string)
		if !ok {
			return "", fmt.Errorf("malformed limited rpc request")
		}
		if _, exists := seen[key]; exists {
			return "", fmt.Errorf("malformed limited rpc request")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("malformed limited rpc request")
		}
		if key == "method" {
			methodPresent = true
			if len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '"' || json.Unmarshal(value, &method) != nil {
				return "", fmt.Errorf("malformed limited rpc request")
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	if !methodPresent {
		return "", fmt.Errorf("malformed limited rpc request")
	}
	if _, ok := limitedRPCMethods[method]; !ok {
		return "", errLimitedMethodForbidden
	}
	return method, nil
}

// AuthorizeLimitedRequest applies the native limited-RPC request policy to a
// request before dispatch. The native authorizer restores the complete body on
// both success and rejection, so gateway callers can safely invoke it before
// their own routing/parsing while retaining one authoritative allowlist.
func (s *RpcServer) AuthorizeLimitedRequest(w http.ResponseWriter, r *http.Request) bool {
	if s == nil {
		rejectLimitedRequest(w, fmt.Errorf("malformed limited rpc request"))
		return false
	}
	if _, err := s.authorizeLimitedRequest(r); err != nil {
		rejectLimitedRequest(w, err)
		return false
	}
	return true
}

func rejectLimitedRequest(w http.ResponseWriter, err error) {
	if errors.Is(err, errLimitedMethodForbidden) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.Error(w, "malformed RPC request.", http.StatusBadRequest)
}

func isCriticalRPCMethod(method string) bool {
	if separator := strings.LastIndexByte(method, serviceMethodSeparator[0]); separator >= 0 {
		method = method[separator+1:]
	}
	switch method {
	case "submitBlock", "SubmitBlock",
		"submitBlockHeader", "SubmitBlockHeader",
		"submitBlockHeaderV2", "SubmitBlockHeaderV2":
		return true
	default:
		return false
	}
}

func (s *RpcServer) tryAcquireClassifier() (rpcClientLimit, bool) {
	if s.classificationSem == nil {
		return rpcClientLimit{scope: "classifier"}, false
	}
	if !s.classificationSem.tryAcquire() {
		return rpcClientLimit{scope: "classifier", max: cap(s.classificationSem), active: len(s.classificationSem)}, false
	}
	rpcClassificationActiveMetric.Inc(1)
	return rpcClientLimit{}, true
}

func (s *RpcServer) releaseClassifier() {
	rpcClassificationActiveMetric.Dec(1)
	s.classificationSem.release()
}

func (s *RpcServer) acquireRequest(w http.ResponseWriter, remoteAddr, method string, critical bool) bool {
	requestClass := "normal"
	sem := s.requestSem
	limit := s.config.RPCMaxConcurrent
	if critical {
		requestClass = "critical"
		sem = s.criticalRequestSem
		limit = s.config.RPCCriticalConcurrent
	}
	if sem != nil && !sem.tryAcquire() {
		rpcRequestRejectMetric.Inc(1)
		log.Info("RPC requests exceeded", "class", requestClass, "method", method, "max", limit, "client", remoteAddr)
		w.Header().Set("Retry-After", "1")
		http.Error(w, "503 Too busy.  Try again later.", http.StatusServiceUnavailable)
		return false
	}
	if critical {
		atomic.AddInt32(&s.criticalRequests, 1)
		rpcCriticalRequestActiveMetric.Inc(1)
	} else {
		atomic.AddInt32(&s.normalRequests, 1)
		rpcNormalRequestActiveMetric.Inc(1)
	}
	return true
}

func (s *RpcServer) releaseRequest(critical bool) {
	if critical {
		atomic.AddInt32(&s.criticalRequests, -1)
		rpcCriticalRequestActiveMetric.Dec(1)
		if s.criticalRequestSem != nil {
			s.criticalRequestSem.release()
		}
		return
	}
	atomic.AddInt32(&s.normalRequests, -1)
	rpcNormalRequestActiveMetric.Dec(1)
	if s.requestSem != nil {
		s.requestSem.release()
	}
}

func rpcClientHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	return remoteAddr
}

type rpcClientLimit struct {
	scope  string
	max    int
	active int
	host   string
}

// tryAcquireClient reserves one standard RPC client lease. reserve is zero for
// normal work and the protected overflow allowance for classified submissions.
// Websocket clients have independent limits.
func (s *RpcServer) tryAcquireClient(remoteAddr string, reserve int) (rpcClientLimit, bool) {
	host := rpcClientHost(remoteAddr)
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	active := int(atomic.LoadInt32(&s.numClients))
	globalMax := s.config.RPCMaxClients
	if globalMax > 0 {
		globalMax += reserve
		if active+1 > globalMax {
			return rpcClientLimit{scope: "global", max: globalMax, active: active, host: host}, false
		}
	}
	hostActive := s.clients[host]
	hostMax := s.config.RPCMaxClientsPerHost
	if hostMax > 0 {
		hostMax += reserve
		if hostActive+1 > hostMax {
			return rpcClientLimit{scope: "host", max: hostMax, active: hostActive, host: host}, false
		}
	}
	s.clients[host] = hostActive + 1
	atomic.AddInt32(&s.numClients, 1)
	rpcClientActiveMetric.Inc(1)
	return rpcClientLimit{}, true
}

func (s *RpcServer) rejectClient(w http.ResponseWriter, remoteAddr, method string, critical bool, limit rpcClientLimit) {
	requestClass := "normal"
	if critical {
		requestClass = "critical"
	}
	rpcClientRejectMetric.Inc(1)
	message := "RPC clients exceeded"
	if limit.scope == "classifier" {
		message = "RPC overflow classifiers exceeded"
	} else if limit.scope == "host" {
		message = "RPC clients per host exceeded"
	}
	log.Info(message, "scope", limit.scope, "class", requestClass, "method", method,
		"max", limit.max, "active", limit.active, "host", limit.host, "client", remoteAddr)
	w.Header().Set("Retry-After", "1")
	http.Error(w, "503 Too busy.  Try again later.", http.StatusServiceUnavailable)
}

// releaseClient releases one standard RPC client slot.
//
// This function is safe for concurrent access.
func (s *RpcServer) releaseClient(remoteAddr string) {
	host := rpcClientHost(remoteAddr)
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	if active := s.clients[host]; active <= 1 {
		delete(s.clients, host)
	} else {
		s.clients[host] = active - 1
	}
	if atomic.LoadInt32(&s.numClients) <= 0 {
		atomic.StoreInt32(&s.numClients, 0)
		return
	}
	atomic.AddInt32(&s.numClients, -1)
	rpcClientActiveMetric.Dec(1)
}

// ActiveClients returns the current standard RPC client count.
func (s *RpcServer) ActiveClients() int {
	return int(atomic.LoadInt32(&s.numClients))
}

// ActiveRequests returns normal and mining-critical callbacks that are still
// executing. Unlike client leases, these counts deliberately remain held by a
// context-ignorant callback after its caller disconnects, bounding orphan work.
func (s *RpcServer) ActiveRequests() (normal, critical int) {
	return int(atomic.LoadInt32(&s.normalRequests)), int(atomic.LoadInt32(&s.criticalRequests))
}

// TODO, repalace Basic Authentication
// checkAuth checks the HTTP Basic authentication supplied by a wallet or RPC
// client in the HTTP request r.  If the supplied authentication does not match
// the username and password expected, a non-nil error is returned.
//
// This check is time-constant.
func (s *RpcServer) checkAuth(r *http.Request, require bool) (bool, error) {
	authhdr := r.Header["Authorization"]
	if len(authhdr) != 1 {
		if require {
			log.Warn("RPC authentication failure", "from", r.RemoteAddr,
				"error", "missing or duplicated authorization header")
			return false, fmt.Errorf("auth failure")
		}

		return false, nil
	}

	authsha := sha256.Sum256([]byte(authhdr[0]))

	// Compare both fixed-size role digests unconditionally. The primary role is
	// the only one that receives admin authority.
	primaryMatch := subtle.ConstantTimeCompare(authsha[:], s.authsha[:])
	limitedMatch := subtle.ConstantTimeCompare(authsha[:], s.authLimitSha[:])
	if primaryMatch == 1 {
		return true, nil
	}
	if limitedMatch == 1 && s.limitedAuthEnabled {
		return false, nil
	}

	// Request's auth doesn't match either user
	log.Warn("RPC authentication failure", "from", r.RemoteAddr)
	return false, fmt.Errorf("auth failure")
}

// jsonAuthFail sends a message back to the client if the http auth is rejected.
func jsonAuthFail(w http.ResponseWriter) {
	w.Header().Add("WWW-Authenticate", `Basic realm="Blockdag RPC"`)
	http.Error(w, "401 Unauthorized.", http.StatusUnauthorized)
}

// CodecOption specifies which type of messages this codec supports
type CodecOption int

const (
	// OptionMethodInvocation is an indication that the codec supports RPC method calls
	OptionMethodInvocation CodecOption = 1 << iota

	// OptionSubscriptions is an indication that the codec suports RPC notifications
	OptionSubscriptions = 1 << iota // support pub sub
)

// jsonRPCRead handles reading and responding to RPC messages.
func (s *RpcServer) jsonRPCRead(w http.ResponseWriter, r *http.Request) {
	if s.IsShutdown() { // server stopped
		return
	}
	// discard dumb empty requests
	if emptyRequest(r) {
		log.Trace("Discard empty request", "from", r.RemoteAddr, "request", r)
		return
	}
	// validate request
	if code, err := validateRequest(r); err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	// All checks passed, create a codec that reads direct from the request body
	// untilEOF and writes the response to w and order the server to process a
	// single request.
	ctx := r.Context()
	ctx = context.WithValue(ctx, "remote", r.RemoteAddr)
	ctx = context.WithValue(ctx, "scheme", r.Proto)
	ctx = context.WithValue(ctx, "local", r.Host)

	// Read and close the JSON-RPC request body from the caller.
	body := io.LimitReader(r.Body, maxRequestContentLength)
	codec := NewJSONCodec(&httpReadWriteNopCloser{body, w})
	defer codec.Close()

	s.ServeSingleRequest(ctx, codec, OptionMethodInvocation)
}

// ServeSingleRequest reads and processes a single RPC request from the given codec. It will not
// close the codec unless a non-recoverable error has occurred. Note, this method will return after
// a single request has been processed!
func (s *RpcServer) ServeSingleRequest(ctx context.Context, codec ServerCodec, options CodecOption) {
	if err := s.serveRequest(ctx, codec, true, options); err != nil {
		log.Error(fmt.Sprintf("Error serving single request: %v", err))
	}
}

// serveRequest will reads requests from the codec, calls the RPC callback and
// writes the response to the given codec.
//
// If singleShot is true it will process a single request, otherwise it will handle
// requests until the codec returns an error when reading a request (in most cases
// an EOF). It executes requests in parallel when singleShot is false.
func (s *RpcServer) serveRequest(ctx context.Context, codec ServerCodec, singleShot bool, options CodecOption) error {
	var pend sync.WaitGroup

	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error(string(buf))
		}
		s.codecsMu.Lock()
		s.codecs.Remove(codec)
		s.codecsMu.Unlock()
	}()

	//	ctx, cancel := context.WithCancel(context.Background())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// if the codec supports notification include a notifier that callbacks can use
	// to send notification to clients. It is tied to the codec/connection. If the
	// connection is closed the notifier will stop and cancels all active subscriptions.
	if options&OptionSubscriptions == OptionSubscriptions {
		ctx = context.WithValue(ctx, notifierKey{}, newNotifier(codec))
	}
	s.codecsMu.Lock()
	if s.IsShutdown() { // server stopped
		s.codecsMu.Unlock()
		return &shutdownError{}
	}
	s.codecs.Add(codec)
	s.codecsMu.Unlock()

	// test if the server is ordered to stop
	for s.IsStarted() {
		reqs, batch, err := s.readRequest(codec)
		if err != nil {
			// If a parsing error occurred, send an error
			if err.Error() != "EOF" {
				log.Debug(fmt.Sprintf("read error %v\n", err))
				if err := codec.Write(codec.CreateErrorResponse(nil, err)); err != nil {
					log.Error(fmt.Sprintf("Error writing generic error response: %v", err))
				}
			}
			// Error or end of stream, wait for requests and tear down
			pend.Wait()
			return nil
		}

		// check if server is ordered to shutdown and return an error
		// telling the client that his request failed.
		if s.IsShutdown() {
			err = &shutdownError{}
			if batch {
				resps := make([]any, len(reqs))
				for i, r := range reqs {
					resps[i] = codec.CreateErrorResponse(&r.id, err)
				}
				if err := codec.Write(resps); err != nil {
					log.Error(fmt.Sprintf("Error writing response: %v", err))
				}
			} else {
				if err := codec.Write(codec.CreateErrorResponse(&reqs[0].id, err)); err != nil {
					log.Error(fmt.Sprintf("Error writing error response for request %d: %v", reqs[0].id, err))
				}
			}
			return nil
		}
		// If a single shot request is executing, run and return immediately
		if singleShot {
			if batch {
				s.execBatch(ctx, codec, reqs)
			} else {
				s.exec(ctx, codec, reqs[0])
			}
			return nil
		}
		// For multi-shot connections, start a goroutine to serve and loop back
		pend.Add(1)

		go func(reqs []*serverRequest, batch bool) {
			defer pend.Done()
			if batch {
				s.execBatch(ctx, codec, reqs)
			} else {
				s.exec(ctx, codec, reqs[0])
			}
		}(reqs, batch)
	}
	return nil
}

// httpReadWriteNopCloser wraps a io.Reader and io.Writer with a NOP Close method.
type httpReadWriteNopCloser struct {
	io.Reader
	io.Writer
}

// Close does nothing and returns always nil
func (t *httpReadWriteNopCloser) Close() error {
	return nil
}

// readRequest requests the next (batch) request from the codec. It will return the collection
// of requests, an indication if the request was a batch, the invalid request identifier and an
// error when the request could not be read/parsed.
func (s *RpcServer) readRequest(codec ServerCodec) ([]*serverRequest, bool, Error) {
	reqs, batch, err := codec.ReadRequestHeaders()
	if err != nil {
		return nil, batch, err
	}

	requests := make([]*serverRequest, len(reqs))

	// verify requests
	for i, r := range reqs {
		var ok bool
		var svc *service

		if r.err != nil {
			requests[i] = &serverRequest{id: r.id, err: r.err}
			continue
		}

		if r.isPubSub && strings.HasSuffix(r.method, unsubscribeMethodSuffix) {
			requests[i] = &serverRequest{id: r.id, isUnsubscribe: true}
			argTypes := []reflect.Type{reflect.TypeOf("")} // expect subscription id as first arg
			if args, err := codec.ParseRequestArguments(argTypes, r.params); err == nil {
				requests[i].args = args
			} else {
				requests[i].err = &invalidParamsError{err.Error()}
			}
			continue
		}

		if svc, ok = s.rpcSvcRegistry[r.service]; !ok { // rpc method isn't available
			requests[i] = &serverRequest{id: r.id, err: &methodNotFoundError{r.service, r.method}}
			continue
		}

		if r.isPubSub { // eth_subscribe, r.method contains the subscription method name
			if callb, ok := svc.subscriptions[r.method]; ok {
				requests[i] = &serverRequest{id: r.id, svcname: svc.svcNamespace, callb: callb}
				if r.params != nil && len(callb.argTypes) > 0 {
					argTypes := []reflect.Type{reflect.TypeOf("")}
					argTypes = append(argTypes, callb.argTypes...)
					if args, err := codec.ParseRequestArguments(argTypes, r.params); err == nil {
						requests[i].args = args[1:] // first one is service.method name which isn't an actual argument
					} else {
						requests[i].err = &invalidParamsError{err.Error()}
					}
				}
			} else {
				requests[i] = &serverRequest{id: r.id, err: &methodNotFoundError{r.service, r.method}}
			}
			continue
		}

		if callb, ok := svc.callbacks[r.method]; ok { // lookup RPC method
			requests[i] = &serverRequest{id: r.id, svcname: svc.svcNamespace, callb: callb}
			if r.params != nil && len(callb.argTypes) > 0 {
				if args, err := codec.ParseRequestArguments(callb.argTypes, r.params); err == nil {
					requests[i].args = args
				} else {
					requests[i].err = &invalidParamsError{err.Error()}
				}
			}
			continue
		}

		requests[i] = &serverRequest{id: r.id, err: &methodNotFoundError{r.service, r.method}}
	}

	return requests, batch, nil
}

// execBatch executes the given requests and writes the result back using the codec.
// It will only write the response back when the last request is processed.
func (s *RpcServer) execBatch(ctx context.Context, codec ServerCodec, requests []*serverRequest) {
	responses := make([]any, len(requests))
	var callbacks []func()
	for i, req := range requests {
		if req.err != nil {
			responses[i] = codec.CreateErrorResponse(&req.id, req.err)
		} else {
			var callback func()
			if responses[i], callback = s.handle(ctx, codec, req); callback != nil {
				callbacks = append(callbacks, callback)
			}
		}
	}

	if err := codec.Write(responses); err != nil {
		log.Error(fmt.Sprintf("%v\n", err))
		codec.Close()
	}

	// when request holds one of more subscribe requests this allows these subscriptions to be activated
	for _, c := range callbacks {
		c()
	}
}

// exec executes the given request and writes the result back using the codec.
func (s *RpcServer) exec(ctx context.Context, codec ServerCodec, req *serverRequest) {
	var response any
	var callback func()
	if req.err != nil {
		response = codec.CreateErrorResponse(&req.id, req.err)
	} else {
		response, callback = s.handle(ctx, codec, req)
	}

	if err := codec.Write(response); err != nil {
		log.Error(fmt.Sprintf("%v\n", err))
		codec.Close()
	}

	// when request was a subscribe request this allows these subscriptions to be actived
	if callback != nil {
		callback()
	}
}

// handle executes a request and returns the response from the callback.
func (s *RpcServer) handle(ctx context.Context, codec ServerCodec, req *serverRequest) (any, func()) {
	if req.err != nil {
		return codec.CreateErrorResponse(&req.id, req.err), nil
	}

	if req.isUnsubscribe { // cancel subscription, first param must be the subscription id
		if len(req.args) >= 1 && req.args[0].Kind() == reflect.String {
			notifier, supported := NotifierFromContext(ctx)
			if !supported { // interface doesn't support subscriptions (e.g. http)
				return codec.CreateErrorResponse(&req.id, &callbackError{ErrNotificationsUnsupported.Error()}), nil
			}

			subid := ID(req.args[0].String())
			if err := notifier.unsubscribe(subid); err != nil {
				return codec.CreateErrorResponse(&req.id, &callbackError{err.Error()}), nil
			}

			return codec.CreateResponse(req.id, true), nil
		}
		return codec.CreateErrorResponse(&req.id, &invalidParamsError{"Expected subscription id as first argument"}), nil
	}

	if req.callb.isSubscribe {
		subid, err := s.createSubscription(ctx, codec, req)
		if err != nil {
			return codec.CreateErrorResponse(&req.id, &callbackError{err.Error()}), nil
		}

		// active the subscription after the sub id was successfully sent to the client
		activateSub := func() {
			notifier, _ := NotifierFromContext(ctx)
			notifier.activate(subid, req.svcname)
		}

		return codec.CreateResponse(req.id, subid), activateSub
	}
	// regular RPC call, prepare arguments
	if len(req.args) != len(req.callb.argTypes) {
		rpcErr := &invalidParamsError{fmt.Sprintf("%s%s%s expects %d parameters, got %d",
			req.svcname, serviceMethodSeparator, req.callb.method.Name,
			len(req.callb.argTypes), len(req.args))}
		return codec.CreateErrorResponse(&req.id, rpcErr), nil
	}

	arguments := []reflect.Value{req.callb.receiver}
	if req.callb.hasCtx {
		arguments = append(arguments, reflect.ValueOf(ctx))
	}
	if len(req.args) > 0 {
		arguments = append(arguments, req.args...)
	}

	s.AddRequstStatus(req)
	// execute RPC method and return result
	reply := req.callb.method.Func.Call(arguments)
	s.RemoveRequstStatus(req)
	if len(reply) == 0 {
		return codec.CreateResponse(req.id, nil), nil
	}
	if req.callb.errPos >= 0 { // test if method returned an error
		if !reply[req.callb.errPos].IsNil() {
			e := reply[req.callb.errPos].Interface().(error)
			res := codec.CreateErrorResponse(&req.id, &callbackError{e.Error()})
			return res, nil
		}
	}
	return codec.CreateResponse(req.id, reply[0].Interface()), nil
}

// createSubscription will call the subscription callback and returns the subscription id or error.
func (s *RpcServer) createSubscription(ctx context.Context, c ServerCodec, req *serverRequest) (ID, error) {
	// subscription have as first argument the context following optional arguments
	args := []reflect.Value{req.callb.receiver, reflect.ValueOf(ctx)}
	args = append(args, req.args...)
	reply := req.callb.method.Func.Call(args)

	if !reply[1].IsNil() { // subscription creation failed
		return "", reply[1].Interface().(error)
	}

	return reply[0].Interface().(*Subscription).ID, nil
}

// RegisterService will create a service for the given type under the given namespace.
// When no methods on the given type match the criteria to be either a RPC method or
// a subscription an error is returned. Otherwise a new service is created and added
// to the service registry.
func (s *RpcServer) RegisterService(namespace string, regSvc any) error {

	typ := reflect.TypeOf(regSvc)
	if namespace == "" {
		return fmt.Errorf("no service namespace for type %s", typ.String())
	}

	// parse & build callbacks/subscriptions
	value := reflect.ValueOf(regSvc)
	calls, subs := suitableCallbacks(value, typ)

	// if the namespace already registered, add callback/subscriptions & return
	if foundSrv, nsExist := s.rpcSvcRegistry[namespace]; nsExist {
		if len(calls) == 0 && len(subs) == 0 {
			return fmt.Errorf("Service %T doesn't have any suitable methods/subscriptions to expose", regSvc)
		}
		for _, m := range calls {
			foundSrv.callbacks[formatName(m.method.Name)] = m
		}
		for _, s := range subs {
			foundSrv.subscriptions[formatName(s.method.Name)] = s
		}
		return nil
	}

	// create new service with callbacks/subscriptions & add to registry
	svc := service{
		svcNamespace:  namespace,
		svcType:       typ,
		callbacks:     calls,
		subscriptions: subs,
	}
	s.rpcSvcRegistry[svc.svcNamespace] = &svc
	return nil
}

func (s *RpcServer) RequestedProcessShutdown() chan struct{} {
	return s.requestProcessShutdown
}

func (s *RpcServer) AddRequstStatus(sReq *serverRequest) {
	s.reqStatusLock.Lock()
	defer s.reqStatusLock.Unlock()
	key := fmt.Sprintf("%s_%s", sReq.svcname, sReq.callb.method.Name)
	rs, ok := s.ReqStatus[key]
	if !ok {
		rs, _ = NewRequestStatus(sReq)
		s.ReqStatus[rs.GetName()] = rs
	} else {
		rs.AddRequst(sReq)
	}
}

func (s *RpcServer) RemoveRequstStatus(sReq *serverRequest) {
	s.reqStatusLock.Lock()
	defer s.reqStatusLock.Unlock()
	key := fmt.Sprintf("%s_%s", sReq.svcname, sReq.callb.method.Name)
	rs, ok := s.ReqStatus[key]
	if !ok {
		return
	}
	rs.RemoveRequst(sReq)
	if len(rs.Requests) == 0 {
		delete(s.ReqStatus, key)
	}
}
