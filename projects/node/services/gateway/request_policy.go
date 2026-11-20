package gateway

import (
	"context"
	"strings"
	"sync"
)

const (
	requestClassNormal   = "normal"
	requestClassCritical = "critical"
)

type gatewayRequestPolicy struct {
	localOnly   bool
	localMining bool
	critical    bool
}

func classifyGatewayRequest(method string) gatewayRequestPolicy {
	return gatewayRequestPolicy{
		localOnly:   isGatewayLocalOnlyMethod(method),
		localMining: isGatewayLocalMiningRPCMethod(method),
		critical:    isGatewayCriticalLocalMethod(method),
	}
}

func isLocalOnlyMethod(method string) bool {
	return classifyGatewayRequest(method).localOnly
}

func isCriticalLocalMethod(method string) bool {
	return classifyGatewayRequest(method).critical
}

func isLocalMiningRPCMethod(method string) bool {
	return classifyGatewayRequest(method).localMining
}

// requiresCertifiedEVMHead identifies RPC methods whose result or side effects
// depend on EVM history. RC21 serves them only from the locally committed head.
func requiresCertifiedEVMHead(method string) bool {
	switch method {
	case "":
		return false
	case "eth_chainId",
		"eth_accounts",
		"eth_sign",
		"eth_newPendingTransactionFilter",
		"eth_uninstallFilter",
		"eth_unsubscribe":
		return false
	case "personal_sendTransaction", "personal_signTransaction":
		return true
	}
	if isGatewayLocalMiningRPCMethod(method) {
		return false
	}
	switch {
	case strings.HasPrefix(method, "eth_"),
		strings.HasPrefix(method, "debug_"),
		strings.HasPrefix(method, "trace_"):
		return true
	case strings.HasPrefix(method, "personal_"),
		strings.HasPrefix(method, "admin_"),
		strings.HasPrefix(method, "txpool_"),
		strings.HasPrefix(method, "miner_"),
		strings.HasPrefix(method, "net_"),
		strings.HasPrefix(method, "rpc_"),
		strings.HasPrefix(method, "web3_"),
		strings.HasPrefix(method, "bdag_"),
		strings.HasPrefix(method, "engine_"):
		return false
	default:
		// Unknown methods on the public EVM listener are treated as state
		// dependent until they have been explicitly audited.
		return true
	}
}

// canDiscardCertifiedEVMResponse is true only for audited read-only RPC
// classes. Side-effecting and unknown methods are preflighted at a committed
// head, then executed exactly once so a generation change cannot consume their
// effect while discarding the response.
func canDiscardCertifiedEVMResponse(method string) bool {
	if !requiresCertifiedEVMHead(method) {
		return false
	}
	switch method {
	case "eth_blobBaseFee",
		"eth_blockNumber",
		"eth_call",
		"eth_createAccessList",
		"eth_estimateGas",
		"eth_feeHistory",
		"eth_fillTransaction",
		"eth_gasPrice",
		"eth_getBalance",
		"eth_getBlockByHash",
		"eth_getBlockByNumber",
		"eth_getBlockReceipts",
		"eth_getBlockTransactionCountByHash",
		"eth_getBlockTransactionCountByNumber",
		"eth_getCode",
		"eth_getFilterLogs",
		"eth_getHeaderByHash",
		"eth_getHeaderByNumber",
		"eth_getLogs",
		"eth_getProof",
		"eth_getRawTransactionByBlockHashAndIndex",
		"eth_getRawTransactionByBlockNumberAndIndex",
		"eth_getRawTransactionByHash",
		"eth_getStorageAt",
		"eth_getTransactionByBlockHashAndIndex",
		"eth_getTransactionByBlockNumberAndIndex",
		"eth_getTransactionByHash",
		"eth_getTransactionCount",
		"eth_getTransactionReceipt",
		"eth_getUncleByBlockHashAndIndex",
		"eth_getUncleByBlockNumberAndIndex",
		"eth_getUncleCountByBlockHash",
		"eth_getUncleCountByBlockNumber",
		"eth_maxPriorityFeePerGas",
		"eth_pendingTransactions",
		"eth_simulateV1",
		"eth_syncing":
		return true
	default:
		return strings.HasPrefix(method, "trace_")
	}
}

