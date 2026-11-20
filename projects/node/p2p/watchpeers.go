package p2p

import (
	"context"
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	peerReconnectDialTimeout    = 30 * time.Second
	peerReconnectFailureBackoff = 30 * time.Second
	peerReconnectMaxConcurrent  = 16
)

const (
	peerReconnectSkipBackoff  = "backoff"
	peerReconnectSkipInFlight = "inflight"
)

type reconnectDialer func(context.Context, peer.AddrInfo) error

// ensurePeerConnections will attempt to reestablish connection to the peers
// if there are currently no connections to that peer.
func (s *Service) ensurePeerConnections(pes []string) {
	if len(pes) == 0 ||
		s.sy.IsPeerAtLimit() {
		return
	}
	for _, p := range pes {
		if len(p) <= 0 {
			continue
		}
		peerInfo, err := MakePeer(p)
		if err != nil {
			log.Error(fmt.Sprintf("Could not make peer: %v", err))
			continue
		}
		pe := s.Peers().Get(peerInfo.ID)
		if pe != nil && !pe.CanConnectWithNetwork() {
			continue
		}

		c := s.Host().Network().ConnsToPeer(peerInfo.ID)
		if len(c) == 0 {
			log.Debug(fmt.Sprintf("No connections to peer, reconnecting:peer %v", peerInfo.ID))

			s.schedulePeerReconnect(*peerInfo, func(ctx context.Context, info peer.AddrInfo) error {
				err := s.connectWithPeerContext(ctx, info, true, true)
				if err != nil {
					log.Trace(fmt.Sprintf("Could not connect with peer %s :%v", info.String(), err))
				}
				return err
			})
		}
	}
}

func (s *Service) schedulePeerReconnect(info peer.AddrInfo, dial reconnectDialer) bool {
	return s.schedulePeerReconnectWithTimeout(info, peerReconnectDialTimeout, dial)
}

func (s *Service) schedulePeerReconnectWithTimeout(info peer.AddrInfo, timeout time.Duration, dial reconnectDialer) bool {
	if s == nil || info.ID == "" || dial == nil {
		return false
	}
	if ok, reason := s.beginPeerReconnectState(info.ID, time.Now()); !ok {
		switch reason {
		case peerReconnectSkipBackoff:
			common.PeerReconnectBackoffSkipsCounter.Inc(1)
		case peerReconnectSkipInFlight:
			common.PeerReconnectInFlightSkipsCounter.Inc(1)
		}
		return false
	}
	slots := s.peerReconnectSlots()
	select {
	case slots <- struct{}{}:
	default:
		s.clearPeerReconnect(info.ID)
		common.PeerReconnectSlotFullSkipsCounter.Inc(1)
		return false
	}
	common.PeerReconnectScheduledCounter.Inc(1)

	go func() {
		var err error
		defer func() {
			<-slots
			s.finishPeerReconnect(info.ID, err, time.Now())
		}()

		ctx, cancel := s.reconnectDialContext(timeout)
		defer cancel()
		common.PeerReconnectAttemptsCounter.Inc(1)
		err = dial(ctx, info)
	}()
	return true
}

func (s *Service) beginPeerReconnect(id peer.ID, now time.Time) bool {
	ok, _ := s.beginPeerReconnectState(id, now)
	return ok
}

func (s *Service) beginPeerReconnectState(id peer.ID, now time.Time) (bool, string) {
	if s == nil || id == "" {
		return false, ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	if next, ok := s.reconnectAfter[id]; ok && now.Before(next) {
		s.updatePeerReconnectGaugesLocked(now)
		return false, peerReconnectSkipBackoff
	}
	if s.reconnecting == nil {
		s.reconnecting = make(map[peer.ID]struct{})
	}
	if _, ok := s.reconnecting[id]; ok {
		s.updatePeerReconnectGaugesLocked(now)
		return false, peerReconnectSkipInFlight
	}
	s.reconnecting[id] = struct{}{}
	s.updatePeerReconnectGaugesLocked(now)
	return true, ""
}

func (s *Service) finishPeerReconnect(id peer.ID, err error, now time.Time) {
	if s == nil || id == "" {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	delete(s.reconnecting, id)
	if err == nil {
		delete(s.reconnectAfter, id)
		common.PeerReconnectSuccessesCounter.Inc(1)
		s.updatePeerReconnectGaugesLocked(now)
		return
	}
	if s.reconnectAfter == nil {
		s.reconnectAfter = make(map[peer.ID]time.Time)
	}
	s.reconnectAfter[id] = now.Add(peerReconnectFailureBackoff)
	common.PeerReconnectFailuresCounter.Inc(1)
	s.updatePeerReconnectGaugesLocked(now)
}

func (s *Service) clearPeerReconnect(id peer.ID) {
	if s == nil || id == "" {
		return
	}
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()
	delete(s.reconnecting, id)
	s.updatePeerReconnectGaugesLocked(time.Now())
}

func (s *Service) peerReconnectSlots() chan struct{} {
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()
	if s.reconnectSlots == nil {
		s.reconnectSlots = make(chan struct{}, peerReconnectMaxConcurrent)
	}
	return s.reconnectSlots
}

func (s *Service) reconnectDialContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.Background()
	if s != nil && s.Context() != nil {
		base = s.Context()
	}
	if timeout <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, timeout)
}

func (s *Service) peerReconnectStateCounts(now time.Time) (inFlight int, backoff int) {
	if s == nil {
		return 0, 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()
	return s.peerReconnectStateCountsLocked(now)
}

func (s *Service) peerReconnectStateCountsLocked(now time.Time) (inFlight int, backoff int) {
	if s == nil {
		return 0, 0
	}
	inFlight = len(s.reconnecting)
	for _, next := range s.reconnectAfter {
		if now.Before(next) {
			backoff++
		}
	}
	return inFlight, backoff
}

func (s *Service) updatePeerReconnectGaugesLocked(now time.Time) {
	inFlight, backoff := s.peerReconnectStateCountsLocked(now)
	common.PeerReconnectInFlightGauge.Update(int64(inFlight))
	common.PeerReconnectBackoffGauge.Update(int64(backoff))
}
