package synch

import (
	"context"
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// pingHandler reads the incoming ping rpc message from the peer.
func (s *Sync) pingHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*uint64)
	if !ok {
		return ErrMessage(fmt.Errorf("wrong message type for ping, got %T, wanted *uint64", msg))
	}
	valid, err := s.validateSequenceNum(*m, pe)
	if err != nil {
		return common.NewError(common.ErrDAGConsensus, err)
	}
	e := s.EncodeResponseMsg(stream, s.p2p.MetadataSeq())
	if e != nil {
		return e
	}
	if valid {
		return nil
	}

	// The sequence number was not valid.  Start our own ping back to the peer.
	go func(id peer.ID) {
		ret, err := s.Send(pe, RPCMetaDataTopic, nil)
		if err != nil {
			log.Debug(fmt.Sprintf("Failed to send metadata request:peer=%s  error=%v", id, err))
			return
		}
		md := ret.(*pb.MetaData)
		// update metadata if there is no error
		pe.SetMetadata(md)
	}(stream.Conn().RemotePeer())

	return nil
}

func (s *Sync) SendPingRequest(stream network.Stream, pe *peers.Peer) *common.Error {
	currentTime := roughtime.Now()
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("ping request rsp")
		return e
	}

	msg := new(uint64)
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return common.NewError(common.ErrStreamRead, err)
	}
	// Record the small ping response round-trip so sync and V2 artifact
	// selection can prefer nearby peers without relying on address classes.
	s.p2p.Host().Peerstore().RecordLatency(pe.GetID(), roughtime.Now().Sub(currentTime))

	valid, err := s.validateSequenceNum(*msg, pe)
	if err != nil {
		return common.NewError(common.ErrSequence, fmt.Errorf("ping request rsp validate seq num:%s", err))
	}
	if valid {
		return nil
	}

	ret, err := s.Send(pe, RPCMetaDataTopic, nil)
	if err != nil {
		return ErrMessage(err)
	}
	md := ret.(*pb.MetaData)
	pe.SetMetadata(md)
	return nil
}

// validates the peer's sequence number.
func (s *Sync) validateSequenceNum(seq uint64, pe *peers.Peer) (bool, error) {
	md := pe.Metadata()
	if md == nil {
		return false, nil
	}
	if md.SeqNumber != seq {
		return false, nil
	}
	return true, nil
}
