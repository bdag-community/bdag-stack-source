package miner

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/rpc/api"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
)

const (
	SubmitInterval = time.Second

	getBlockTemplateEnqueueTimeout  = 2 * time.Second
	getBlockTemplateResponseTimeout = 10 * time.Second
)

func (m *Miner) APIs() []api.API {
	return []api.API{
		{
			NameSpace: cmds.DefaultServiceNameSpace,
			Service:   NewPublicMinerAPI(m),
			Public:    true,
		},
		{
			NameSpace: cmds.MinerNameSpace,
			Service:   NewPrivateMinerAPI(m),
			Public:    false,
		},
	}
}

type PublicMinerAPI struct {
	miner *Miner
}

func decodedSubmitBlockHeaderLen(hexBlockHeader string) int {
	return (len(hexBlockHeader) + 1) / 2
}

func decodeSubmitBlockHeaderHex(dst []byte, hexBlockHeader string) (int, error) {
	decodedLen := decodedSubmitBlockHeaderLen(hexBlockHeader)
	if len(dst) < decodedLen {
		return 0, fmt.Errorf("submit header decode buffer too small: got %d want %d", len(dst), decodedLen)
	}
	out := 0
	i := 0
	if len(hexBlockHeader)%2 != 0 {
		nibble, ok := hexNibble(hexBlockHeader[0])
		if !ok {
			return 0, hex.InvalidByteError(hexBlockHeader[0])
		}
		dst[out] = nibble
		out++
		i = 1
	}
	for ; i < len(hexBlockHeader); i += 2 {
		high, ok := hexNibble(hexBlockHeader[i])
		if !ok {
			return out, hex.InvalidByteError(hexBlockHeader[i])
		}
		low, ok := hexNibble(hexBlockHeader[i+1])
		if !ok {
			return out, hex.InvalidByteError(hexBlockHeader[i+1])
		}
		dst[out] = high<<4 | low
		out++
	}
	return out, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func NewPublicMinerAPI(m *Miner) *PublicMinerAPI {
	pmAPI := &PublicMinerAPI{miner: m}
	return pmAPI
}

// func (api *PublicMinerAPI) GetBlockTemplate(request *mining.TemplateRequest) (any, error){
func (api *PublicMinerAPI) GetBlockTemplate(ctx context.Context, capabilities []string, powType byte, payAddress *string) (any, error) {
	readiness := api.miner.miningRPCReadiness()
	if !readiness.getTemplateReady {
		return nil, rpc.NodeIsSyncingError("Block Template Request Rejected", "Node is Syncing")
	}
	// Set the default mode and override it if supplied.
	mode := "template"
	request := json.TemplateRequest{Mode: mode, Capabilities: capabilities, PowType: powType}
	if payAddress != nil {
		request.PayAddress = *payAddress
	}

	switch mode {
	case "template":
		start := time.Now().UnixMilli()
		log.Debug("gbtstart")
		api.miner.stats.TotalGbtRequests++
		totalGbtRequests.Update(api.miner.stats.TotalGbtRequests)
		data, err := handleGetBlockTemplateRequest(ctx, api, &request)
		if err != nil {
			return nil, err
		}
		txcount := len(data.(*json.GetBlockTemplateResult).Transactions)
		if txcount < 1 {
			api.miner.stats.MempoolEmptyWarns++
		} else {
			api.miner.stats.LastestMempoolEmptyTimestamp = 0
		}
		api.miner.StatsGbtRequest(time.Now().UnixMilli()-start, txcount, data.(*json.GetBlockTemplateResult).LongPollID)
		log.Debug("gbtend", "txcount", txcount, "longpollid",
			data.(*json.GetBlockTemplateResult).LongPollID, "spent", (time.Now().UnixMilli()-start)/1000)
		return data, err
	}
	return nil, rpc.RpcInvalidError("Invalid mode")
}

// GetMiningStats func (api *PublicMinerAPI) GetMiningStats() (any, error){
func (api *PrivateMinerAPI) GetMiningStats() (any, error) {
	b, err := api.miner.stats.MarshalJSON()
	return string(b), err
}

// LL
// handleGetBlockTemplateRequest is a helper for handleGetBlockTemplate which
// deals with generating and returning block templates to the caller. In addition,
// it detects the capabilities reported by the caller
// in regards to whether or not it supports creating its own coinbase (the
// coinbasetxn and coinbasevalue capabilities) and modifies the returned block
// template accordingly.
func handleGetBlockTemplateRequest(ctx context.Context, api *PublicMinerAPI, request *json.TemplateRequest) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reply := make(chan *gbtResponse, 1)
	err := api.miner.GBTMining(ctx, request, reply)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(getBlockTemplateResponseTimeout)
	defer timer.Stop()
	select {
	case resp := <-reply:
		if resp == nil {
			return nil, rpc.RpcInternalError("getBlockTemplate failed", "miner returned an empty response")
		}
		return resp.result, resp.err
	case <-ctx.Done():
		return nil, rpc.RpcInternalError("getBlockTemplate canceled", ctx.Err().Error())
	case <-timer.C:
		return nil, rpc.RpcInternalError("getBlockTemplate timed out", fmt.Sprintf("mining template did not complete within %s", getBlockTemplateResponseTimeout))
	}
}

