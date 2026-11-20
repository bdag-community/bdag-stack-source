package p2p

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
	"time"

	pv "github.com/BlockdagNetworkLabs/bdag/core/protocol"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestDefaultServicesAdvertiseOnlyCoreSyncFlags(t *testing.T) {
	want := pv.Full | pv.CF
	if defaultServices != want {
		t.Fatalf("default services = %s, want %s", defaultServices, want)
	}
}

func TestCloseHostClosesLibp2pHost(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("new libp2p host: %v", err)
	}
	if len(h.Addrs()) == 0 {
		t.Fatal("expected host to have a listen address")
	}
	addr := h.Addrs()[0]
	s := &Service{host: h}

	if err := s.closeHost(); err != nil {
		t.Fatalf("close host: %v", err)
	}
	if err := h.Network().Listen(addr); err == nil {
		t.Fatal("expected closed host network to reject new listen")
	}
}

func TestEligibleRelayTransactionPeersSkipsRelayDisabledPeers(t *testing.T) {
	disabledPeer := broadcastTestPeer("relay-disabled", pv.BroadcastblockProtocolVersion)
	disabledPeer.SetChainState(&pb.ChainState{
		ProtocolVersion: pv.BroadcastblockProtocolVersion,
		GraphState:      &pb.GraphState{},
		DisableRelayTx:  true,
	})
	eligiblePeer := broadcastTestPeer("relay-enabled", pv.BroadcastblockProtocolVersion)

	eligiblePeers := eligibleRelayTransactionPeers([]*peers.Peer{disabledPeer, eligiblePeer})
	if len(eligiblePeers) != 1 {
		t.Fatalf("eligible relay tx peers = %d, want 1", len(eligiblePeers))
	}
	if eligiblePeers[0].GetID() != eligiblePeer.GetID() {
		t.Fatalf("eligible peer = %s, want %s", eligiblePeers[0].GetID(), eligiblePeer.GetID())
	}
}

func TestEligibleBroadcastBlockPeersFiltersSourceAndOldProtocol(t *testing.T) {
	source := broadcastTestPeer("source", pv.BroadcastblockProtocolVersion)
	old := broadcastTestPeer("old-protocol", pv.BroadcastblockProtocolVersion-1)
	eligible := broadcastTestPeer("eligible", pv.BroadcastblockProtocolVersion)
	sourceID := source.GetID()

	got := eligibleBroadcastBlockPeers([]*peers.Peer{source, old, eligible}, &sourceID)
	if len(got) != 1 || got[0].GetID() != eligible.GetID() {
		t.Fatalf("eligible broadcast peers = %v, want [%s]", got, eligible.GetID())
	}
}

func TestEligibleBroadcastBlockPeersReturnsEmptyWithoutRecipients(t *testing.T) {
	source := broadcastTestPeer("source", pv.BroadcastblockProtocolVersion)
	old := broadcastTestPeer("old-protocol", pv.BroadcastblockProtocolVersion-1)
	sourceID := source.GetID()

	if got := eligibleBroadcastBlockPeers([]*peers.Peer{source, old}, &sourceID); len(got) != 0 {
		t.Fatalf("eligible broadcast peers = %d, want 0", len(got))
	}
}

