package blockchain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/marshal"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	bjson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	rapi "github.com/BlockdagNetworkLabs/bdag/rpc/api"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	"github.com/ethereum/go-ethereum/common"
)

func (b *BlockChain) APIs() []rapi.API {
	apis := b.Service.APIs()
	apis = append(apis, []rapi.API{
		rapi.API{
			NameSpace: cmds.DefaultServiceNameSpace,
			Service:   NewPublicBlockAPI(b),
			Public:    true,
		},
	}...)
	return apis
}

type PublicBlockAPI struct {
	chain *BlockChain
}

func NewPublicBlockAPI(bc *BlockChain) *PublicBlockAPI {
	return &PublicBlockAPI{chain: bc}
}

func (api *PublicBlockAPI) GetBlockhash(order int64) (string, error) {
	if order == rapi.LatestBlockOrder.Int64() {
		order = int64(api.chain.BestSnapshot().GraphState.GetMainOrder())
	}
	blockHash, err := api.chain.BlockHashByOrder(uint64(order))
	if err != nil {
		return "", err
	}
	return blockHash.String(), nil
}

// Return the hash range of block from 'start' to 'end'(exclude self)
// if 'end' is equal to zero, 'start' is the number that from the last block to the Gen
// if 'start' is greater than or equal to 'end', it will just return the hash of 'start'
func (api *PublicBlockAPI) GetBlockhashByRange(start int64, end int64) ([]string, error) {
	totalOrder := int64(api.chain.BestSnapshot().GraphState.GetMainOrder())
	if start > totalOrder {
		return nil, fmt.Errorf("startOrder(%d) is greater than or equal to the totalOrder(%d)", start, totalOrder)
	}
	result := []string{}
	if start >= end && end != 0 && end != rapi.LatestBlockOrder.Int64() {
		block, err := api.chain.BlockByOrder(uint64(start))
		if err != nil {
			return nil, err
		}
		result = append(result, block.Hash().String())
	} else if end == 0 {
		for i := totalOrder; i >= 0; i-- {
			if int64(len(result)) >= start {
				break
			}
			block, err := api.chain.BlockByOrder(uint64(i))
			if err != nil {
				return nil, err
			}
			result = append(result, block.Hash().String())
		}
	} else {
		for i := start; i <= totalOrder; i++ {
			if i > end && end != rapi.LatestBlockOrder.Int64() {
				break
			}
			block, err := api.chain.BlockByOrder(uint64(i))
			if err != nil {
				return nil, err
			}
			result = append(result, block.Hash().String())
		}
	}
	return result, nil
}

func (api *PublicBlockAPI) GetBlockByOrder(order int64, verbose *bool, inclTx *bool, fullTx *bool) (any, error) {
	mainOrder := int64(api.chain.BestSnapshot().GraphState.GetMainOrder())
	if order == rapi.LatestBlockOrder.Int64() {
		order = mainOrder
	} else {
		if order > mainOrder {
			return nil, fmt.Errorf("Order is too big")
		}
	}

	blockHash, err := api.chain.BlockHashByOrder(uint64(order))
	if err != nil {
		return nil, err
	}
	vb := false
	if verbose != nil {
		vb = *verbose
	}
	iTx := true
	if inclTx != nil {
		iTx = *inclTx
	}
	fTx := true
	if fullTx != nil {
		fTx = *fullTx
	}
	return api.GetBlock(*blockHash, &vb, &iTx, &fTx)
}

func (api *PublicBlockAPI) GetBlock(h common.Hash, verbose *bool, inclTx *bool, fullTx *bool) (any, error) {
	return api.makeBlock(h, verbose, inclTx, fullTx, false)
}

func (api *PublicBlockAPI) GetBlockV2(h common.Hash, verbose *bool, inclTx *bool, fullTx *bool) (any, error) {
	return api.makeBlock(h, verbose, inclTx, fullTx, true)
}

func (api *PublicBlockAPI) GetBestBlockHash() (any, error) {
	best := api.chain.BestSnapshot()
	return best.Hash.String(), nil
}

// The total ordered Block count
func (api *PublicBlockAPI) GetBlockCount() (any, error) {
	best := api.chain.BestSnapshot()
	return best.GraphState.GetMainOrder() + 1, nil
}

