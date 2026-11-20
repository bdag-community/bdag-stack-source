package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"golang.org/x/time/rate"
)

type batchElementContextKey struct{}
type requestRoleContextKey struct{}

const (
	maxGatewayBatchItems         = 1000
	certifiedEVMHeadPollInterval = 100 * time.Millisecond
)

type rpcRequestMeta struct {
	ID      stdjson.RawMessage `json:"id"`
	Method  string             `json:"method"`
	Params  stdjson.RawMessage `json:"params"`
	JSONRPC string             `json:"jsonrpc"`
}

type batchResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func newBatchResponseWriter() *batchResponseWriter {
	return &batchResponseWriter{header: make(http.Header), status: http.StatusOK}
}

func (w *batchResponseWriter) Header() http.Header { return w.header }

func (w *batchResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
}

func (w *batchResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(data)
}

func (g *Gateway) handle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	method := r.Method
	var target string
	var errOccurred bool
	// Overwritten at each rejection site. The default only covers paths that
	// return without setting a more specific reason.
	errLogMessage := "rpc request refused"
	defer func() {
		dur := time.Since(start)
		requestCount.WithLabelValues(method).Inc()
		requestDuration.WithLabelValues(method).Observe(dur.Seconds())
		sanitized := sanitizeURL(target)
		if errOccurred {
			errorCount.WithLabelValues(method).Inc()
			g.logger.Warn(errLogMessage, "method", method, "target", sanitized, "duration", dur)
		} else {
			g.logger.Debug("request handled", "method", method, "target", sanitized, "duration", dur)
		}
	}()
	_, batchElement := r.Context().Value(batchElementContextKey{}).(bool)
	role := roleUnknown
	if inherited, ok := r.Context().Value(requestRoleContextKey{}).(gatewayRole); ok {
		role = inherited
	}
	if !batchElement && r.Method == http.MethodOptions {
		g.writePreflightResponse(w, r)
		return
	}

	if !batchElement {
		var authenticated bool
		role, authenticated = g.checkClientAuth(w, r)
		if !authenticated {
			errOccurred = true
			errLogMessage = "rpc request refused: client authentication failed"
			return
		}
		isWebSocket := strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if role == roleLimited && isWebSocket {
			errOccurred = true
			errLogMessage = "rpc request refused: limited websocket forbidden"
			g.addCORSHeaders(w, r)
			http.Error(w, "limited websocket is forbidden", http.StatusForbidden)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), requestRoleContextKey{}, role))
	}
	if !batchElement && g.cfg.RequestTimeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), g.cfg.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}

	// 1. Global Rate Limit
	if !batchElement && g.globalLimiter != nil {
		if !g.globalLimiter.Allow() {
			errOccurred = true
			errLogMessage = "rpc request refused: global rate limit exceeded"
			g.addCORSHeaders(w, r)
			http.Error(w, "global rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}

	if !batchElement && g.cfg.RateLimit > 0 {
		ip := r.Header.Get("X-Forwarded-For")
		if ip != "" {
			ip = strings.TrimSpace(strings.Split(ip, ",")[0])
		} else {
			var err error
			ip, _, err = net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				errOccurred = true
				g.addCORSHeaders(w, r)
				http.Error(w, "invalid remote address", http.StatusInternalServerError)
				return
			}
		}
		if !g.getLimiter(ip).Allow() {
			errOccurred = true
			errLogMessage = "rpc request refused: per-ip rate limit exceeded"
			g.addCORSHeaders(w, r)
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
	}
	if !batchElement && g.cfg.MaxRequestBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, g.cfg.MaxRequestBytes)
	}
	if !batchElement {
		if err := r.Context().Err(); err != nil {
			errOccurred = true
			errLogMessage = "rpc request refused: request context expired before authorization"
			g.addCORSHeaders(w, r)
			http.Error(w, err.Error(), http.StatusGatewayTimeout)
			return
		}
	}

	// Limited authorization consumes and restores the complete, byte-capped
	// request body only after admission controls, but before any gateway parse,
	// dispatch, relay, or batch handling.
	if !batchElement && role == roleLimited {
		if g.cfg.AuthorizeLimitedRequest == nil {
			errOccurred = true
			errLogMessage = "rpc request refused: limited authorization unavailable"
			g.addCORSHeaders(w, r)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !g.cfg.AuthorizeLimitedRequest(w, r) {
			errOccurred = true
			errLogMessage = "rpc request refused: limited request forbidden"
			return
		}
	}

	// RC21 cannot certify individual WebSocket frames against the committed
	// native/EVM tuple. Disable this surface until framed requests and
	// subscriptions can be guarded independently.
	if !batchElement && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		releaseSlot, requestClass, ok := g.acquireRequestSlot("websocket")
		if !ok {
			errOccurred = true
			errLogMessage = "rpc request refused: too many concurrent websocket requests"
			busyRejectCount.WithLabelValues(requestClass, "websocket").Inc()
			g.addCORSHeaders(w, r)
			http.Error(w, "server busy (too many concurrent requests)", http.StatusServiceUnavailable)
			return
		}
		defer releaseSlotOnContextDone(r.Context(), releaseSlot)()

		if g.cfg.IsEVM {
			errOccurred = true
			errLogMessage = "rpc request refused: EVM websocket disabled"
			g.addCORSHeaders(w, r)
			http.Error(w, "EVM websocket is disabled in this release", http.StatusServiceUnavailable)
			return
		}
		if !g.cfg.IsEVM && g.DagHandler != nil {
			g.addCORSHeaders(w, r)
			g.DagHandler.ServeHTTP(w, r)
			return
		}
	}

	// Use pooled buffer to read request body
	buf := getBuffer()
	defer putBuffer(buf)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		errOccurred = true
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			g.addCORSHeaders(w, r)
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		} else {
			g.addCORSHeaders(w, r)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	body := buf.Bytes()
	r.Body = io.NopCloser(bytes.NewBuffer(body))
	defer r.Body.Close()
	trimmedBody := bytes.TrimSpace(body)
	isBatchRequest := len(trimmedBody) > 0 && trimmedBody[0] == '['

	var req rpcRequestMeta
	requestParsed := false

	if isBatchRequest {
		method = "batch"
		if g.cfg.IsEVM {
			if batchElement {
				g.writeRPCInvalidRequest(w, r, nil, "nested JSON-RPC batches are not supported")
				return
			}
			g.handleEVMBatch(w, r, body)
			return
		}
		g.writeRPCInvalidRequest(w, r, nil, "DAG JSON-RPC batches are disabled")
		return
	} else {
		if err := stdjson.Unmarshal(body, &req); err == nil {
			requestParsed = true
			method = req.Method
		} else {
			g.logger.Warn("Failed to unmarshal request", "err", err)
			if peeked, peekErr := PeekMethod(body); peekErr == nil {
				method = peeked
			}
		}
	}

	if err := r.Context().Err(); err != nil {
		errOccurred = true
		g.addCORSHeaders(w, r)
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}

	// 2. Concurrency Limit
	releaseSlot, requestClass, ok := g.acquireRequestSlot(method)
	if !ok {
		errOccurred = true
		errLogMessage = "rpc request refused: node too busy (concurrency limit)"
		busyRejectCount.WithLabelValues(requestClass, method).Inc()
		g.addCORSHeaders(w, r)
		http.Error(w, "server busy (too many concurrent requests)", http.StatusServiceUnavailable)
		return
	}
	defer releaseSlotOnContextDone(r.Context(), releaseSlot)()

	validRPCRequest := requestParsed && rpcVersionSupported(req.JSONRPC)
	if g.cfg.IsEVM && validRPCRequest && isPublicGatewayRejectedMethod(method) {
		if len(req.ID) == 0 {
			g.writeRPCNotificationAck(w, r)
			return
		}
		errOccurred = true
		errLogMessage = "rpc request refused: public engine API is disabled"
		g.writeRPCMethodNotFound(w, r, req.ID)
		return
	}

	canIntercept := validRPCRequest && rpcHasNoParams(req.Params)
	if !isBatchRequest && canIntercept && method == "eth_blockNumber" && g.cfg.IsEVM && g.SyncChecker != nil {
		current, headVisible := g.SyncChecker.EVMHead()
		_, peerMax, nativeTarget := g.evmSyncTarget()
		visible := current
		if nativeTarget > 0 && visible > nativeTarget {
			visible = nativeTarget
		}
		// Block acceptance may advance geth through a large ordered batch before
		// publishing the next native commitment. The previous atomically
		// published native height remains canonical during that window. It is
		// safe to report that number when geth has not fallen behind it; no
		// mutable EVM state is read or exposed by this metadata response.
		if !headVisible && nativeTarget > 0 && current >= nativeTarget {
			visible = nativeTarget
			headVisible = true
			g.logger.Debug("returning committed native EVM height while current head is mutating", "evmCurrent", current, "nativeTargetEVM", nativeTarget, "peerMaxEVM", peerMax)
		}
		if headVisible {
			if len(req.ID) == 0 {
				g.writeRPCNotificationAck(w, r)
				return
			}
			g.addCORSHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			res := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  hexutil.EncodeUint64(visible),
			}
			stdjson.NewEncoder(w).Encode(res)
			return
		}
		if peerMax > 0 || nativeTarget > 0 {
			if len(req.ID) == 0 {
				g.writeRPCNotificationAck(w, r)
				return
			}
			errOccurred = true
			errLogMessage = "rpc request refused: EVM head unavailable"
			g.writeRPCError(w, r, body, errors.New("EVM head unavailable"))
			return
		}
	}

	// EVM sync health must remain responsive while native block acceptance holds
	// the chain write lock. A known native-bounded target is sufficient to answer
	// without entering the lock-taking native IsSyncing path.
	if !isBatchRequest && canIntercept && method == "eth_syncing" && g.cfg.IsEVM && g.SyncChecker != nil {
		highest, peerMax, nativeTarget := g.evmSyncTarget()
		current := g.SyncChecker.EvmHeight()
		if current < highest {
			if len(req.ID) == 0 {
				g.writeRPCNotificationAck(w, r)
				return
			}
			g.logger.Debug("intercepting eth_syncing because local EVM is behind native-bounded target", "evmCurrent", current, "evmTarget", highest, "peerMaxEVM", peerMax, "nativeTargetEVM", nativeTarget)
			g.addCORSHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			res := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"startingBlock": hexutil.EncodeUint64(0),
					"currentBlock":  hexutil.EncodeUint64(current),
					"highestBlock":  hexutil.EncodeUint64(highest),
				},
			}
			stdjson.NewEncoder(w).Encode(res)
			return
		}
		if nativeTarget > 0 {
			if len(req.ID) == 0 {
				g.writeRPCNotificationAck(w, r)
				return
			}
			// A current geth height at or above the atomically committed native
			// target is not syncing. The stricter generation/hash/root guard is
			// still required for every state-dependent request below.
			g.logger.Debug("returning eth_syncing=false because local EVM is at native-bounded target", "evmCurrent", current, "evmTarget", highest, "peerMaxEVM", peerMax, "nativeTargetEVM", nativeTarget)
			g.addCORSHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			res := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  false,
			}
			stdjson.NewEncoder(w).Encode(res)
			return
		}
	}

	// RC21 serves state-dependent EVM requests only from the locally committed
	// head. A deep peer compatibility checkpoint is not a current RPC-head proof.
	forwarded := strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-By")), "bdag-gateway")
	certifiedEVMMethod := g.cfg.IsEVM && requiresCertifiedEVMHead(method)
	requiresCertifiedHead := certifiedEVMMethod && validRPCRequest
	isNodeSpecific := (requestParsed && method == "") || isLocalOnlyMethod(method) || isLocalMiningRPCMethod(method)
	mustUseLocal := isNodeSpecific || certifiedEVMMethod
	localHeadVisible := true
	if requiresCertifiedHead {
		_, localHeadVisible = g.waitForCertifiedEVMHead(r.Context())
	}
	var useLocal bool

	var syncing bool
	if !mustUseLocal && !forwarded {
		if g.SyncChecker == nil {
			g.logger.Warn("SyncChecker not initialized, assuming synced")
		} else {
			syncing = g.SyncChecker.IsSyncing()
		}
	}

	g.logger.Trace("Routing decision", "method", method, "syncing", syncing, "requiresCertifiedEVMHead", requiresCertifiedHead, "localEVMHeadVisible", localHeadVisible, "forwarded", forwarded)

	rejectUnavailableEVMHead := func(message string) {
		errOccurred = true
		errLogMessage = "rpc request refused: " + message
		if len(req.ID) == 0 {
			g.writeRPCNotificationAck(w, r)
			return
		}
		g.writeRPCError(w, r, body, errors.New(message))
	}

	// Check for state-dependent or node-specific RPC methods that must never be relayed.
	if mustUseLocal {
		if requiresCertifiedHead && !localHeadVisible {
			message := "EVM head unavailable"
			if forwarded {
				message = "EVM head unavailable on forwarded request"
			}
			rejectUnavailableEVMHead(message)
			return
		}
		g.logger.Trace("Method requires local handling", "method", method, "requiresCertifiedEVMHead", requiresCertifiedHead)

		if requestParsed && method == "net_peerCount" && g.cfg.IsEVM {
			if len(req.ID) == 0 {
				g.writeRPCNotificationAck(w, r)
				return
			}
			count := 0
			if g.peerManager != nil {
				count = g.peerManager.GetPeerCount()
			}
			g.addCORSHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			res := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  hexutil.EncodeUint64(uint64(count)),
			}
			stdjson.NewEncoder(w).Encode(res)
			return
		}

		useLocal = true
	} else if forwarded {
		// The forwarding marker is an anti-loop hint, not authentication. It may
		// suppress another relay, but never bypasses the certified-head gate above.
		g.logger.Trace("Request already forwarded, using local handler", "method", method)
		useLocal = true
	} else {
		// No specific peer for this TX, check standard routing
		var highest uint64
		if g.peerManager != nil {
			highest = g.peerManager.GetMaxHeight()
		}
		var local uint64
		if g.SyncChecker != nil {
			if g.cfg.IsEVM {
				highest, _, _ = g.evmSyncTarget()
				local = g.SyncChecker.EvmHeight()
			} else {
				local = g.SyncChecker.DagHeight()
			}
		}
		peerCount := 0
		if g.peerManager != nil {
			peerCount = g.peerManager.GetPeerCount()
		}
		g.logger.Trace("Routing heights", "local", local, "maxPeer", highest, "peerCount", peerCount, "isEVM", g.cfg.IsEVM)

		if (syncing || local < highest) && highest > local {
			if !syncing {
				g.logger.Debug("Local node is behind, using best peer", "localHeight", local, "maxHeight", highest)
				syncing = true
			}
			clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				clientIP = r.RemoteAddr
			}
			affinityKey := clientIP
			affinityTTL := time.Duration(0)
			deterministic := false
			if g.cfg.IsEVM {
				switch method {
				case "eth_sendRawTransaction":
					if from, err := ExtractSenderFromSendRawTx(body); err == nil {
						affinityKey = "from:" + strings.ToLower(from)
						affinityTTL = 15 * time.Second
						deterministic = true
						g.logger.Debug("using sender affinity", "from", from, "key", affinityKey)
					} else {
						g.logger.Trace("sender affinity unavailable, falling back to client IP", "err", err)
					}
				case "eth_getTransactionCount":
					if from, err := ExtractAddressFromGetTransactionCount(body); err == nil {
						affinityKey = "from:" + strings.ToLower(from)
						affinityTTL = 15 * time.Second
						deterministic = true
						g.logger.Debug("using sender affinity for nonce", "from", from, "key", affinityKey)
					} else {
						g.logger.Trace("sender affinity unavailable for nonce", "err", err)
					}
				case "eth_getTransactionReceipt", "eth_getTransactionByHash":
					if txHash, err := ExtractTxHashFromTxQuery(body); err == nil {
						affinityKey = "tx:" + strings.ToLower(txHash)
						affinityTTL = 5 * time.Second
						deterministic = true
						g.logger.Debug("using tx affinity", "tx", txHash, "key", affinityKey)
					} else {
						g.logger.Trace("tx affinity unavailable", "err", err)
					}
				}
			}

			var peer *Peer
			var ok bool
			if g.peerManager != nil {
				if deterministic {
					peer, ok = g.peerManager.GetBestPeerInfoWithTTL(affinityKey, affinityTTL, true)
				} else {
					peer, ok = g.peerManager.GetBestPeerInfo(affinityKey)
				}
			}
			if ok {
				port := peer.DAGPort
				if g.cfg.IsEVM {
					port = peer.EVMPort
				}
				target = g.buildPeerURL(peer.Host, port)
				g.logger.Debug("using best peer", "target", target, "host", peer.Host, "height", peer.BlockHeight, "latency", peer.Latency, "synced", peer.IsSynced, "active", peer.IsActive)
			} else {
				g.logger.Warn("syncing but no relay peer available", "localHeight", local, "maxHeight", highest, "peerCount", peerCount)
				errOccurred = true
				g.addCORSHeaders(w, r)
				http.Error(w, "syncing but no active peers", http.StatusServiceUnavailable)
				return
			}
		} else {
			// Node is synced and not behind any peer - use local handler
			useLocal = true
			g.logger.Trace("using local handler", "syncing", syncing, "localHeight", local, "maxHeight", highest)
		}
	}

	if useLocal {
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		if g.cfg.IsEVM && g.EvmHandler != nil {
			g.addCORSHeaders(w, r)
			if requiresCertifiedHead {
				if !canDiscardCertifiedEVMResponse(method) {
					if !g.withCommittedEVMHeadRetry(r.Context(), func() {}) {
						rejectUnavailableEVMHead("EVM head unavailable at dispatch")
						return
					}
					g.EvmHandler.ServeHTTP(w, r)
					return
				}
				var captured *batchResponseWriter
				if !g.withCommittedEVMHeadRetry(r.Context(), func() {
					captured = newBatchResponseWriter()
					attempt := r.Clone(r.Context())
					attempt.Body = io.NopCloser(bytes.NewReader(body))
					attempt.ContentLength = int64(len(body))
					g.EvmHandler.ServeHTTP(captured, attempt)
				}) {
					rejectUnavailableEVMHead("EVM head unavailable at dispatch")
					return
				}
				writeCapturedResponse(w, captured)
				return
			}
			g.EvmHandler.ServeHTTP(w, r)
			return
		}
		if !g.cfg.IsEVM && g.DagHandler != nil {
			g.addCORSHeaders(w, r)
			g.DagHandler.ServeHTTP(w, r)
			return
		}
		// Failure if we need to use local but no handlers are available
		errOccurred = true
		g.addCORSHeaders(w, r)
		http.Error(w, "no local handler available", http.StatusInternalServerError)
		return
	}

	// 4. Intercept mining-related methods when we are relaying to peers
	if isMiningRelayMethod(method) {
		if method == "getBlockTemplate" {
			if target == "" {
				errOccurred = true
				g.addCORSHeaders(w, r)
				http.Error(w, "best peer not found for mining request", http.StatusServiceUnavailable)
				return
			}
			if err := g.proxyGetBlockTemplate(w, r, target, body, role); err != nil {
				errOccurred = true
				if isLimitedForwardRejection(err) {
					g.addCORSHeaders(w, r)
					http.Error(w, "forbidden", http.StatusForbidden)
				} else {
					g.writeRPCError(w, r, body, err)
				}
			}
			return
		}

		// Always broadcast submissions when in relaying mode
		if err := g.broadcastSubmitBlock(w, r, body, role); err != nil {
			errOccurred = true
			if isLimitedForwardRejection(err) {
				g.addCORSHeaders(w, r)
				http.Error(w, "forbidden", http.StatusForbidden)
			} else {
				g.writeRPCError(w, r, body, err)
			}
		}
		return
	}

	if target == "" {
		errOccurred = true
		g.addCORSHeaders(w, r)
		http.Error(w, "best peer not found for request", http.StatusServiceUnavailable)
		return
	}

	resp, err := g.forwardCtx(r.Context(), target, body, role)
	if err != nil {
		errOccurred = true
		// If the error happened after headers were written, this might look potentially ugly,
		// but it's better than silent failure.
		if isLimitedForwardRejection(err) {
			g.addCORSHeaders(w, r)
			http.Error(w, "forbidden", http.StatusForbidden)
		} else {
			g.writeRPCError(w, r, body, err)
		}
		return
	}

	g.addCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (g *Gateway) waitForCertifiedEVMHead(ctx context.Context) (uint64, bool) {
	if g == nil || g.SyncChecker == nil {
		return 0, false
	}
	height, visible := g.SyncChecker.EVMHead()
	if visible || g.cfg.CertifiedEVMHeadWait <= 0 {
		return height, visible
	}

	timer := time.NewTimer(g.cfg.CertifiedEVMHeadWait)
	defer timer.Stop()
	ticker := time.NewTicker(certifiedEVMHeadPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return height, false
		case <-timer.C:
			return height, false
		case <-ticker.C:
			height, visible = g.SyncChecker.EVMHead()
			if visible {
				return height, true
			}
		}
	}
}

