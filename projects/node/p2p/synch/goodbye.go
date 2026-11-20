package synch

import (
	"context"
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (s *Sync) sendGoodByeMessage(message any, pe *peers.Peer) *common.Error {
	codeV := message.(*uint64)
	code := common.ErrorCode(*codeV)
	s.recordGoodbye(goodbyeDirectionOutbound, code, pe, "")
	return nil
}

// goodbyeRPCHandler reads the incoming goodbye rpc message from the peer.
func (s *Sync) goodbyeRPCHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*uint64)
	if !ok {
		return ErrMessage(fmt.Errorf("wrong message type for goodbye, got %T, wanted *uint64", msg))
	}
	s.recordGoodbyeWire(goodbyeDirectionInbound, *m, pe, "")
	// closes all streams with the peer
	go s.peerSync.immediatelyDisconnected(pe)
	return nil
}

func (s *Sync) recordGoodbyeWire(direction string, raw uint64, pe *peers.Peer, detail string) {
	code, codeInt := normalizeGoodbyeWireCode(raw)
	s.recordGoodbyeRecord(direction, code, codeInt, pe, detail)
}

func (s *Sync) sendGoodByeAndDisconnect(code common.ErrorCode, pe *peers.Peer) error {
	return s.sendGoodByeAndDisconnectWithDetail(code, pe, "")
}

func (s *Sync) sendGoodByeAndDisconnectWithDetail(code common.ErrorCode, pe *peers.Peer, detail string) error {
	codeV := uint64(code)
	_, err := s.Send(pe, RPCGoodByeTopic, &codeV)
	if err != nil {
		// Send failed before sendGoodByeMessage ran; still record the intent.
		s.recordGoodbye(goodbyeDirectionOutbound, code, pe, firstNonEmpty(detail, err.Error()))
		log.Info("Could not send goodbye message", "peer", peerIDAddr(pe), "code", code.String(), "err", err)
	} else if detail != "" {
		s.attachGoodbyeDetail(pe, goodbyeDirectionOutbound, code, detail)
		log.Info("Goodbye detail", "peer", peerIDAddr(pe), "direction", goodbyeDirectionOutbound, "code", code.String(), "detail", detail)
	}
	go s.peerSync.immediatelyDisconnected(pe)
	return err
}

func (s *Sync) recordGoodbye(direction string, code common.ErrorCode, pe *peers.Peer, detail string) {
	s.recordGoodbyeRecord(direction, code.String(), int(code), pe, detail)
}

func (s *Sync) recordGoodbyeRecord(direction, code string, codeInt int, pe *peers.Peer, detail string) {
	if s == nil {
		return
	}
	code, codeInt = normalizeGoodbyeRecordCode(code, codeInt)
	rec := GoodbyeRecord{
		At:        time.Now().UTC(),
		Direction: direction,
		Code:      code,
		CodeInt:   codeInt,
		Detail:    detail,
	}
	var pid peer.ID
	if pe != nil {
		pid = pe.GetID()
		rec.PeerID = pid.String()
		rec.PeerAddr = pe.IDWithAddress()
		rec.Consensus = pe.IsConsensus()
		if snap, err := pe.StatsSnapshot(); err == nil && snap != nil {
			rec.Reconnects = snap.ReConnect
		}
	}
	if s.goodbyes != nil {
		s.goodbyes.record(rec, pid)
	}
	log.Info("Peer goodbye",
		"direction", direction,
		"code", code,
		"code_int", codeInt,
		"peer", firstNonEmpty(rec.PeerAddr, rec.PeerID),
		"consensus", rec.Consensus,
		"reconnects", rec.Reconnects,
		"detail", detail)
}

func (s *Sync) attachGoodbyeDetail(pe *peers.Peer, direction string, code common.ErrorCode, detail string) {
	if s == nil || s.goodbyes == nil || pe == nil || detail == "" {
		return
	}
	s.goodbyes.mu.Lock()
	defer s.goodbyes.mu.Unlock()
	if n := len(s.goodbyes.records); n > 0 {
		last := &s.goodbyes.records[n-1]
		if last.PeerID == pe.GetID().String() && last.Direction == direction && last.Code == code.String() && last.Detail == "" {
			last.Detail = detail
		}
	}
}

func (s *Sync) recordSilentDisconnect(pe *peers.Peer) {
	if s == nil || pe == nil || s.goodbyes == nil {
		return
	}
	now := time.Now().UTC()
	if s.goodbyes.consumeRecentGoodbye(pe.GetID(), now) {
		return
	}
	rec := GoodbyeRecord{
		At:        now,
		Direction: goodbyeDirectionUnknown,
		Code:      goodbyeCodeNone,
		CodeInt:   -1,
		PeerID:    pe.GetID().String(),
		PeerAddr:  pe.IDWithAddress(),
		Consensus: pe.IsConsensus(),
		Detail:    "libp2p disconnect without goodbye RPC",
	}
	if snap, err := pe.StatsSnapshot(); err == nil && snap != nil {
		rec.Reconnects = snap.ReConnect
	}
	s.goodbyes.record(rec, pe.GetID())
	log.Info("Peer disconnect without goodbye",
		"peer", rec.PeerAddr,
		"consensus", rec.Consensus,
		"reconnects", rec.Reconnects)
}

// GoodbyeSnapshot returns the most recent goodbye/disconnect records.
func (s *Sync) GoodbyeSnapshot(limit int) []GoodbyeRecord {
	if s == nil || s.goodbyes == nil {
		return nil
	}
	return s.goodbyes.snapshot(limit)
}

// GoodbyeCounts returns direction/code tallies for the current ring buffer.
func (s *Sync) GoodbyeCounts() map[string]int {
	if s == nil || s.goodbyes == nil {
		return map[string]int{}
	}
	return s.goodbyes.counts()
}

func peerIDAddr(pe *peers.Peer) string {
	if pe == nil {
		return ""
	}
	return pe.IDWithAddress()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
