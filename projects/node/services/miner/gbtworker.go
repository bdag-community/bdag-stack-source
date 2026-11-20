package miner

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/ethereum/go-ethereum/common"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

// LL
// gbtNonceRange is two 32-bit big-endian hexadecimal integers which
// represent the valid ranges of nonces returned by the getblocktemplate
// RPC.
const gbtNonceRange = "00000000ffffffff"

var (
	gbtMutableFields       = []string{"time", "transactions/add", "prevblock", "coinbase/append"}
	gbtCapabilities        = []string{"proposal"}
	gbtEmptyTxDependencies = make([]int64, 0)
)

type GBTWorker struct {
	started  int32
	shutdown int32

	miner *Miner
	sync.Mutex

	coinbaseAux *json.GetBlockTemplateResultAux
	cache       *gbtSerializedTemplateCache
	// generationFailure prevents a retained template from being published
	// after a required rebuild failed. It is cleared only when a replacement
	// template is installed.
	generationFailure error
}

type gbtSerializedTemplateKey struct {
	fingerprint  [32]byte
	lastTemplate int64
	powType      pow.PowType
}

type gbtSerializedTemplateCache struct {
	key          gbtSerializedTemplateKey
	transactions []json.GetBlockTemplateResultTx
	parents      []json.GetBlockTemplateResultPt
	target       string
	longPollID   string
}

func (w *GBTWorker) GetType() string {
	return GBTWorkerType
}

func (w *GBTWorker) Start() error {
	// Already started?
	if atomic.AddInt32(&w.started, 1) != 1 {
		return nil
	}

	log.Info("Start GBT Worker...")
	err := w.miner.initCoinbase()
	if err != nil {
		log.Warn(fmt.Sprintf("You will not be allowed to use <coinbasetxn> :%s", err.Error()))
	}
	if err := w.miner.updateBlockTemplate(false, nil); err != nil {
		w.miner.invalidateGBTWorkerGeneration(err)
		return err
	}
	return nil
}

func (w *GBTWorker) Stop() {
	if atomic.AddInt32(&w.shutdown, 1) != 1 {
		log.Warn("GBT Worker is already in the process of shutting down")
		return
	}
	log.Info("Stop GBT Worker...")
}

func (w *GBTWorker) IsRunning() bool {
	return atomic.LoadInt32(&w.started) != 0
}

func (w *GBTWorker) Update() {
	if atomic.LoadInt32(&w.shutdown) != 0 {
		return
	}
	w.Lock()
	w.invalidateSerializedTemplateCacheLocked()
	w.Unlock()
}

func (w *GBTWorker) invalidateGeneration(err error) {
	w.Lock()
	w.invalidateGenerationLocked(err)
	w.Unlock()
}

func (w *GBTWorker) invalidateGenerationLocked(err error) {
	w.generationFailure = err
	w.invalidateSerializedTemplateCacheLocked()
}

func (w *GBTWorker) updateAfterTemplateBuild(previous, current *types.BlockTemplate) {
	w.Lock()
	w.invalidateSerializedTemplateCacheLocked()
	if previous != current && current != nil {
		w.generationFailure = nil
	}
	w.Unlock()
}

// invalidateSerializedTemplateCache clears the derived GBT representation
// from callers that do not already hold the worker lock.
func (w *GBTWorker) invalidateSerializedTemplateCache() {
	w.Lock()
	w.invalidateSerializedTemplateCacheLocked()
	w.Unlock()
}

func (w *GBTWorker) invalidateSerializedTemplateCacheLocked() {
	w.cache = nil
}