func (g *Gateway) withCommittedEVMHeadRetry(ctx context.Context, read func()) bool {
	if g == nil || g.SyncChecker == nil || read == nil {
		return false
	}
	if g.SyncChecker.WithCommittedEVMHead(read) {
		return true
	}
	if g.cfg.CertifiedEVMHeadWait <= 0 {
		return false
	}

	timer := time.NewTimer(g.cfg.CertifiedEVMHeadWait)
	defer timer.Stop()
	ticker := time.NewTicker(certifiedEVMHeadPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
			if g.SyncChecker.WithCommittedEVMHead(read) {
				return true
			}
		}
	}
}

func writeCapturedResponse(w http.ResponseWriter, captured *batchResponseWriter) {
	if captured == nil {
		return
	}
	for key, values := range captured.header {
		w.Header().Del(key)
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(captured.status)
	_, _ = w.Write(captured.body.Bytes())
}

func rpcHasNoParams(params stdjson.RawMessage) bool {
	trimmed := bytes.TrimSpace(params)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null"))
}

func rpcVersionSupported(version string) bool {
	return version == "2.0"
}

func (g *Gateway) handleEVMBatch(w http.ResponseWriter, r *http.Request, body []byte) {
	var elements []stdjson.RawMessage
	if err := stdjson.Unmarshal(body, &elements); err != nil {
		g.writeRPCParseError(w, r, "parse error")
		return
	}
	if len(elements) == 0 {
		g.writeRPCInvalidRequest(w, r, nil, "empty JSON-RPC batch")
		return
	}
	if len(elements) > maxGatewayBatchItems {
		g.writeRPCInvalidRequest(w, r, nil, "JSON-RPC batch exceeds item limit")
		return
	}

	responses := make([]stdjson.RawMessage, 0, len(elements))
	for _, element := range elements {
		trimmed := bytes.TrimSpace(element)
		var meta rpcRequestMeta
		parsed := len(trimmed) > 0 && trimmed[0] == '{' && stdjson.Unmarshal(trimmed, &meta) == nil
		if !parsed {
			responses = append(responses, marshalRPCError(nil, -32600, "invalid request"))
			continue
		}

		isNotification := meta.Method != "" && rpcVersionSupported(meta.JSONRPC) && len(meta.ID) == 0
		recorder := newBatchResponseWriter()
		ctx := context.WithValue(r.Context(), batchElementContextKey{}, true)
		subrequest := r.Clone(ctx)
		subrequest.Body = io.NopCloser(bytes.NewReader(trimmed))
		subrequest.ContentLength = int64(len(trimmed))
		g.handle(recorder, subrequest)

		if isNotification {
			continue
		}
		response := bytes.TrimSpace(recorder.body.Bytes())
		if len(response) > 0 && response[0] == '{' && stdjson.Valid(response) {
			responses = append(responses, append(stdjson.RawMessage(nil), response...))
			continue
		}

		message := strings.TrimSpace(string(response))
		if message == "" {
			message = http.StatusText(recorder.status)
		}
		if message == "" {
			message = "request failed"
		}
		responses = append(responses, marshalRPCError(meta.ID, -32000, message))
	}

	g.addCORSHeaders(w, r)
	if len(responses) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := stdjson.NewEncoder(w).Encode(responses); err != nil {
		g.logger.Warn("failed to encode JSON-RPC batch response", "err", err)
	}
}

func marshalRPCError(id stdjson.RawMessage, code int, message string) stdjson.RawMessage {
	if len(id) == 0 || !stdjson.Valid(id) {
		id = stdjson.RawMessage("null")
	}
	response := struct {
		JSONRPC string             `json:"jsonrpc"`
		ID      stdjson.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id}
	response.Error.Code = code
	response.Error.Message = message
	encoded, _ := stdjson.Marshal(response)
	return encoded
}

func (g *Gateway) writeRPCInvalidRequest(w http.ResponseWriter, r *http.Request, id stdjson.RawMessage, message string) {
	g.addCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(marshalRPCError(id, -32600, message))
}

func (g *Gateway) writeRPCParseError(w http.ResponseWriter, r *http.Request, message string) {
	g.addCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(marshalRPCError(nil, -32700, message))
}

func (g *Gateway) writeRPCMethodNotFound(w http.ResponseWriter, r *http.Request, id stdjson.RawMessage) {
	g.addCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(marshalRPCError(id, -32601, "method not found"))
}

func (g *Gateway) checkClientAuth(w http.ResponseWriter, r *http.Request) (gatewayRole, bool) {
	if !g.cfg.InboundAuth {
		return rolePrimary, true
	}
	authHeaders := r.Header.Values("Authorization")
	if len(authHeaders) != 1 {
		g.writeUnauthorized(w, r)
		return roleUnknown, false
	}
	authDigest := sha256.Sum256([]byte(authHeaders[0]))
	primaryMatch := subtle.ConstantTimeCompare(authDigest[:], g.primaryAuthDigest[:])
	limitedMatch := subtle.ConstantTimeCompare(authDigest[:], g.limitedAuthDigest[:])
	if primaryMatch == 1 {
		return rolePrimary, true
	}
	if limitedMatch == 1 && credentialsConfigured(g.cfg.InboundLimitUser, g.cfg.InboundLimitPass) {
		return roleLimited, true
	}
	g.writeUnauthorized(w, r)
	return roleUnknown, false
}

func (g *Gateway) writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	g.addCORSHeaders(w, r)
	w.Header().Set("WWW-Authenticate", `Basic realm="gateway"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	io.WriteString(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"unauthorized"}}`)
}