func TestBroadcastBlockUsesProductionEncodingAndSendPath(t *testing.T) {
	source := broadcastTestPeer("source", pv.BroadcastblockProtocolVersion)
	old := broadcastTestPeer("old", pv.BroadcastblockProtocolVersion-1)
	eligible := broadcastTestPeer("eligible", pv.BroadcastblockProtocolVersion)
	block := coretypes.NewBlock(&coretypes.Block{Header: coretypes.BlockHeader{
		Timestamp: time.Unix(1, 0),
		Pow:       pow.GetInstance(pow.BLAKE2BD, 0, 0, nil),
	}})
	var got []byte
	done := make(chan struct{})
	sourceID := source.GetID()
	s := &Service{
		broadcastBlockPeers: func() []*peers.Peer { return []*peers.Peer{source, old, eligible} },
		broadcastBlockSend: func(pe *peers.Peer, message any) (any, error) {
			if pe != eligible {
				t.Fatalf("broadcast sent to %s, want eligible peer", pe.GetID())
			}
			got = append([]byte(nil), message.(*pb.BroadcastBlock).Block.BlockBytes...)
			close(done)
			return nil, nil
		},
	}
	if err := s.BroadcastBlock(block, &sourceID, time.Now()); err != nil {
		t.Fatalf("BroadcastBlock: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast send")
	}
	want, err := block.Bytes()
	if err != nil {
		t.Fatalf("serialize block: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("broadcast bytes mismatch: got %x want %x", got, want)
	}
}

func TestBroadcastBlockWithNoEligiblePeersDoesNotEncodeOrSend(t *testing.T) {
	source := broadcastTestPeer("source", pv.BroadcastblockProtocolVersion)
	old := broadcastTestPeer("old", pv.BroadcastblockProtocolVersion-1)
	block := coretypes.NewBlock(&coretypes.Block{Header: coretypes.BlockHeader{
		Timestamp: time.Unix(1, 0),
		Pow:       pow.GetInstance(pow.BLAKE2BD, 0, 0, nil),
	}})
	sourceID := source.GetID()
	sent := false
	s := &Service{
		broadcastBlockPeers: func() []*peers.Peer { return []*peers.Peer{source, old} },
		broadcastBlockSend: func(*peers.Peer, any) (any, error) {
			sent = true
			return nil, nil
		},
	}
	if err := s.BroadcastBlock(block, &sourceID, time.Now()); err != nil {
		t.Fatalf("BroadcastBlock: %v", err)
	}
	if sent {
		t.Fatal("broadcast sent with no eligible peers")
	}
}

func TestBroadcastBlockCopiesPayloadPerEligiblePeer(t *testing.T) {
	source := broadcastTestPeer("source-copy", pv.BroadcastblockProtocolVersion)
	first := broadcastTestPeer("eligible-copy-first", pv.BroadcastblockProtocolVersion)
	second := broadcastTestPeer("eligible-copy-second", pv.BroadcastblockProtocolVersion)
	block := coretypes.NewBlock(&coretypes.Block{Header: coretypes.BlockHeader{
		Timestamp: time.Unix(1, 0),
		Pow:       pow.GetInstance(pow.BLAKE2BD, 0, 0, nil),
	}})
	want, err := block.Bytes()
	if err != nil {
		t.Fatalf("serialize block: %v", err)
	}
	sourceID := source.GetID()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondDone := make(chan struct{})
	var secondBytes []byte
	s := &Service{
		broadcastBlockPeers: func() []*peers.Peer { return []*peers.Peer{source, first, second} },
		broadcastBlockSend: func(pe *peers.Peer, message any) (any, error) {
			payload := message.(*pb.BroadcastBlock).Block.BlockBytes
			switch pe {
			case first:
				close(firstStarted)
				payload[0] ^= 0xff
				<-releaseFirst
			case second:
				secondBytes = append([]byte(nil), payload...)
				close(secondDone)
			}
			return nil, nil
		},
	}
	if err := s.BroadcastBlock(block, &sourceID, time.Now()); err != nil {
		t.Fatalf("BroadcastBlock: %v", err)
	}
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first broadcast send")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		close(releaseFirst)
		t.Fatal("timed out waiting for second broadcast send")
	}
	close(releaseFirst)
	if !bytes.Equal(secondBytes, want) {
		t.Fatalf("second broadcast payload changed after first mutation: got %x want %x", secondBytes, want)
	}
}

func TestRelayTransactionUsesProductionEncodingAndContinuesAfterFailure(t *testing.T) {
	first := broadcastTestPeer("first", pv.BroadcastblockProtocolVersion)
	second := broadcastTestPeer("second", pv.BroadcastblockProtocolVersion)
	tx := ethtypes.NewTransaction(1, ethcommon.Address{1}, big.NewInt(1), 21000, big.NewInt(1), nil)
	wantHash := ethcommon.HexToHash("0x1234")
	var sent []peer.ID
	s := &Service{
		relayTransactionPeers: func() []*peers.Peer { return []*peers.Peer{first, second} },
		relayTransactionSend: func(pe *peers.Peer, message any) (any, error) {
			sent = append(sent, pe.GetID())
			if pe == first {
				return nil, errors.New("first peer failed")
			}
			if len(message.(*pb.Transaction).TxBytes) == 0 {
				t.Fatal("relay transaction payload was empty")
			}
			return &pb.Hash{Hash: wantHash.Bytes()}, nil
		},
	}
	got, err := s.RelayTransaction(tx)
	if err != nil {
		t.Fatalf("RelayTransaction: %v", err)
	}
	if got == nil || *got != wantHash {
		t.Fatalf("returned hash = %v, want %s", got, wantHash)
	}
	if len(sent) != 2 || sent[0] != first.GetID() || sent[1] != second.GetID() {
		t.Fatalf("sent peers = %v, want first then second", sent)
	}
}