func (w *GBTWorker) GetRequest(request *json.TemplateRequest, reply chan *gbtResponse) {
	if atomic.LoadInt32(&w.shutdown) != 0 {
		reply <- &gbtResponse{nil, fmt.Errorf("GBTWorker is not running ")}
		return
	}

	w.Lock()
	if err := w.generationFailure; err != nil {
		w.Unlock()
		reply <- &gbtResponse{nil, err}
		return
	}
	if err := w.miner.gbtGenerationError(); err != nil {
		w.generationFailure = err
		w.invalidateSerializedTemplateCacheLocked()
		w.Unlock()
		reply <- &gbtResponse{nil, err}
		return
	}
	// The miner handler serializes template builds. A successful build calls
	// updateAfterTemplateBuild, which acquires this mutex itself. Keep only
	// worker cache/generation operations locked, never the build callback.
	w.Unlock()

	// Extract the relevant passed capabilities and restrict the result to
	// either a coinbase value or a coinbase transaction object depending on
	// the request.  Default to only providing a coinbase value.
	useCoinbaseValue := true
	var powtyp byte
	if request != nil {
		var hasCoinbaseValue, hasCoinbaseTxn bool
		for _, capability := range request.Capabilities {
			switch capability {
			case "coinbasetxn":
				hasCoinbaseTxn = true
			case "coinbasevalue":
				hasCoinbaseValue = true
			}
		}

		if hasCoinbaseTxn && !hasCoinbaseValue {
			useCoinbaseValue = false
		}
		powtyp = request.PowType
	}

	// When a coinbase transaction has been requested, respond with an error
	// if there are no addresses to pay the created block template to.
	if !useCoinbaseValue && w.miner.coinbaseAddress == ethcommon.HexToAddress("0x00") && (request == nil || request.PayAddress == "") {
		reply <- &gbtResponse{nil, rpc.RpcInternalError("No payment addresses specified ",
			"A coinbase transaction has been requested, "+
				"but the server has not been configured with "+
				"any payment addresses via --miningaddr")}
		return
	}

	// Get and return a block template.  A new block template will be
	// generated when the current best block has changed or the transactions
	// in the memory pool have been updated and it has been at least five
	// seconds since the last template was generated.  Otherwise, the
	// timestamp for the existing block template is updated .
	var ephemeralAddr *ethcommon.Address
	if request != nil && request.PayAddress != "" {
		if !ethcommon.IsHexAddress(request.PayAddress) {
			reply <- &gbtResponse{nil, fmt.Errorf("invalid getBlockTemplate payAddress")}
			return
		}
		addr := ethcommon.HexToAddress(request.PayAddress)
		if addr == (ethcommon.Address{}) {
			reply <- &gbtResponse{nil, fmt.Errorf("getBlockTemplate payAddress must be nonzero")}
			return
		}
		ephemeralAddr = &addr
	}

	previousTemplate := w.miner.template
	if w.miner.powType != pow.PowType(powtyp) {
		previousPowType := w.miner.powType
		w.miner.powType = pow.PowType(powtyp)
		if err := w.miner.updateBlockTemplate(true, ephemeralAddr); err != nil {
			w.miner.powType = previousPowType
			w.invalidateSerializedTemplateCache()
			reply <- &gbtResponse{nil, err}
			return
		}
	} else if ephemeralAddr != nil || (w.miner.template != nil && w.miner.template.Block != nil && w.miner.template.Block.Header.CoinbaseAddress != w.miner.coinbaseAddress) {
		// An explicit request may use another address. A subsequent default
		// request must restore the configured payout without changing it.
		if err := w.miner.updateBlockTemplate(false, ephemeralAddr); err != nil {
			w.invalidateSerializedTemplateCache()
			reply <- &gbtResponse{nil, err}
			return
		}
	}
	w.Lock()
	defer w.Unlock()
	if previousTemplate != w.miner.template {
		w.generationFailure = nil
		w.miner.clearGBTGenerationFailure(previousTemplate, w.miner.template)
	}
	result, err := w.getResultForAddress(nil, ephemeralAddr)
	reply <- &gbtResponse{result, err}
}

// blockTemplateResult returns the current block template associated with the
// state as a GetBlockTemplateResult that is ready to be encoded to JSON
// and returned to the caller.
//
// This function MUST be called with the state locked.
func (w *GBTWorker) getResult(submitOld *bool) (*json.GetBlockTemplateResult, error) {
	return w.getResultForAddress(submitOld, nil)
}