// LL
// Attempts to submit new block to network.
// See https://en.bitcoin.it/wiki/BIP_0022 for full specification
func (api *PublicMinerAPI) SubmitBlock(ctx context.Context, hexBlock string) (any, error) {
	if err := api.checkSubmitLimit(); err != nil {
		return nil, err
	}
	api.miner.stats.LastestSubmit = time.Now()
	// Deserialize the hexBlock.
	m := api.miner

	if len(hexBlock)%2 != 0 {
		hexBlock = "0" + hexBlock
	}
	serializedBlock, err := hex.DecodeString(hexBlock)

	if err != nil {
		return nil, rpc.RpcDecodeHexError(hexBlock)
	}
	block, err := types.NewBlockFromBytes(serializedBlock)
	if err != nil {
		return nil, rpc.RpcDeserializationError("Block decode failed: %s", err.Error())
	}

	// Because it's asynchronous, so you must ensure that all tips are referenced

	start := time.Now()
	blockHash := block.Block().BlockHash()
	blockHashString := blockHash.String()
	txCount := len(block.Block().Transactions)
	log.Debug("submitstart", "blockhash", blockHash, "txcount", txCount)
	res, err := m.submitBlockContext(ctx, block)
	if err == nil {
		api.miner.StatsSubmit(start, blockHashString, txCount-1)
	}

	log.Debug("submitend", "blockhash", blockHash, "txcount",
		txCount, "res", res, "err", err, "spent", time.Since(start))
	return res, err
}

func (api *PublicMinerAPI) GetMinerInfo() (any, error) {
	if !api.miner.IsEnable() {
		return nil, fmt.Errorf("Miner is disable. You can enable by --miner.")
	}
	if api.miner.template == nil || api.miner.worker == nil {
		return nil, fmt.Errorf("Not ready")
	}
	result := json.MinerInfoResult{}
	result.Timestamp = api.miner.template.Block.Header.Timestamp.String()
	result.Height = api.miner.template.Height
	result.Pow = pow.GetPowName(api.miner.powType)
	result.Difficulty = fmt.Sprintf("%x", api.miner.template.Block.Header.Difficulty)
	result.Target = fmt.Sprintf("%064x", pow.CompactToBig(api.miner.template.Block.Header.Difficulty))
	result.Coinbase = api.miner.coinbaseAddress.String()
	result.CoinbaseFlags = string(api.miner.coinbaseFlags)
	api.miner.submitLocker.Lock()
	result.TotalSubmit = api.miner.totalSubmit
	result.SuccessSubmit = api.miner.successSubmit
	api.miner.submitLocker.Unlock()
	if api.miner.worker != nil {
		result.Running = api.miner.worker.IsRunning()
		result.Type = api.miner.worker.GetType()
	}

	return &result, nil
}

func (api *PublicMinerAPI) GetTemplateHealth() (any, error) {
	return api.miner.buildTemplateHealthResult(time.Now()), nil
}

func (api *PublicMinerAPI) GetRemoteGBT(powType byte, extraNonce *bool) (any, error) {
	return nil, rpc.RpcInvalidError("Remote mining is disabled")
}

// BlockTemplate creates a WebSocket subscription that streams the latest
// RemoteGBTResult every time the node (re)creates a block template. Pool
// software invokes this via Blockdag_subscribe("blockTemplate") over
// WebSocket and receives pushes on the returned subscription id.
//
// The first push is sent eagerly using the current template (if any) so
// subscribers do not have to wait for the next template cycle to bootstrap.
// Subsequent pushes are delivered whenever updateBlockTemplate fires.
//
// Each payload carries a monotonically increasing Seq field; subscribers
// MUST discard any push whose Seq is lower than the highest Seq already
// observed to guard against out-of-order delivery after reconnects.
func (api *PublicMinerAPI) BlockTemplate(ctx context.Context, powType *byte) (*rpc.Subscription, error) {
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return nil, rpc.ErrNotificationsUnsupported
	}

	sub := notifier.CreateSubscription()

	// Keep the event.Feed subscriber channel shallow and drain it into a
	// latest-only bridge. event.Feed waits for subscriber channels, so the
	// bridge must keep draining even when the WebSocket client is slow.
	ch := make(chan *json.RemoteGBTResult, 1)
	latest := make(chan *json.RemoteGBTResult, 1)
	feedSub := api.miner.TemplateEvent().Subscribe(ch)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case bt, ok := <-ch:
				if !ok {
					return
				}
				if bt == nil || !api.miner.templatePayloadAdmitted(bt.Seq) || !api.miner.miningRPCReadiness().getTemplateReady {
					continue
				}
				queueLatestBlockTemplate(latest, bt)
			case <-done:
				return
			case <-sub.Err():
				return
			case <-notifier.Closed():
				return
			case <-feedSub.Err():
				return
			}
		}
	}()

	go func() {
		defer close(done)
		defer feedSub.Unsubscribe()
		// Notify drops inactive frames. The bridge keeps draining while the
		// sender waits for the response/activation handoff, without a timer.
		select {
		case <-sub.Activated():
		case <-sub.Err():
			return
		case <-notifier.Closed():
			return
		case <-feedSub.Err():
			return
		}

		// Eagerly deliver a snapshot of the current template so the
		// pool can start building jobs immediately after subscribe.
		if api.miner.miningRPCReadiness().getTemplateReady {
			if bt := api.miner.buildRemoteGBTResult(); bt != nil && api.miner.templatePayloadAdmitted(bt.Seq) {
				queueLatestBlockTemplate(latest, bt)
			}
		}

		for {
			select {
			case bt, ok := <-latest:
				if !ok {
					return
				}
				if hook := api.miner.templateNotifyAdmissionHook; hook != nil {
					hook(bt)
				}
				if bt == nil || !api.miner.templatePayloadAdmitted(bt.Seq) || !api.miner.miningRPCReadiness().getTemplateReady {
					continue
				}
				if err := notifier.Notify(sub.ID, bt); err != nil {
					return
				}
			case <-sub.Err():
				return
			case <-notifier.Closed():
				return
			case <-feedSub.Err():
				return
			}
		}
	}()

	return sub, nil
}

