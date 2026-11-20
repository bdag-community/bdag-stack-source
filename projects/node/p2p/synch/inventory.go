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

func (s *Sync) tryToSendInventoryRequest(pe *peers.Peer, invs []*pb.InvVect) error {

	if s.PeerSync().pause {
		return fmt.Errorf("P2P is pause")
	}
	if !s.PeerSync().IsRunning() {
		return fmt.Errorf("P2P is not running")
	}
	for _, invMsg := range chunkInventory(invs, MaxInvPerMsg) {
		go func(msg *pb.Inventory) {
			s.Send(pe, RPCInventory, msg)
		}(invMsg)
	}

	return nil
}

func chunkInventory(invs []*pb.InvVect, maxPerMsg int) []*pb.Inventory {
	if len(invs) == 0 {
		return nil
	}
	if maxPerMsg <= 0 {
		maxPerMsg = MaxInvPerMsg
	}
	messages := make([]*pb.Inventory, 0, (len(invs)+maxPerMsg-1)/maxPerMsg)
	for start := 0; start < len(invs); start += maxPerMsg {
		end := start + maxPerMsg
		if end > len(invs) {
			end = len(invs)
		}
		chunk := append([]*pb.InvVect(nil), invs[start:end]...)
		messages = append(messages, &pb.Inventory{Invs: chunk})
	}
	return messages
}

func (s *Sync) sendInventoryRequest(stream network.Stream, pe *peers.Peer) *common.Error {

	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("inventory request rsp")
		return e
	}
	return nil
}

func (s *Sync) inventoryHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {

	m, ok := msg.(*pb.Inventory)
	if !ok {
		err := fmt.Errorf("message is not type *pb.Inventory")
		return ErrMessage(err)
	}
	err := s.handleInventory(m, pe)
	if err != nil {
		return ErrMessage(err)
	}
	return s.EncodeResponseMsg(stream, nil)
}

func (s *Sync) handleInventory(msg *pb.Inventory, pe *peers.Peer) error {

	if msg == nil {
		return fmt.Errorf("missing inventory")
	}
	for i, inv := range msg.Invs {
		if inv == nil {
			return fmt.Errorf("invalid inventory vector at index %d: missing vector", i)
		}
		if _, err := changePBHashToHash(inv.Hash); err != nil {
			return fmt.Errorf("invalid inventory hash at index %d: %w", i, err)
		}
	}
	if len(msg.Invs) <= 0 {
		return nil
	}
	txs := make([]*ethcommon.Hash, 0, len(msg.Invs))
	hasBlocks := false
	isCurrent := s.peerSync.IsCurrent()
	for _, inv := range msg.Invs {
		switch InvType(inv.Type) {
		case InvTypeBlock:
			hasBlocks = true
		case InvTypeTx:
			// log.Info(fmt.Sprintf("handleInventory: DisableRelayTx=%v, IsCurrent=%v", s.p2p.Config().DisableRelayTx, isCurrent))
			if s.p2p.Config().DisableRelayTx || !isCurrent {
				continue
			}
			h, err := changePBHashToHash(inv.Hash)
			if err != nil {
				return fmt.Errorf("invalid inventory tx hash: %w", err)
			}
			if s.haveInventoryHash(InvTypeTx, h) {
				continue
			}
			txs = append(txs, h)
		}
	}
	if hasBlocks {
		s.peerSync.UpdateGraphState(pe)
	}
	if len(txs) > 0 {
		go s.peerSync.getTxs(pe, txs)
	}
	return nil
}

func (s *Sync) haveInventory(invVect *pb.InvVect) bool {
	if invVect == nil {
		return true
	}
	h, err := changePBHashToHash(invVect.Hash)
	if err != nil {
		return true
	}
	return s.haveInventoryHash(InvType(invVect.Type), h)
}

func (s *Sync) haveInventoryHash(invType InvType, h *ethcommon.Hash) bool {
	if h == nil {
		return true
	}
	switch invType {
	case InvTypeBlock:
		known := s.p2p.BlockChain().HaveBlock(h)
		return known

	case InvTypeTx:
		known := s.p2p.TxMemPool().Has(*h)
		return known
	}

	return true
}