// The total Block count, included possible blocks have not ordered by Blockdag consensus yet at the moments.
func (api *PublicBlockAPI) GetBlockTotal() (any, error) {
	best := api.chain.BestSnapshot()
	return best.GraphState.GetTotal(), nil
}

// GetBlockHeader implements the getblockheader command.
func (api *PublicBlockAPI) GetBlockHeader(hash common.Hash, verbose bool) (any, error) {

	// Fetch the block node
	node := api.chain.Blockdag().GetBlock(&hash)
	if node == nil {
		return nil, internalError(fmt.Errorf("no block").Error(), fmt.Sprintf("Block not found: %v", hash))
	}
	// Fetch the header from chain.
	blockHeader, err := api.chain.HeaderByHash(&hash)
	if err != nil {
		return nil, internalError(err.Error(), fmt.Sprintf("Block not found: %v", hash))
	}

	// When the verbose flag isn't set, simply return the serialized block
	// header as a hex-encoded string.
	if !verbose {
		var headerBuf bytes.Buffer
		err := blockHeader.Serialize(&headerBuf)
		if err != nil {
			context := "Failed to serialize block header"
			return nil, internalError(err.Error(), context)
		}
		return hex.EncodeToString(headerBuf.Bytes()), nil
	}
	// Get next block hash unless there are none.
	confirmations := int64(api.chain.Blockdag().GetConfirmations(node.GetID()))
	layer := api.chain.Blockdag().GetLayer(node.GetID())
	blockHeaderReply := json.GetBlockHeaderVerboseResult{
		Hash:          hash.String(),
		Confirmations: confirmations,
		Version:       int32(blockHeader.Version),
		ParentRoot:    blockHeader.ParentRoot.String(),
		TxRoot:        blockHeader.TxRoot.String(),
		StateRoot:     blockHeader.StateRoot.String(),
		Difficulty:    blockHeader.Difficulty,
		Layer:         uint32(layer),
		Time:          blockHeader.Timestamp.Unix(),
		PowResult:     blockHeader.Pow.GetPowResult(),
		Reward:        int64(blockHeader.Reward),
	}

	return blockHeaderReply, nil

}

// Query whether a given block is on the main chain.
// Note that some DAG protocols may not support this feature.
func (api *PublicBlockAPI) IsOnMainChain(h common.Hash) (any, error) {
	node := api.chain.Blockdag().GetBlock(&h)
	if node == nil {
		return nil, internalError(fmt.Errorf("no block").Error(), fmt.Sprintf("Block not found: %v", h))
	}
	isOn := api.chain.Blockdag().IsOnMainChain(node.GetID())

	return strconv.FormatBool(isOn), nil
}

// Return the current height of DAG main chain
func (api *PublicBlockAPI) GetMainChainHeight() (any, error) {
	return strconv.FormatUint(uint64(api.chain.Blockdag().GetMainChainTip().GetHeight()), 10), nil
}

// Return the weight of block
func (api *PublicBlockAPI) GetBlockWeight(h common.Hash) (any, error) {
	block, err := api.chain.FetchBlockByHash(&h)
	if err != nil {
		return nil, internalError(fmt.Errorf("no block").Error(), fmt.Sprintf("Block not found: %v", h))
	}
	return strconv.FormatInt(int64(types.GetBlockWeight(block.Block())), 10), nil
}

// Return the total number of orphan blocks, orphan block are the blocks have not been included into the DAG at this moment.
func (api *PublicBlockAPI) GetOrphansTotal() (any, error) {
	return api.chain.GetOrphansTotal(), nil
}

// Obsoleted GetBlockByID Method, since the confused naming, replaced by GetBlockByNum method
func (api *PublicBlockAPI) GetBlockByID(id uint64, verbose *bool, inclTx *bool, fullTx *bool) (any, error) {
	blockHash := api.chain.Blockdag().GetBlockHash(uint(id))
	if blockHash == nil {
		return nil, internalError(fmt.Errorf("no block").Error(), fmt.Sprintf("Block not found: id=%v", id))
	}
	vb := false
	if verbose != nil {
		vb = *verbose
	}
	iTx := true
	if inclTx != nil {
		iTx = *inclTx
	}
	fTx := true
	if fullTx != nil {
		fTx = *fullTx
	}
	return api.GetBlock(*blockHash, &vb, &iTx, &fTx)
}