func queueLatestBlockTemplate(ch chan *json.RemoteGBTResult, bt *json.RemoteGBTResult) {
	if bt == nil {
		return
	}
	select {
	case ch <- bt:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- bt:
	default:
	}
}

func (api *PublicMinerAPI) SubmitBlockHeader(ctx context.Context, hexBlockHeader string, extraNonce *uint64) (any, error) {
	res, err := api.submitBlockHeaderResult(ctx, hexBlockHeader, extraNonce)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (api *PublicMinerAPI) SubmitBlockHeaderV2(ctx context.Context, hexBlockHeader string, extraNonce *uint64) (any, error) {
	res, err := api.submitBlockHeaderResult(ctx, hexBlockHeader, extraNonce)
	if err != nil && !isSubmitOutcomeError(err) {
		return nil, err
	}
	return res, nil
}

func (api *PublicMinerAPI) submitBlockHeaderResult(ctx context.Context, hexBlockHeader string, extraNonce *uint64) (json.SubmitBlockResult, error) {
	if err := api.checkSubmitLimit(); err != nil {
		return json.SubmitBlockResult{}, err
	}
	api.miner.stats.LastestSubmit = time.Now()
	// Deserialize the hexBlock.
	m := api.miner

	// Compute the decoded length before normalizing odd input. Valid oversized
	// input is rejected from the original bytes, so the size-error path never
	// allocates a normalized copy proportional to untrusted input length.
	originalHeader := hexBlockHeader
	headerLen := decodedSubmitBlockHeaderLen(originalHeader)
	// Retain legacy precedence and normalized error text only on the
	// malformed-input path. Valid oversized bytes never need normalization.
	if headerLen > types.BaseBlockHeaderPayload {
		for i := 0; i < len(originalHeader); i++ {
			if _, ok := hexNibble(originalHeader[i]); !ok {
				if len(originalHeader)%2 != 0 {
					originalHeader = "0" + originalHeader
				}
				return json.SubmitBlockResult{}, rpc.RpcDecodeHexError(originalHeader)
			}
		}
		return json.SubmitBlockResult{}, fmt.Errorf("block header size %d exceeds %d", headerLen, types.BaseBlockHeaderPayload)
	}
	hexBlockHeader = originalHeader
	if len(hexBlockHeader)%2 != 0 {
		hexBlockHeader = "0" + hexBlockHeader
	}
	serializedBlockHeader := make([]byte, types.BaseBlockHeaderPayload)
	if _, err := decodeSubmitBlockHeaderHex(serializedBlockHeader[:headerLen], hexBlockHeader); err != nil {
		return json.SubmitBlockResult{}, rpc.RpcDecodeHexError(hexBlockHeader)
	}
	var header types.BlockHeader
	err := header.DeserializeForMiningSubmit(bytes.NewReader(serializedBlockHeader))
	// log.Info("3")
	if err != nil {
		return json.SubmitBlockResult{}, err
	}

	// log.Info(fmt.Sprintf("block header received from miner is ===> %+v",header))
	// log.Info("4")

	return m.submitBlockHeaderContext(ctx, &header)
}

func (api *PublicMinerAPI) checkSubmitLimit() error {
	// if time.Since(api.miner.stats.LastestSubmit) < SubmitInterval {
	// 	return fmt.Errorf("Submission interval Limited:%s < %s\n", time.Since(api.miner.stats.LastestSubmit), SubmitInterval)
	// }
	return nil
}

// PrivateMinerAPI providzes private RPC methods to control the miner.
type PrivateMinerAPI struct {
	miner *Miner
}

func NewPrivateMinerAPI(m *Miner) *PrivateMinerAPI {
	pmAPI := &PrivateMinerAPI{miner: m}
	return pmAPI
}
