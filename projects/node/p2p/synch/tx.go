package synch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	qcommon "github.com/BlockdagNetworkLabs/bdag/bevm/common"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtxpool "github.com/ethereum/go-ethereum/core/txpool"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const TXDATA_SSZ_HEAD_SIZE = 4
const transactionsSSZBaseSize = 4

func transactionEntrySSZSize(txBytes []byte) int {
	return TXDATA_SSZ_HEAD_SIZE + 4 + len(txBytes)
}

func (s *Sync) sendTxRequest(stream network.Stream, pe *peers.Peer) (*pb.Transactions, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("tx request rsp")
		return nil, e
	}
	msg := &pb.Transactions{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	return msg, nil
}

func (s *Sync) sendRelayTxRequest(stream network.Stream, pe *peers.Peer) (*pb.Hash, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("relay tx rsp")
		return nil, e
	}
	msg := &pb.Hash{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	return msg, nil
}

func (s *Sync) txHandler(ctx context.Context, msg interface{}, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.GetTxs)
	if !ok {
		err := fmt.Errorf("message is not type *pb.Transaction")
		return ErrMessage(err)
	}
	if m == nil {
		return ErrMessage(fmt.Errorf("missing transaction request"))
	}
	for i, tx := range m.Txs {
		if _, err := changePBHashToHash(tx); err != nil {
			return ErrMessage(fmt.Errorf("invalid tx hash at index %d: %w", i, err))
		}
	}
	capacity := len(m.Txs)
	if capacity > MaxInvPerMsg {
		capacity = MaxInvPerMsg
	}
	pbtxs := &pb.Transactions{Txs: make([]*pb.Transaction, 0, capacity)}
	responseSize := transactionsSSZBaseSize
	for _, tx := range m.Txs {
		if len(pbtxs.Txs) >= MaxInvPerMsg {
			break
		}
		txHash, err := changePBHashToHash(tx)
		if err != nil {
			return ErrMessage(fmt.Errorf("invalid tx hash: %w", err))
		}
		eths := s.p2p.TxMemPool().Get(*txHash)
		if eths == nil {
			log.Debug("transaction not found in mempool", "hash", txHash)
			continue
		}
		var buf bytes.Buffer
		buf.Grow(int(eths.Size()))
		err = eths.EncodeRLP(&buf)
		if err != nil {
			log.Warn(err.Error())
			continue
		}
		pbtx := &pb.Transaction{TxBytes: buf.Bytes()}
		entrySize := transactionEntrySSZSize(pbtx.TxBytes)
		if uint64(responseSize+entrySize) >= s.p2p.Encoding().GetMaxChunkSize() {
			break
		}
		pbtxs.Txs = append(pbtxs.Txs, pbtx)
		responseSize += entrySize
	}
	return s.EncodeResponseMsg(stream, pbtxs)
}

func (s *Sync) handleTxMsg(
	msg *pb.Transaction,
	pid peer.ID,
) (*ethcommon.Hash, error) {
	tx := changePBTxToTx(msg)
	if tx == nil {
		return nil, fmt.Errorf("message is not type *pb.Transaction")
	}
	txh := tx.Hash()
	errs := s.p2p.TxMemPool().Add([]*ethtypes.Transaction{tx}, false, false)
	if len(errs) > 0 && errs[0] != nil {
		if errors.Is(errs[0], ethtxpool.ErrAlreadyKnown) {
			return &txh, nil
		}
		return nil, errs[0]
	}

	cost := tx.Cost()
	cost = cost.Sub(cost, tx.Value())
	cost = cost.Div(cost, qcommon.Precision)
	fee := cost.Int64()
	txs := &types.TxDesc{
		Tx:       tx,
		Added:    time.Now(),
		Height:   int64(s.getGraphState().MainHeight + 1),
		Fee:      fee,
		FeePerKB: fee * 1000 / int64(tx.Size()),
	}
	transactions := []*types.TxDesc{txs}
	s.p2p.Notify().AnnounceNewTransactions(transactions, []peer.ID{pid})

	return &txh, nil
}

func (ps *PeerSync) processGetTxs(pe *peers.Peer, otxs []*ethcommon.Hash) error {
	if len(otxs) <= 0 {
		return nil
	}
	txsM := make(map[ethcommon.Hash]struct{}, len(otxs))
	var gtxs *pb.GetTxs
	flush := func() error {
		if gtxs == nil || len(gtxs.Txs) == 0 {
			return nil
		}
		ret, err := ps.sy.send(pe, RPCTransaction, gtxs)
		if err != nil {
			log.Error(fmt.Sprintf("Failed to send transaction request: %s", err.Error()))
			return err
		}
		gtxs = nil
		ptxs, ok := ret.(*pb.Transactions)
		if !ok || ptxs == nil {
			return fmt.Errorf("unexpected transaction response %T", ret)
		}
		for _, tx := range ptxs.Txs {
			if !ps.IsRunning() {
				return fmt.Errorf("No run PeerSync\n")
			}
			if tx == nil {
				return fmt.Errorf("nil transaction response")
			}
			if _, err := ps.sy.handleTxMsg(tx, pe.GetID()); err != nil {
				log.Debug(err.Error())
			}
		}
		return nil
	}
	for _, txh := range otxs {
		if !ps.IsRunning() {
			return fmt.Errorf("No run PeerSync\n")
		}
		if txh == nil {
			return fmt.Errorf("nil transaction hash")
		}
		if !shouldRequestTxHash(txsM, txh, false) {
			continue
		}
		if ps.sy.p2p.TxMemPool().Has(*txh) {
			delete(txsM, *txh)
			continue
		}

		if gtxs == nil {
			gtxs = &pb.GetTxs{Txs: make([]*pb.Hash, 0, MaxInvPerMsg)}
		}
		gtxs.Txs = append(gtxs.Txs, &pb.Hash{Hash: txh.Bytes()})
		if len(gtxs.Txs) == MaxInvPerMsg {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func shouldRequestTxHash(seen map[ethcommon.Hash]struct{}, txh *ethcommon.Hash, inMempool bool) bool {
	if txh == nil {
		return false
	}
	if _, ok := seen[*txh]; ok {
		return false
	}
	if inMempool {
		return false
	}
	seen[*txh] = struct{}{}
	return true
}

func (ps *PeerSync) getTxs(pe *peers.Peer, txs []*ethcommon.Hash) {
	// Ignore if we are shutting down.
	if atomic.LoadInt32(&ps.shutdown) != 0 {
		return
	}
	err := ps.processGetTxs(pe, txs)
	if err != nil {
		log.Debug(err.Error())
	}
}

func (s *Sync) relayTxHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.Transaction)
	if !ok {
		err := fmt.Errorf("message is not type *pb.Transaction")
		return ErrMessage(err)
	}
	h, err := s.handleTxMsg(m, pe.GetID())
	if err != nil {
		return ErrMessage(err)
	}
	return s.EncodeResponseMsg(stream, &pb.Hash{Hash: h.Bytes()})
}
