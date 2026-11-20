package peers

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusStateLists(t *testing.T) {
	s := NewStatus(nil)

	connected := addStatusPeer(t, s, "connected", PeerConnected)
	connecting := addStatusPeer(t, s, "connecting", PeerConnecting)
	disconnecting := addStatusPeer(t, s, "disconnecting", PeerDisconnecting)
	disconnected := addStatusPeer(t, s, "disconnected", PeerDisconnected)

	assert.ElementsMatch(t, []peer.ID{connected}, s.Connected())
	assert.ElementsMatch(t, []peer.ID{connecting}, s.Connecting())
	assert.ElementsMatch(t, []peer.ID{disconnecting}, s.Disconnecting())
	assert.ElementsMatch(t, []peer.ID{disconnected}, s.Disconnected())
	assert.ElementsMatch(t, []peer.ID{connected, connecting, disconnecting, disconnected}, s.All())

	connectedPeers := s.ConnectedPeers()
	require.Len(t, connectedPeers, 1)
	assert.Equal(t, connected, connectedPeers[0].GetID())

	allPeers := s.AllPeers()
	require.Len(t, allPeers, 4)
	allPeerIDs := make([]peer.ID, 0, len(allPeers))
	for _, pe := range allPeers {
		allPeerIDs = append(allPeerIDs, pe.GetID())
	}
	assert.ElementsMatch(t, []peer.ID{connected, connecting, disconnecting, disconnected}, allPeerIDs)
}

func addStatusPeer(t *testing.T, s *Status, name string, state PeerConnectionState) peer.ID {
	t.Helper()

	pid := peer.ID(name)
	pe := NewPeer(pid, nil)
	pe.SetConnectionState(state)
	s.peers[pid] = pe
	return pid
}