func isPublicGatewayRejectedMethod(method string) bool {
	return strings.HasPrefix(method, "engine_")
}

func isGatewayLocalOnlyMethod(method string) bool {
	switch {
	case strings.HasPrefix(method, "debug_"),
		strings.HasPrefix(method, "personal_"),
		strings.HasPrefix(method, "admin_"),
		strings.HasPrefix(method, "engine_"),
		strings.HasPrefix(method, "txpool_"),
		strings.HasPrefix(method, "miner_"),
		strings.HasPrefix(method, "net_"),
		strings.HasPrefix(method, "rpc_"),
		strings.HasPrefix(method, "trace_"),
		strings.HasPrefix(method, "web3_"),
		strings.HasPrefix(method, "bdag_"),
		strings.HasPrefix(method, "p2p_"):
		return true
	}
	switch method {
	case "isCurrent",
		"Blockdag_isCurrent",
		"eth_syncing",
		"getPeerInfo",
		"Blockdag_getPeerInfo",
		"getNetworkInfo",
		"Blockdag_getNetworkInfo",
		"getNodeInfo",
		"Blockdag_getNodeInfo",
		"getBlockCount",
		"Blockdag_getBlockCount",
		"getMainChainHeight",
		"Blockdag_getMainChainHeight",
		"eth_chainId",
		"eth_accounts",
		"eth_sign",
		"eth_sendTransaction",
		"eth_signTransaction",
		"eth_pendingTransactions",
		"eth_resend",
		"eth_newFilter",
		"eth_newBlockFilter",
		"eth_newPendingTransactionFilter",
		"eth_getFilterChanges",
		"eth_getFilterLogs",
		"eth_uninstallFilter",
		"eth_subscribe",
		"eth_unsubscribe",
		"eth_newHeads",
		"eth_logs",
		"eth_newPendingTransactions":
		return true
	default:
		return false
	}
}

func isGatewayCriticalLocalMethod(method string) bool {
	switch method {
	case "getTemplateHealth",
		"Blockdag_getTemplateHealth",
		"Blockdag_GetTemplateHealth",
		"getBlockCount",
		"Blockdag_getBlockCount",
		"isCurrent",
		"Blockdag_isCurrent",
		"getMainChainHeight",
		"Blockdag_getMainChainHeight",
		"submitBlockHeader",
		"Blockdag_submitBlockHeader",
		"Blockdag_SubmitBlockHeader",
		"submitBlockHeaderV2",
		"Blockdag_submitBlockHeaderV2",
		"Blockdag_SubmitBlockHeaderV2":
		return true
	default:
		return false
	}
}

func isGatewayLocalMiningRPCMethod(method string) bool {
	switch method {
	case "getBlockTemplate",
		"Blockdag_getBlockTemplate",
		"Blockdag_GetBlockTemplate",
		"getTemplateHealth",
		"Blockdag_getTemplateHealth",
		"Blockdag_GetTemplateHealth",
		"submitBlockHeader",
		"Blockdag_submitBlockHeader",
		"Blockdag_SubmitBlockHeader",
		"submitBlockHeaderV2",
		"Blockdag_submitBlockHeaderV2",
		"Blockdag_SubmitBlockHeaderV2",
		"submitBlock",
		"Blockdag_SubmitBlock":
		return true
	default:
		return false
	}
}

func isMiningRelayMethod(method string) bool {
	switch method {
	case "getBlockTemplate", "submitBlock", "submitBlockHeader":
		return true
	default:
		return false
	}
}

func (g *Gateway) acquireRequestSlot(method string) (func(), string, bool) {
	if isCriticalLocalMethod(method) {
		return acquireSlot(g.criticalSem, requestClassCritical)
	}
	return acquireSlot(g.sem, requestClassNormal)
}

func acquireSlot(sem chan struct{}, requestClass string) (func(), string, bool) {
	if sem == nil {
		return func() {}, requestClass, true
	}
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, requestClass, true
	default:
		return nil, requestClass, false
	}
}

func releaseSlotOnContextDone(ctx context.Context, release func()) func() {
	var once sync.Once
	releaseOnce := func() {
		once.Do(release)
	}
	if ctx != nil {
		go func() {
			<-ctx.Done()
			releaseOnce()
		}()
	}
	return releaseOnce
}