// IsBlue:0:not blue;  1：blue  2：Cannot confirm
func (api *PublicBlockAPI) IsBlue(h common.Hash) (any, error) {
	ib := api.chain.Blockdag().GetBlock(&h)
	if ib == nil {
		return 2, internalError(fmt.Errorf("no block").Error(), fmt.Sprintf("Block not found: %s", h.String()))
	}
	confirmations := api.chain.Blockdag().GetConfirmations(ib.GetID())
	if confirmations == 0 {
		return 2, nil
	}
	if api.chain.Blockdag().IsBlue(ib.GetID()) {
		return 1, nil
	}
	return 0, nil
}

// Return a list hash of the tip blocks of the DAG at this moment.
func (api *PublicBlockAPI) Tips() (any, error) {
	tips := api.chain.Blockdag().GetTipsList()
	tipinfos := json.TipsInfo{Count: len(tips), Valid: []bjson.TipInfo{}}
	validtips := api.chain.Blockdag().GetValidTips(blockdag.MaxPriority)
	mtheight := api.chain.Blockdag().GetMainChainTip().GetHeight()

	temp := map[common.Hash]struct{}{}
	for _, tiphash := range validtips {
		temp[*tiphash] = struct{}{}
	}
	getTipInfo := func(block blockdag.IBlock, valid bool) bjson.TipInfo {
		pruneHeight := int64(block.GetHeight()) + api.chain.Blockdag().GetTipsDisLimit()
		pruneCD := pruneHeight - int64(mtheight)
		if pruneCD < 0 {
			pruneCD = 0
		}
		if valid {
			return bjson.TipInfo{ID: uint64(block.GetID()),
				Hash:   block.GetHash().String(),
				Height: uint64(block.GetHeight())}
		} else {
			return bjson.TipInfo{ID: uint64(block.GetID()),
				Hash:        block.GetHash().String(),
				Height:      uint64(block.GetHeight()),
				PruneHeight: uint64(pruneHeight),
				PruneCD:     uint64(pruneCD)}
		}
	}
	for _, tip := range tips {
		if tip.GetHash().Cmp(*validtips[0]) == 0 {
			tipinfos.Valid = append(tipinfos.Valid, getTipInfo(tip, true))
			break
		}
	}
	for _, tip := range tips {
		if tip.GetHash().Cmp(*validtips[0]) == 0 {
			continue
		}
		if _, ok := temp[*tip.GetHash()]; ok {
			tipinfos.Valid = append(tipinfos.Valid, getTipInfo(tip, true))
		} else {
			if len(tipinfos.Invalid) <= 0 {
				tipinfos.Invalid = []bjson.TipInfo{getTipInfo(tip, false)}
			} else {
				tipinfos.Invalid = append(tipinfos.Invalid, getTipInfo(tip, false))
			}
		}
	}
	return tipinfos, nil
}

// GetCoinbase
func (api *PublicBlockAPI) GetCoinbaseAddress(h common.Hash) (any, error) {
	blk, err := api.chain.FetchBlockByHash(&h)
	if err != nil {
		return nil, err
	}
	return blk.Block().Header.CoinbaseAddress, nil
}

// GetCoinbase
func (api *PublicBlockAPI) GetFees(h common.Hash) (int64, error) {
	fsm := api.chain.GetFees(&h)
	return fsm, nil
}

// func (api *PublicBlockAPI) GetTokenInfo() (any, error) {
// 	state := api.chain.GetCurTokenState()
// 	if state == nil {
// 		return nil, nil
// 	}

// 	tbs := []json.TokenState{}
// 	for _, v := range state.Types {
// 		ts := json.TokenState{}
// 		ts.CoinId = uint16(v.Id)
// 		ts.CoinName = v.Name
// 		ts.Owners = hex.EncodeToString(v.Owners)
// 		if v.Id != types.BDAGB && v.Id != types.BDAGB {
// 			ts.UpLimit = v.UpLimit
// 			ts.Enable = v.Enable
// 			for k, vb := range state.Balances {
// 				if k == v.Id {
// 					ts.Balance = vb.Balance
// 					ts.Lockedbdag = vb.Lockedbdag
// 				}
// 			}
// 		}
// 		tbs = append(tbs, ts)
// 	}
// 	return tbs, nil
// }

