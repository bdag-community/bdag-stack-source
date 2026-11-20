package p2p

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPeerDialInFlightSuppressesDuplicateDial(t *testing.T) {
	s := &Service{}
	id := peer.ID("peer-one")

	token, ok := s.beginPeerDial(id)
	if !ok {
		t.Fatal("first dial should be accepted")
	}
	if _, ok := s.beginPeerDial(id); ok {
		t.Fatal("duplicate in-flight dial should be suppressed")
	}

	s.endPeerDial(id, token.Add(time.Second))
	if _, ok := s.beginPeerDial(id); ok {
		t.Fatal("wrong token must not clear in-flight dial")
	}

	s.endPeerDial(id, token)
	if _, ok := s.beginPeerDial(id); !ok {
		t.Fatal("dial should be accepted after matching completion token")
	}
}

func TestPeerDialInFlightAllowsStaleRetry(t *testing.T) {
	s := &Service{
		peerDials: map[peer.ID]time.Time{
			peer.ID("peer-one"): time.Now().Add(-peerDialInFlightTTL - time.Second),
		},
	}

	if _, ok := s.beginPeerDial(peer.ID("peer-one")); !ok {
		t.Fatal("stale in-flight dial marker should allow a retry")
	}
}
