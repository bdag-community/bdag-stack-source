package synch

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
)

func (s *Sync) sendGraphStateRequest(stream network.Stream, pe *peers.Peer) (*pb.GraphState, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("graph state request rsp")
		return nil, e
	}
	msg := &pb.GraphState{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	return msg, nil
}

func (s *Sync) graphStateHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.GraphState)
	if !ok {
		err := fmt.Errorf("message is not type *pb.GraphState")
		return ErrMessage(err)
	}
	pe.UpdateGraphState(m)
	go s.peerSync.PeerUpdate(pe, false)

	return s.EncodeResponseMsg(stream, s.getGraphState())
}

func (ps *PeerSync) processUpdateGraphState(pe *peers.Peer) error {
	if !pe.IsConnected() {
		err := fmt.Errorf("peer is not active")
		log.Warn(err.Error())
		return err
	}
	log.Trace(fmt.Sprintf("UpdateGraphState recevied from %v, state=%v ", pe.GetID(), pe.GraphState()))

	ret, err := ps.sy.Send(pe, RPCGraphState, ps.sy.getGraphState())
	if err != nil {
		log.Debug(err.Error())
		return err
	}
	gs := ret.(*pb.GraphState)
	pe.UpdateGraphState(gs)
	go ps.PeerUpdate(pe, false)
	return nil
}

func (ps *PeerSync) UpdateGraphState(pe *peers.Peer) {
	// Ignore if we are shutting down.
	if atomic.LoadInt32(&ps.shutdown) != 0 {
		return
	}
	pe.RunRate(UpdateGraphState, UpdateGraphStateTime, func() {
		if err := ps.processUpdateGraphState(pe); err != nil {
			log.Error(fmt.Sprintf("Failed to process update graph state for peer %v: %v", pe.Address(), err))
		}
	})
}
