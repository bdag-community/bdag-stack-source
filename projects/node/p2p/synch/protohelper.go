package synch

import (
	"bytes"
	"fmt"
	"reflect"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

func changePBHashsToHashs(hs []*pb.Hash) ([]*common.Hash, error) {
	result := make([]*common.Hash, 0, len(hs))
	for i, ha := range hs {
		h, err := changePBHashToHash(ha)
		if err != nil {
			return nil, fmt.Errorf("invalid hash at index %d: %w", i, err)
		}
		result = append(result, h)
	}
	return result, nil
}

func changePBHashToHash(ha *pb.Hash) (*common.Hash, error) {
	if ha == nil {
		return nil, fmt.Errorf("missing hash")
	}
	if len(ha.Hash) != hash.HashSize {
		return nil, fmt.Errorf("invalid hash length %d, want %d", len(ha.Hash), hash.HashSize)
	}
	h, err := hash.NewHash(ha.Hash)
	if err != nil {
		return nil, fmt.Errorf("decode hash: %w", err)
	}
	return h, nil
}

func changeHashsToPBHashs(hs []*common.Hash) []*pb.Hash {
	result := make([]*pb.Hash, 0, len(hs))
	for _, ha := range hs {
		result = append(result, &pb.Hash{Hash: ha.Bytes()})
	}
	return result
}

func changePBTxToTx(tx *pb.Transaction) *ethtypes.Transaction {
	var transaction ethtypes.Transaction
	stream := rlp.NewStream(bytes.NewReader(tx.TxBytes), 0)
	err := transaction.DecodeRLP(stream)
	if err != nil {
		return nil
	}
	return &transaction
}

func getMessageString(message any) string {
	str := fmt.Sprintf("%v:", reflect.TypeOf(message))
	switch msg := message.(type) {
	case *pb.ChainState:
		gh, ghErr := changePBHashToHash(msg.GenesisHash)
		gs, gsErr := changePBGraphStateToGraphState(msg.GraphState)
		genesis := "<invalid>"
		if ghErr == nil {
			genesis = gh.String()
		}
		str += fmt.Sprintf(" genesis:%s version:%d timestamp:%d services:%d disableRelayTx:%v useragent:%s",
			genesis, msg.ProtocolVersion, msg.Timestamp, msg.Services, msg.DisableRelayTx, string(msg.UserAgent))
		if gsErr == nil {
			str += fmt.Sprintf(" graphstate:%s", gs.String())
		}
		return str
	case pb.GetBlockDatas:
		str += fmt.Sprintf(" locator:%d", len(msg.Locator))
		return str
	case pb.GetBlocks:
		str += fmt.Sprintf(" locator:%d", len(msg.Locator))
		return str
	case *pb.GraphState:
		gs, err := changePBGraphStateToGraphState(msg)
		if err == nil {
			str += fmt.Sprintf(" graphstate:%s", gs.String())
		}
		return str
	case *pb.Inventory:
		str += fmt.Sprintf(" invs:%d", len(msg.Invs))
		return str
	case *pb.MemPoolRequest:
		str += fmt.Sprintf(" txsNum:%d", msg.TxsNum)
		return str
	case *pb.SyncDAG:
		gs, err := changePBGraphStateToGraphState(msg.GraphState)
		str += fmt.Sprintf(" mainlocator:%d", len(msg.MainLocator))
		if err == nil {
			str += fmt.Sprintf(" graphstate:%s", gs.String())
		}
		return str
	case *pb.GetTxs:
		str += fmt.Sprintf(" txs:%d", len(msg.Txs))
		return str
	case *pb.BroadcastBlock:
		block, err := types.NewBlockFromBytes(msg.Block.BlockBytes)
		if err != nil {
			return err.Error()
		}
		str += fmt.Sprintf(" blockHash:%s", block.Hash().String())
		return str
	}
	str += fmt.Sprintf("%v", message)
	if len(str) > peers.MaxBadResponses {
		str = str[0:peers.MaxBadResponses]
	}
	return str
}

func changePBGraphStateToGraphState(csgs *pb.GraphState) (*blockdag.GraphState, error) {
	if csgs == nil {
		return nil, fmt.Errorf("missing graph state")
	}
	if len(csgs.Tips) > blockdag.MaxTips {
		return nil, fmt.Errorf("too many graph tips %d, want at most %d", len(csgs.Tips), blockdag.MaxTips)
	}
	gs := blockdag.NewGraphState()
	gs.SetTotal(uint(csgs.Total))
	gs.SetLayer(uint(csgs.Layer))
	gs.SetMainHeight(uint(csgs.MainHeight))
	gs.SetMainOrder(uint(csgs.MainOrder))
	tips := make([]*common.Hash, 0, len(csgs.Tips))
	for i, tip := range csgs.Tips {
		h, err := changePBHashToHash(tip)
		if err != nil {
			return nil, fmt.Errorf("invalid graph tip at index %d: %w", i, err)
		}
		tips = append(tips, h)
	}
	gs.SetTips(tips)
	return gs, nil
}