func (g *Gateway) writeRPCNotificationAck(w http.ResponseWriter, r *http.Request) {
	g.addCORSHeaders(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) proxyGetBlockTemplate(w http.ResponseWriter, r *http.Request, target string, body []byte, role gatewayRole) error {
	// 1. Inject PayAddress if syncing
	var newBody = body
	syncing := g.SyncChecker != nil && g.SyncChecker.IsSyncing()

	if syncing && len(g.cfg.MiningAddrs) > 0 {
		var req struct {
			ID     stdjson.RawMessage   `json:"id"`
			Method string               `json:"method"`
			Params []stdjson.RawMessage `json:"params"`
		}
		if err := stdjson.Unmarshal(body, &req); err == nil && len(req.Params) == 2 {
			localAddr := g.cfg.MiningAddrs[0]
			quotedAddr := `"` + localAddr + `"`
			req.Params = append(req.Params, stdjson.RawMessage(quotedAddr))
			if b, err := stdjson.Marshal(req); err == nil {
				newBody = b
				g.logger.Debug("Injected PayAddress into GetBlockTemplate request", "addr", localAddr)
			}
		}
	}

	// 2. Fetch from peer with potentially modified body
	respBody, err := g.forwardCtx(r.Context(), target, newBody, role)
	if err != nil {
		return err
	}

	var rpcResp struct {
		Result *json.GetBlockTemplateResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := stdjson.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("failed to parse peer template: %v", err)
	}

	if rpcResp.Error != nil || rpcResp.Result == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
		return nil
	}

	// 3. Cache for reconstruction (using the valid response from peer)
	nbits, _ := strconv.ParseUint(rpcResp.Result.PowDiffReference.NBits, 16, 32)

	// Reconstruct transactions/parents objects for cache
	// Note: We need deserialized objects for the cache entry used in reconstruction
	txs := make([]*etypes.Transaction, len(rpcResp.Result.Transactions))
	for i, rtx := range rpcResp.Result.Transactions {
		raw, err := hex.DecodeString(rtx.Data)
		if err == nil {
			tx := new(etypes.Transaction)
			if err := tx.UnmarshalBinary(raw); err == nil {
				txs[i] = tx
			}
		}
	}
	parents := make([]*common.Hash, len(rpcResp.Result.Parents))
	for i, p := range rpcResp.Result.Parents {
		h := common.HexToHash(p.Hash)
		parents[i] = &h
	}

	entry := &TemplateCacheEntry{
		Transactions: txs,
		Header: types.BlockHeader{
			Version:         rpcResp.Result.Version,
			ParentRoot:      common.HexToHash(rpcResp.Result.PreviousHash),
			TxRoot:          common.HexToHash(rpcResp.Result.TxRoot),
			Timestamp:       time.Unix(rpcResp.Result.CurTime, 0),
			Difficulty:      uint32(nbits),
			BlockHeight:     uint64(rpcResp.Result.Height),
			CoinbaseAddress: rpcResp.Result.CoinbaseAddress,
			Reward:          rpcResp.Result.Reward,
			StateRoot:       common.HexToHash(rpcResp.Result.StateRoot),
		},
		Parents: parents,
		Height:  int64(rpcResp.Result.Height),
		Reward:  rpcResp.Result.Reward,
	}
	g.tplCache.Set(rpcResp.Result.TxRoot, entry)

	// 4. Return peer response (which is already correct)
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)
	return nil
}