func (w *GBTWorker) getResultForAddress(submitOld *bool, requested *ethcommon.Address) (*json.GetBlockTemplateResult, error) {
	resultStart := time.Now()
	defer gbtResultDuration.UpdateSince(resultStart)
	success := false
	defer func() {
		if !success {
			w.invalidateSerializedTemplateCacheLocked()
		}
	}()
	if err := w.generationFailure; err != nil {
		return nil, err
	}
	if err := w.miner.gbtGenerationError(); err != nil {
		w.generationFailure = err
		return nil, err
	}
	// Ensure the timestamps are still in valid range for the template.
	// This should really only ever happen if the local clock is changed
	// after the template is generated, but it's important to avoid serving
	// invalid block templates.
	template := w.miner.template
	if template == nil {
		return nil, fmt.Errorf("No template")
	}
	if requested != nil {
		if *requested == (ethcommon.Address{}) {
			return nil, fmt.Errorf("getBlockTemplate payAddress must be nonzero")
		}
		if err := validateBlockTemplateCoinbase(template, requested, "getBlockTemplate"); err != nil {
			return nil, err
		}
	} else if err := w.miner.validateCurrentTemplateCoinbase("getBlockTemplate"); err != nil {
		return nil, err
	}
	msgBlock := template.Block
	header := &msgBlock.Header
	adjustedTime := w.miner.timeSource.AdjustedTime()
	maxTime := adjustedTime.Add(time.Second * blockchain.MaxTimeOffsetSeconds)
	if header.Timestamp.After(maxTime) {
		return nil, rpc.RpcInvalidError("The template time is after the maximum allowed time for a block - template time %v, maximum time %v", adjustedTime, maxTime)
	}
	serialized, err := w.serializedTemplate(template)
	if err != nil {
		return nil, err
	}

	blockFee := template.BlockFees
	// for coinid, val := range template.BlockFees {
	// 	blockFeeMap[int(coinid)] = val
	// }
	reply := json.GetBlockTemplateResult{
		StateRoot:    template.Block.Header.StateRoot.String(),
		CurTime:      template.Block.Header.Timestamp.Unix(),
		Height:       int64(template.Height),
		NodeInfo:     version.String() + ":" + w.miner.policy.PeerID,
		Blues:        template.Blues,
		PreviousHash: template.Block.Header.ParentRoot.String(),
		WeightLimit:  types.MaxBlockWeight,
		SigOpLimit:   types.MaxBlockSigOpsCost,
		SizeLimit:    types.MaxBlockPayload,
		//TODO，transactions
		// make([]json.GetBlockTemplateResultTx, 0, 1)
		Parents:      cloneGBTParents(serialized.parents),
		Transactions: cloneGBTTransactions(serialized.transactions),
		Version:      template.Block.Header.Version,
		LongPollID:   serialized.longPollID,
		//TODO, submitOld
		SubmitOld: submitOld,
		PowDiffReference: json.PowDiffReference{
			Target: serialized.target,
			NBits:  strconv.FormatInt(int64(template.Difficulty), 16),
		},
		MinTime: w.miner.minTimestamp.Unix(),
		MaxTime: maxTime.Unix(),
		// gbtMutableFields
		Mutable:    cloneGBTStrings(gbtMutableFields),
		NonceRange: gbtNonceRange,
		// TODO, Capabilities
		Capabilities:    cloneGBTStrings(gbtCapabilities),
		BlockFees:       blockFee,
		CoinbaseAddress: template.Block.Header.CoinbaseAddress,
		Reward:          template.Block.Header.Reward,
		TxRoot:          template.Block.Header.TxRoot.String(),
		PowRoot:         template.Block.Header.PowRoot.String(),
	}

	// if useCoinbaseValue {
	// 	reply.CoinbaseAux = w.coinbaseAux
	// 	v := uint64(msgBlock.Transactions[0].TxOut[0].Amount.Value)
	// 	reply.CoinbaseValue = &v
	// } else {
	// 	// Ensure the template has a valid payment address associated
	// 	// with it when a full coinbase is requested.
	// 	if !template.ValidPayAddress {
	// 		return nil, rpc.RpcInvalidError("A coinbase transaction has been " +
	// 			"requested, but the server has not " +
	// 			"been configured with any payment " +
	// 			"addresses via --miningaddr")
	// 	}
	// 	// Serialize the transaction for conversion to hex.
	// 	tx := msgBlock.Transactions[0]
	// 	txBuf, err := tx.Serialize()
	// 	if err != nil {
	// 		context := "Failed to serialize transaction"
	// 		return nil, rpc.RpcInvalidError("%s %s", err.Error(), context)
	// 	}

	// 	resultTx := json.GetBlockTemplateResultTx{
	// 		Data:    hex.EncodeToString(txBuf),
	// 		Hash:    tx.TxHash().String(),
	// 		Depends: []int64{},
	// 		Fee:     template.Fees[0],
	// 		SigOps:  template.SigOpCounts[0],
	// 	}

	// 	reply.CoinbaseTxn = &resultTx
	// }
	log.Debug("gbtdetail", "txcount", len(reply.Transactions), "parents", reply.Parents, "prehash",
		reply.PreviousHash, "longpollid", reply.LongPollID)
	success = true
	return &reply, nil
}