func TestRelayTransactionWithNoEligiblePeersDoesNotEncodeOrSend(t *testing.T) {
	tx := ethtypes.NewTransaction(1, ethcommon.Address{1}, big.NewInt(1), 21000, big.NewInt(1), nil)
	sent := false
	s := &Service{
		relayTransactionPeers: func() []*peers.Peer { return nil },
		relayTransactionSend: func(*peers.Peer, any) (any, error) {
			sent = true
			return nil, nil
		},
	}
	if _, err := s.RelayTransaction(tx); err == nil {
		t.Fatal("RelayTransaction unexpectedly succeeded with no eligible peers")
	}
	if sent {
		t.Fatal("transaction sent with no eligible peers")
	}
}

func TestRelayTransactionSkipsTypedNilAndMalformedHashes(t *testing.T) {
	peersForRelay := []*peers.Peer{
		broadcastTestPeer("nil", pv.BroadcastblockProtocolVersion),
		broadcastTestPeer("short", pv.BroadcastblockProtocolVersion),
		broadcastTestPeer("long", pv.BroadcastblockProtocolVersion),
		broadcastTestPeer("valid-zero", pv.BroadcastblockProtocolVersion),
	}
	tx := ethtypes.NewTransaction(1, ethcommon.Address{1}, big.NewInt(1), 21000, big.NewInt(1), nil)
	responses := []any{
		(*pb.Hash)(nil),
		&pb.Hash{Hash: make([]byte, ethcommon.HashLength-1)},
		&pb.Hash{Hash: make([]byte, ethcommon.HashLength+1)},
		&pb.Hash{Hash: make([]byte, ethcommon.HashLength)},
	}
	var calls int
	s := &Service{
		relayTransactionPeers: func() []*peers.Peer { return peersForRelay },
		relayTransactionSend: func(_ *peers.Peer, _ any) (any, error) {
			response := responses[calls]
			calls++
			return response, nil
		},
	}
	got, err := s.RelayTransaction(tx)
	if err != nil {
		t.Fatalf("RelayTransaction: %v", err)
	}
	if got == nil || *got != (ethcommon.Hash{}) {
		t.Fatalf("returned hash = %v, want valid all-zero hash", got)
	}
	if calls != len(responses) {
		t.Fatalf("relay attempts = %d, want %d", calls, len(responses))
	}
}

func TestRelayTransactionMalformedResponsesDoNotReportSuccess(t *testing.T) {
	peer := broadcastTestPeer("malformed", pv.BroadcastblockProtocolVersion)
	tx := ethtypes.NewTransaction(1, ethcommon.Address{1}, big.NewInt(1), 21000, big.NewInt(1), nil)
	responses := []any{(*pb.Hash)(nil), &pb.Hash{Hash: make([]byte, ethcommon.HashLength-1)}}
	calls := 0
	s := &Service{
		relayTransactionPeers: func() []*peers.Peer { return []*peers.Peer{peer} },
		relayTransactionSend: func(_ *peers.Peer, _ any) (any, error) {
			response := responses[calls]
			calls++
			return response, nil
		},
	}
	if got, err := s.RelayTransaction(tx); err == nil || got != nil {
		t.Fatalf("RelayTransaction = (%v, %v), want failure", got, err)
	}
	if calls != 1 {
		t.Fatalf("malformed-only relay attempts = %d, want 1", calls)
	}
}

func broadcastTestPeer(id string, protocolVersion uint32) *peers.Peer {
	pe := peers.NewPeer(peer.ID(id), nil)
	pe.SetChainState(&pb.ChainState{
		ProtocolVersion: protocolVersion,
		GraphState:      &pb.GraphState{},
	})
	return pe
}
