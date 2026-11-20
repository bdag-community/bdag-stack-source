package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	// multiaddr "github.com/multiformats/go-multiaddr"
)

const (
	maxRetries = 0
	// initialTimeout = 1500 * time.Millisecond
	retryBackoff = 100 * time.Millisecond
)

func (g *Gateway) buildPeerURL(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func (g *Gateway) peerDagIsCurrent(host string, port int) bool {
	dagURL := g.buildPeerURL(host, port)
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"isCurrent","params":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	resp, err := g.forwardProbeCtx(ctx, dagURL, req, roleInternal)
	if err != nil {
		return false
	}
	var env struct {
		Result bool        `json:"result"`
		Error  interface{} `json:"error"`
	}
	if json.Unmarshal(resp, &env) != nil {
		return false
	}
	if env.Error != nil {
		return false
	}
	return env.Result
}

func (g *Gateway) peerDagBlockCount(host string, port int) (uint64, bool) {
	dagURL := g.buildPeerURL(host, port)
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"getBlockCount","params":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	resp, err := g.forwardProbeCtx(ctx, dagURL, req, roleInternal)
	if err != nil {
		return 0, false
	}
	var env struct {
		Result uint64      `json:"result"`
		Error  interface{} `json:"error"`
	}
	if json.Unmarshal(resp, &env) != nil {
		return 0, false
	}
	if env.Error != nil {
		return 0, false
	}
	return env.Result, true
}

// peerEvmBlockNumberWithRetry attempts to get peer's EVM block number with retries
func (g *Gateway) peerEvmBlockNumberWithRetry(host string, port int, timeout time.Duration) (uint64, bool) {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: wait before retry
			backoff := retryBackoff * time.Duration(1<<uint(attempt-1))
			time.Sleep(backoff)
		}

		evmURL := g.buildPeerURL(host, port)
		req := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		resp, err := g.forwardProbeCtx(ctx, evmURL, req, roleInternal)
		cancel()

		if err != nil {
			if attempt < maxRetries {
				continue // Retry on error
			}
			return 0, false
		}

		var env struct {
			Result string      `json:"result"`
			Error  interface{} `json:"error"`
		}
		if json.Unmarshal(resp, &env) != nil {
			if attempt < maxRetries {
				continue // Retry on parse error
			}
			return 0, false
		}
		if env.Error != nil {
			if attempt < maxRetries {
				continue // Retry on RPC error
			}
			return 0, false
		}

		result := strings.TrimSpace(env.Result)
		if result == "" {
			if attempt < maxRetries {
				continue
			}
			return 0, false
		}
		result = strings.TrimPrefix(result, "0x")
		if result == "" {
			if attempt < maxRetries {
				continue
			}
			return 0, false
		}

		head, err := strconv.ParseUint(result, 16, 64)
		if err != nil {
			if attempt < maxRetries {
				continue
			}
			return 0, false
		}
		return head, true
	}
	return 0, false
}