func sanitizeURL(target string) string {
	if target == "" {
		return ""
	}
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	return u.Host
}

func (g *Gateway) getLimiter(ip string) *rate.Limiter {
	g.limitMu.Lock()
	defer g.limitMu.Unlock()
	lim, ok := g.limiterS.Get(ip)
	if ok != nil {
		lim = rate.NewLimiter(rate.Limit(g.cfg.RateLimit), g.cfg.RateBurst)
		g.limiterS.Set(ip, lim)
	}
	return lim
	// lim, ok := g.limiters[ip]
	// if !ok {
	// 	lim = rate.NewLimiter(rate.Limit(g.cfg.RateLimit), g.cfg.RateBurst)
	// 	g.limiters[ip] = lim
	// }
	// return lim
}

func (g *Gateway) writePreflightResponse(w http.ResponseWriter, r *http.Request) {
	g.addCORSHeaders(w, r)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) addCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	allowedOrigin := ""
	if len(g.cfg.AllowedOrigins) == 0 {
		if origin != "" {
			allowedOrigin = origin
		} else {
			allowedOrigin = "*"
		}
	} else {
		for _, o := range g.cfg.AllowedOrigins {
			if o == "*" {
				allowedOrigin = "*"
				break
			}
			if origin != "" && origin == o {
				allowedOrigin = origin
				break
			}
		}
	}
	if allowedOrigin == "*" && g.cfg.AllowCredentials && origin != "" {
		allowedOrigin = origin
	}
	if allowedOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		if allowedOrigin != "*" {
			w.Header().Add("Vary", "Origin")
		}
	}
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	w.Header().Add("Vary", "Access-Control-Request-Method")

	methods := g.cfg.AllowedMethods
	if len(methods) == 0 {
		methods = []string{http.MethodPost, http.MethodOptions}
	}
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))

	if acrh := r.Header.Get("Access-Control-Request-Headers"); acrh != "" {
		w.Header().Set("Access-Control-Allow-Headers", acrh)
	} else if len(g.cfg.AllowedHeaders) > 0 {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(g.cfg.AllowedHeaders, ", "))
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
	}

	if g.cfg.AllowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	if g.cfg.CORSMaxAge > 0 {
		seconds := int64(g.cfg.CORSMaxAge / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(seconds, 10))
	}
}