func (w *GBTWorker) serializedTemplate(template *types.BlockTemplate) (*gbtSerializedTemplateCache, error) {
	key, err := serializedTemplateKey(template, w.miner.lastTemplate, w.miner.powType)
	if err != nil {
		w.invalidateSerializedTemplateCacheLocked()
		return nil, err
	}
	if w.cache != nil && w.cache.key == key {
		return w.cache, nil
	}
	cache, err := buildGBTSerializedTemplateCache(key, template)
	if err != nil {
		w.invalidateSerializedTemplateCacheLocked()
		return nil, err
	}
	w.cache = cache
	return cache, nil
}

func serializedTemplateKey(template *types.BlockTemplate, lastTemplate time.Time, powType pow.PowType) (gbtSerializedTemplateKey, error) {
	var encoded bytes.Buffer
	if template == nil || template.Block == nil {
		return gbtSerializedTemplateKey{}, fmt.Errorf("template unavailable")
	}
	if err := validateGBTTemplateFees(template); err != nil {
		return gbtSerializedTemplateKey{}, err
	}
	if template.Block.Header.Pow == nil {
		return gbtSerializedTemplateKey{}, fmt.Errorf("template has no proof of work")
	}
	if err := template.Block.Header.Serialize(&encoded); err != nil {
		return gbtSerializedTemplateKey{}, err
	}
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], template.Height)
	encoded.Write(scratch[:])
	binary.BigEndian.PutUint64(scratch[:], uint64(template.Difficulty))
	encoded.Write(scratch[:])
	binary.BigEndian.PutUint64(scratch[:], uint64(template.BlockFees))
	encoded.Write(scratch[:])
	binary.BigEndian.PutUint64(scratch[:], uint64(template.Blues))
	encoded.Write(scratch[:])
	if template.ValidPayAddress {
		encoded.WriteByte(1)
	} else {
		encoded.WriteByte(0)
	}
	binary.BigEndian.PutUint64(scratch[:], uint64(len(template.Block.Parents)))
	encoded.Write(scratch[:])
	for _, parent := range template.Block.Parents {
		if parent == nil {
			return gbtSerializedTemplateKey{}, fmt.Errorf("template contains nil parent")
		}
		encoded.Write(parent.Bytes())
	}
	binary.BigEndian.PutUint64(scratch[:], uint64(template.Block.Transactions.Len()))
	encoded.Write(scratch[:])
	for i, tx := range template.Block.Transactions {
		if tx == nil {
			return gbtSerializedTemplateKey{}, fmt.Errorf("template contains nil transaction")
		}
		txBytes, err := tx.MarshalBinary()
		if err != nil {
			return gbtSerializedTemplateKey{}, rpc.RpcInvalidError(err.Error(), "Failed to serialize transaction")
		}
		binary.BigEndian.PutUint64(scratch[:], uint64(len(txBytes)))
		encoded.Write(scratch[:])
		encoded.Write(txBytes)
		fee := template.Fees[i+1]
		binary.BigEndian.PutUint64(scratch[:], uint64(fee))
		encoded.Write(scratch[:])
	}
	return gbtSerializedTemplateKey{fingerprint: sha256.Sum256(encoded.Bytes()), lastTemplate: lastTemplate.UnixNano(), powType: powType}, nil
}

