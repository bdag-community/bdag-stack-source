package synch

import (
	"context"
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
)

func (s *Sync) sendStateRootRequest(stream network.Stream, pe *peers.Peer) (*ethcommon.Hash, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("state root request rsp")
		return nil, e
	}
	msg := &pb.StateRootRsp{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	if !msg.Has {
		return nil, nil
	}
	hash, err := changePBHashToHash(msg.Root)
	if err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	return hash, nil
}

func (s *Sync) stateRootHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.StateRootReq)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.StateRootReq")
		return ErrMessage(err)
	}
	blockHash, err := changePBHashToHash(m.Block)
	if err != nil {
		return ErrMessage(fmt.Errorf("invalid block hash: %w", err))
	}
	rsp := &pb.StateRootRsp{Has: false}
	block := s.p2p.BlockChain().Blockdag().GetBlock(blockHash)
	if block != nil && block.GetState() != nil {
		rsp.Has = true
		rsp.Root = &pb.Hash{Hash: block.GetState().Root().Bytes()}
	}
	return s.EncodeResponseMsg(stream, rsp)
}