func (g *Gateway) broadcastSubmitBlock(w http.ResponseWriter, r *http.Request, body []byte, role gatewayRole) error {
	var rpcReq struct {
		ID     stdjson.RawMessage   `json:"id"`
		Method string               `json:"method"`
		Params []stdjson.RawMessage `json:"params"`
	}
	if err := stdjson.Unmarshal(body, &rpcReq); err != nil {
		return err
	}

	// 1. Try to reconstruct full block if it's submitBlockHeader
	var finalMethod = rpcReq.Method
	var finalParams = rpcReq.Params

	if rpcReq.Method == "submitBlockHeader" && len(rpcReq.Params) > 0 {
		var hexHeader string
		if err := stdjson.Unmarshal(rpcReq.Params[0], &hexHeader); err == nil {
			block, err := g.reconstructBlockFromHeader(hexHeader)
			if err == nil {
				// Convert to submitBlock for broader acceptance and bypassing "overdue" checks on peers
				finalMethod = "submitBlock"
				var buf bytes.Buffer
				if err := block.Serialize(&buf); err == nil {
					finalParams = []stdjson.RawMessage{
						stdjson.RawMessage(`"` + hex.EncodeToString(buf.Bytes()) + `"`),
					}
					g.logger.Info("Reconstructed full block from header for broad submission", "hash", block.BlockHash().String(), "txRoot", block.Header.TxRoot.String())
				} else {
					g.logger.Warn("Failed to serialize reconstructed block", "err", err)
				}
			} else {
				g.logger.Warn("Failed to reconstruct block from header, relaying as-is", "err", err)
			}
		}
	}

	// 2. Prepare new body for broadcast
	newReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      rpcReq.ID,
		"method":  finalMethod,
		"params":  finalParams,
	}
	newBody, _ := stdjson.Marshal(newReq)

	// 3. Broadcast to all synced peers
	hosts := g.peerManager.GetSyncedPeers()
	if len(hosts) == 0 {
		return fmt.Errorf("no synced peers available for submission")
	}

	g.logger.Debug("Broadcasting solved block to synced peers", "count", len(hosts), "method", finalMethod)

	type attempt struct {
		resp []byte
		err  error
	}
	results := make(chan attempt, len(hosts))

	for _, p := range hosts {
		go func(peer Peer) {
			port := peer.DAGPort
			if g.cfg.IsEVM {
				port = peer.EVMPort
			}
			url := g.buildPeerURL(peer.Host, port)
			res, err := g.forwardCtx(r.Context(), url, newBody, role)
			results <- attempt{res, err}
		}(p)
	}

	var lastRes []byte
	var lastErr error
	success := false

	for i := 0; i < len(hosts); i++ {
		res := <-results
		if res.err == nil {
			success = true
			lastRes = res.resp
		} else {
			lastErr = res.err
		}
	}

	if success {
		w.Header().Set("Content-Type", "application/json")
		g.addCORSHeaders(w, r)
		w.Write(lastRes)
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all broadcast attempts failed")
}