func internalError(err, context string) error {
	return fmt.Errorf("%s : %s", context, err)
}

func (api *PublicBlockAPI) GetStateRoot(order int64, verbose *bool) (any, error) {
	mainOrder := int64(api.chain.BestSnapshot().GraphState.GetMainOrder())
	if rapi.BlockOrder(order) == rapi.LatestBlockOrder {
		order = mainOrder
	} else {
		if order > mainOrder {
			return nil, fmt.Errorf("Order is too big")
		} else if order < 0 {
			return nil, fmt.Errorf("Order is too small")
		}
	}
	vb := false
	if verbose != nil {
		vb = *verbose
	}
	ib := api.chain.Blockdag().GetBlockByOrder(uint(order))
	if ib == nil {
		return nil, internalError(fmt.Errorf("no block").Error(), fmt.Sprintf("Block not found: %d", order))
	}
	api.chain.ChainRLock()
	defer api.chain.ChainRUnlock()
	if vb {
		ret := bjson.OrderedResult{
			bjson.KV{Key: "Hash", Val: ib.GetHash().String()},
			bjson.KV{Key: "Order", Val: order},
			bjson.KV{Key: "Height", Val: ib.GetHeight()},
			bjson.KV{Key: "Valid", Val: !ib.GetState().GetStatus().KnownInvalid()},
			bjson.KV{Key: "EVMStateRoot", Val: ib.GetState().GetEVMRoot().String()},
			bjson.KV{Key: "EVMHeight", Val: ib.GetState().GetEVMNumber()},
			bjson.KV{Key: "EVMHead", Val: ib.GetState().GetEVMHash().String()},
			bjson.KV{Key: "StateRoot", Val: ib.GetState().Root().String()},
		}
		return ret, nil
	}
	return ib.GetState().Root().String(), nil
}

// Get json format block through the EVM block number
// verbose: Show Details or hexadecimal
// inclTx: Show transactions or not
// fullTx: Is complete transaction information displayed
func (api *PublicBlockAPI) GetBlockByNumber(number uint64, verbose *bool, inclTx *bool, fullTx *bool) (any, error) {
	block := api.chain.GetBlockByNumber(number)
	if block == nil {
		return nil, fmt.Errorf("Can't find block: number=%d", number)
	}
	return api.makeBlock(*block.GetHash(), verbose, inclTx, fullTx, true)
}

func (api *PublicBlockAPI) makeBlock(h common.Hash, verbose *bool, inclTx *bool, fullTx *bool, showFees bool) (any, error) {

	vb := false
	if verbose != nil {
		vb = *verbose
	}
	iTx := true
	if inclTx != nil {
		iTx = *inclTx
	}
	fTx := true
	if fullTx != nil {
		fTx = *fullTx
	}

	block := api.chain.Blockdag().GetBlock(&h)
	if block == nil {
		return nil, fmt.Errorf("no node")
	}
	node := api.chain.GetBlockNode(block)
	blk := node.block
	// When the verbose flag isn't set, simply return the
	// network-serialized block as a hex-encoded string.
	if !vb {
		blkBytes, err := blk.Bytes()
		if err != nil {
			return nil, internalError(err.Error(),
				"Could not serialize block")
		}
		return hex.EncodeToString(blkBytes), nil
	}
	confirmations := int64(api.chain.Blockdag().GetConfirmations(block.GetID()))
	bd := api.chain.Blockdag()
	cs := bd.GetChildren(block)
	children := []*common.Hash{}
	if cs != nil && !cs.IsEmpty() {
		for _, v := range cs.GetMap() {
			children = append(children, v.(blockdag.IBlock).GetHash())
		}
	}
	coinbaseFees, _ := api.chain.CalculateFees(blk)

	// still need to fix coinbase amount
	coinbaseAmount := int64(0)
	//TODO, refactor marshal api
	fields, err := marshal.MarshalJsonBlock(block, blk, iTx, fTx, api.chain.params, confirmations, children, coinbaseAmount, coinbaseFees)
	if err != nil {
		return nil, err
	}
	return fields, nil

}
