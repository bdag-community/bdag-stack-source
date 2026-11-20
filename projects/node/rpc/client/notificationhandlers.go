package client

import (
	"encoding/hex"
	"encoding/json"
	"time"

	j "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type NotificationHandlers struct {
	OnClientConnected   func()
	OnBlockConnected    func(hash *common.Hash, height, order int64, t time.Time, txs []*ethtypes.Transaction)
	OnBlockDisconnected func(hash *common.Hash, height, order int64, t time.Time, txs []*ethtypes.Transaction)
	OnBlockAccepted     func(hash *common.Hash, height, order int64, t time.Time, txs []*ethtypes.Transaction)
	OnReorganization    func(hash *common.Hash, order int64, olds []*common.Hash)
	OnTxAccepted        func(hash *common.Hash, amounts types.AmountGroup)
	OnTxAcceptedVerbose func(c *Client, tx *j.DecodeRawTransactionResult)
	OnTxConfirm         func(txConfirm *cmds.TxConfirmResult)
	OnRescanProgress    func(param *cmds.RescanProgressNtfn)
	OnRescanFinish      func(param *cmds.RescanFinishedNtfn)
	OnNodeExit          func(nodeExit *cmds.NodeExitNtfn)
	OnBlockTemplate     func(bt *j.RemoteGBTResult)

	OnUnknownNotification func(method string, params []json.RawMessage)
}

func parseChainNtfnParams(params []json.RawMessage) (*common.Hash, int64, int64, time.Time, []*ethtypes.Transaction, error) {
	if len(params) != 5 {
		return nil, 0, 0, time.Time{}, nil, wrongNumParams(len(params))
	}

	// Unmarshal first parameter as a string.
	var blockHashStr string
	err := json.Unmarshal(params[0], &blockHashStr)
	if err != nil {
		return nil, 0, 0, time.Time{}, nil, err
	}

	// Unmarshal second parameter as an integer.
	var height int64
	err = json.Unmarshal(params[1], &height)
	if err != nil {
		return nil, 0, 0, time.Time{}, nil, err
	}

	// Unmarshal third parameter as an integer.
	var blockOrder int64
	err = json.Unmarshal(params[2], &blockOrder)
	if err != nil {
		return nil, 0, 0, time.Time{}, nil, err
	}

	// Unmarshal fourth parameter as unix time.
	var blockTimeUnix int64
	err = json.Unmarshal(params[3], &blockTimeUnix)
	if err != nil {
		return nil, 0, 0, time.Time{}, nil, err
	}

	var txHexs []string
	err = json.Unmarshal(params[4], &txHexs)
	if err != nil {
		return nil, 0, 0, time.Time{}, nil, err
	}
	txs := []*ethtypes.Transaction{}
	for _, txHex := range txHexs {
		serializedTx, err := hex.DecodeString(txHex)
		if err != nil {
			return nil, 0, 0, time.Time{}, nil, err
		}
		var tx ethtypes.Transaction
		err = tx.UnmarshalBinary(serializedTx)
		if err != nil {
			return nil, 0, 0, time.Time{}, nil, err
		}
		txs = append(txs, &tx)
	}

	// Create hash from block hash string.
	blockHash, err := NewHashFromHex(blockHashStr)
	if err != nil {
		return nil, 0, 0, time.Time{}, nil, err
	}

	// Create time.Time from unix time.
	blockTime := time.Unix(blockTimeUnix, 0)

	return blockHash, height, blockOrder, blockTime, txs, nil
}

func parseReorganizationNtfnParams(params []json.RawMessage) (*common.Hash, int64, []*common.Hash, error) {
	if len(params) != 4 {
		return nil, 0, nil, wrongNumParams(len(params))
	}

	// Unmarshal first parameter as a string.
	var blockHashStr string
	err := json.Unmarshal(params[0], &blockHashStr)
	if err != nil {
		return nil, 0, nil, err
	}

	// Unmarshal second parameter as an integer.
	var blockOrder int64
	err = json.Unmarshal(params[2], &blockOrder)
	if err != nil {
		return nil, 0, nil, err
	}

	var oldsStr []string
	err = json.Unmarshal(params[3], &oldsStr)
	if err != nil {
		return nil, 0, nil, err
	}
	olds := []*common.Hash{}
	for _, oldStr := range oldsStr {
		oldHash, err := NewHashFromHex(oldStr)
		if err != nil {
			return nil, 0, nil, err
		}
		olds = append(olds, oldHash)
	}

	// Create hash from block hash string.
	blockHash, err := NewHashFromHex(blockHashStr)
	if err != nil {
		return nil, 0, nil, err
	}

	return blockHash, blockOrder, olds, nil
}