func buildGBTSerializedTemplateCache(key gbtSerializedTemplateKey, template *types.BlockTemplate) (*gbtSerializedTemplateCache, error) {
	if err := validateGBTTemplateFees(template); err != nil {
		return nil, err
	}
	numTx := template.Block.Transactions.Len()
	transactions := make([]json.GetBlockTemplateResultTx, 0, numTx)
	for i, tx := range template.Block.Transactions {
		if tx == nil {
			return nil, fmt.Errorf("template contains nil transaction")
		}
		txBytes, err := tx.MarshalBinary()
		if err != nil {
			return nil, rpc.RpcInvalidError(err.Error(), "Failed to serialize transaction")
		}
		fee := template.Fees[i+1]
		transactions = append(transactions, json.GetBlockTemplateResultTx{
			Data: hex.EncodeToString(txBytes), Hash: tx.Hash().String(), Depends: gbtEmptyTxDependencies,
			Fee: fee, Weight: 2000000,
		})
	}
	parents := make([]json.GetBlockTemplateResultPt, 0, len(template.Block.Parents))
	for _, parent := range template.Block.Parents {
		parents = append(parents, json.GetBlockTemplateResultPt{Data: hex.EncodeToString(parent.Bytes()), Hash: parent.String()})
	}
	target := fmt.Sprintf("%064x", pow.CompactToBig(template.Difficulty))
	return &gbtSerializedTemplateCache{
		key: key, transactions: transactions, parents: parents, target: target,
		longPollID: encodeTemplateID(template.Block.Header.ParentRoot, time.Unix(0, key.lastTemplate)),
	}, nil
}

func cloneGBTTransactions(src []json.GetBlockTemplateResultTx) []json.GetBlockTemplateResultTx {
	dst := make([]json.GetBlockTemplateResultTx, len(src))
	copy(dst, src)
	for i := range dst {
		if src[i].Depends != nil {
			dst[i].Depends = make([]int64, len(src[i].Depends))
			copy(dst[i].Depends, src[i].Depends)
		}
	}
	return dst
}

func cloneGBTStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func validateGBTTemplateFees(template *types.BlockTemplate) error {
	if template == nil || template.Block == nil {
		return fmt.Errorf("template unavailable")
	}
	// Fees[0] is the aggregate sentinel; each EVM transaction begins at slot 1.
	// Historical empty templates accepted nil or surplus observations.
	required := template.Block.Transactions.Len()
	if required > 0 && len(template.Fees) != required+1 {
		return fmt.Errorf("template fee observations are invalid: got %d, need %d", len(template.Fees), required+1)
	}
	return nil
}

func cloneGBTParents(src []json.GetBlockTemplateResultPt) []json.GetBlockTemplateResultPt {
	dst := make([]json.GetBlockTemplateResultPt, len(src))
	copy(dst, src)
	return dst
}

func NewGBTWorker(miner *Miner) *GBTWorker {
	w := GBTWorker{
		miner: miner,
	}
	// w.coinbaseAux = &json.GetBlockTemplateResultAux{
	// 	Flags: hex.EncodeToString(builderScript(txscript.NewScriptBuilder().
	// 		AddData([]byte(mining.CoinbaseFlagsStatic)))),
	// }
	return &w
}

// func builderScript(builder *txscript.ScriptBuilder) []byte {
// 	script, err := builder.Script()
// 	if err != nil {
// 		panic(err)
// 	}
// 	return script
// }

// LL
// encodeTemplateID encodes the passed details into an ID that can be used to
// uniquely identify a block template.
func encodeTemplateID(prevHash common.Hash, lastGenerated time.Time) string {
	return fmt.Sprintf("%s-%d", prevHash.String(), lastGenerated.Unix())
}