func (g *Gateway) reconstructBlockFromHeader(hexHeader string) (*types.Block, error) {
	if len(hexHeader)%2 != 0 {
		hexHeader = "0" + hexHeader
	}
	raw, err := hex.DecodeString(hexHeader)
	if err != nil {
		return nil, err
	}
	// TxRoot is at offset 36 in the serialized header (Version 4, ParentRoot 32)
	if len(raw) < 68 {
		return nil, fmt.Errorf("header too short: %d", len(raw))
	}
	txRoot := common.BytesToHash(raw[36:68])
	txRootHex := txRoot.String()

	entry, err := g.tplCache.Get(txRootHex)
	if err != nil {
		return nil, fmt.Errorf("template not found in cache for TxRoot %s: %v", txRootHex, err)
	}

	// Deserialize the header to catch miner modifications (timestamp, nonce, etc.)
	var header types.BlockHeader
	// We use the same strategy as api.go to ensure POW field can be deserialized
	buffer := make([]byte, len(raw)+169)
	copy(buffer, raw)
	if err := header.Deserialize(bytes.NewReader(buffer)); err != nil {
		// Fallback to cached header if full deserialization fails
		g.logger.Warn("Failed to fully deserialize header from miner, using cached header", "err", err)
		header = entry.Header
	}

	block := &types.Block{
		Header:       header,
		Transactions: entry.Transactions,
		Parents:      entry.Parents,
	}
	return block, nil
}