func parseTxAcceptedNtfnParams(params []json.RawMessage) (*common.Hash,
	types.AmountGroup, error) {

	if len(params) != 2 {
		return nil, nil, wrongNumParams(len(params))
	}

	// Unmarshal first parameter as a string.
	var txHashStr string
	err := json.Unmarshal(params[0], &txHashStr)
	if err != nil {
		return nil, nil, err
	}

	// Unmarshal second parameter as a floating point number.
	var amouts types.AmountGroup
	err = json.Unmarshal(params[1], &amouts)
	if err != nil {
		return nil, nil, err
	}

	// Decode string encoding of transaction sha.
	txHash, err := NewHashFromHex(txHashStr)
	if err != nil {
		return nil, nil, err
	}

	return txHash, amouts, nil
}

func parseTxAcceptedVerboseNtfnParams(params []json.RawMessage) (*j.DecodeRawTransactionResult,
	error) {

	if len(params) != 1 {
		return nil, wrongNumParams(len(params))
	}
	// Unmarshal first parameter as a raw transaction result object.
	var rawTx j.DecodeRawTransactionResult
	err := json.Unmarshal(params[0], &rawTx)
	if err != nil {
		log.Error("Unmarshal Tx Error", "err", err)
		return nil, err
	}
	return &rawTx, nil
}

func parseTxConfirm(params []json.RawMessage) (*cmds.TxConfirmResult,
	error) {
	// Unmarshal first parameter as result object.
	var txConfirm cmds.TxConfirmResult
	err := json.Unmarshal(params[0], &txConfirm)
	if err != nil {
		return nil, err
	}

	return &txConfirm, nil
}

func parseRescanProgress(params []json.RawMessage) (*cmds.RescanProgressNtfn,
	error) {

	if len(params) != 3 {
		return nil, wrongNumParams(len(params))
	}
	var h string
	err := json.Unmarshal(params[0], &h)
	if err != nil {
		return nil, err
	}
	var order uint64
	err = json.Unmarshal(params[1], &order)
	if err != nil {
		return nil, err
	}
	var tim int64
	// Unmarshal first parameter as result object.
	err = json.Unmarshal(params[2], &tim)
	if err != nil {
		return nil, err
	}

	return &cmds.RescanProgressNtfn{
		Hash:  h,
		Order: order,
		Time:  tim,
	}, nil
}

func parseRescanFinish(params []json.RawMessage) (*cmds.RescanFinishedNtfn,
	error) {

	if len(params) != 4 {
		return nil, wrongNumParams(len(params))
	}

	var h string
	err := json.Unmarshal(params[0], &h)
	if err != nil {
		return nil, err
	}
	var order uint64
	err = json.Unmarshal(params[1], &order)
	if err != nil {
		return nil, err
	}
	var tim int64
	// Unmarshal first parameter as result object.
	err = json.Unmarshal(params[2], &tim)
	if err != nil {
		return nil, err
	}
	var lastTxHash string
	// Unmarshal first parameter as result object.
	err = json.Unmarshal(params[3], &lastTxHash)
	if err != nil {
		return nil, err
	}

	return &cmds.RescanFinishedNtfn{
		Hash:       h,
		Order:      order,
		Time:       tim,
		LastTxHash: lastTxHash,
	}, nil
}

func parseBlockTemplateNtfnParams(params []json.RawMessage) (*j.RemoteGBTResult, error) {
	if len(params) != 1 {
		return nil, wrongNumParams(len(params))
	}
	// Unmarshal first parameter as a raw transaction result object.
	var rawTx j.RemoteGBTResult
	err := json.Unmarshal(params[0], &rawTx)
	if err != nil {
		log.Error("Unmarshal Tx Error", "err", err)
		return nil, err
	}
	return &rawTx, nil
}
