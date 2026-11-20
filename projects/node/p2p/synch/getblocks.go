package synch

import (
	"context"
	"fmt"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
)

func (s *Sync) sendGetBlocksRequest(stream network.Stream, pe *peers.Peer) (*pb.DagBlocks, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("get blocks request rsp")
		return nil, e
	}
	msg := &pb.DagBlocks{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	return msg, nil
}

func (s *Sync) getBlocksHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.GetBlocks)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.Hash")
		return ErrMessage(err)
	}
	if err := validateDAGHashes(m.Locator, MaxBlockLocatorsPerMsg); err != nil {
		return ErrMessage(err)
	}
	if ctx == nil || ctx.Err() != nil {
		return ErrMessage(fmt.Errorf("DAG request context is unavailable or cancelled"))
	}
	locators, err := changePBHashsToHashs(m.Locator)
	if err != nil {
		return ErrMessage(fmt.Errorf("invalid getblocks locator: %w", err))
	}
	blocks, _, err := s.PeerSync().dagSync.CalcSyncBlocksContext(ctx, nil, locators, blockdag.DirectMode, MaxBlockLocatorsPerMsg, s.p2p.BlockChain().IsCurrent())
	if err != nil {
		return ErrMessage(err)
	}
	bd := &pb.DagBlocks{Blocks: changeHashsToPBHashs(blocks)}
	return s.EncodeResponseMsg(stream, bd)
}

func (ps *PeerSync) processGetBlocks(pe *peers.Peer, blocks []*ethcommon.Hash) *ProcessResult {
	ret, err := ps.sy.Send(pe, RPCGetBlocks, &pb.GetBlocks{Locator: changeHashsToPBHashs(blocks)})
	if err != nil {
		log.Warn(err.Error(), "processID", ps.processID)
		return nil
	}
	db := ret.(*pb.DagBlocks)
	if len(db.Blocks) <= 0 {
		log.Warn("no block need to get", "processID", ps.processID)
		return nil
	}
	if ps.IsInterrupt() {
		return nil
	}
	blocks, conversionErr := changePBHashsToHashs(db.Blocks)
	if conversionErr != nil {
		log.Warn(conversionErr.Error(), "processID", ps.processID)
		return nil
	}
	return ps.processGetBlockDatas(pe, blocks)
}

func (s *Sync) GetDataHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.Inventory)
	if !ok {
		err := fmt.Errorf("message is not type *MsgFilterLoad")
		return ErrMessage(err)
	}
	s.peerSync.msgChan <- &GetDatasMsg{pe: pe, data: m}
	return nil
}
