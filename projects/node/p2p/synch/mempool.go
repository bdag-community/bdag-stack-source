package synch

import (
	"context"
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
)

func (s *Sync) SendMempoolRequest(stream network.Stream, pe *peers.Peer) *common.Error {
	pe.SetMempoolReqTime(time.Now())
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("mempool request rsp")
		return e
	}
	return nil
}

func (s *Sync) HandlerMemPool(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	mpr, ok := msg.(*pb.MemPoolRequest)
	if !ok {
		err := fmt.Errorf("message is not type *MsgFilterLoad")
		return ErrMessage(err)
	}
	if s.p2p.Consensus().Config().Miner {
		return s.EncodeResponseMsg(stream, nil)
	}

	pendingCount, queuedCount := s.p2p.TxMemPool().Stats()
	curCount := uint64(pendingCount + queuedCount)
	if shouldSendMempoolInventory(mpr.TxsNum, curCount) {
		err := s.peerSync.OnMemPool(pe, &MsgMemPool{InventoryCountHint: int(curCount)})
		if err != nil {
			return ErrMessage(err)
		}
	}
	return s.EncodeResponseMsg(stream, nil)
}

func shouldSendMempoolInventory(requestedCount uint64, currentCount uint64) bool {
	return currentCount != 0 && requestedCount != currentCount
}

// OnMemPool is invoked when a peer receives a mempool Blockdag message.
// It creates and sends an inventory message with the contents of the memory
// pool up to the maximum inventory allowed per message.  When the peer has a
// bloom filter loaded, the contents are filtered accordingly.
func (ps *PeerSync) OnMemPool(sp *peers.Peer, msg *MsgMemPool) error {
	// Only allow mempool requests if the server has bloom filtering
	// enabled.
	// services := sp.Services()
	// if services&protocol.Bloom != protocol.Bloom {
	// 	return fmt.Errorf("%s sent a filterclear request with no "+
	// 		"filter loaded -- disconnecting", sp.GetID().String())
	// }

	// Generate inventory message with the available transactions in the
	// transaction memory pool.  Limit it to the max allowed inventory
	// per message.  The NewMsgInvSizeHint function automatically limits
	// the passed hint to the maximum allowed, so it's safe to pass it
	// without double checking it here.
	// Pull back both pending and queued txs from the mempool.
	pending, queued := ps.sy.p2p.TxMemPool().Content()
	invs := buildMempoolInventory(pending, queued, msg.InventoryCountHint)
	// Send the inventory message if there is anything to send.
	if err := ps.sy.tryToSendInventoryRequest(sp, invs); err != nil {
		log.Error(fmt.Sprintf("Failed to send inventory request to peer %s: %v", sp.GetID().String(), err))
	}

	return nil
}

func buildMempoolInventory(pending, queued map[ethcommon.Address][]*ethtypes.Transaction, capacityHint int) []*pb.InvVect {
	if capacityHint < 0 {
		capacityHint = 0
	}
	invs := make([]*pb.InvVect, 0, capacityHint)
	appendInvs := func(txs []*ethtypes.Transaction) {
		for _, tx := range txs {
			txHash := tx.Hash()
			invs = append(invs, NewInvVect(InvTypeTx, &txHash))
		}
	}
	for _, batch := range pending {
		appendInvs(batch)
	}
	for _, batch := range queued {
		appendInvs(batch)
	}
	return invs
}
