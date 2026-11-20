package synch

import (
	"context"
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	ethcommon "github.com/ethereum/go-ethereum/common"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const BLOCKDATA_SSZ_HEAD_SIZE = 4
const blockDatasSSZBaseSize = 4

func blockDatasEntrySSZSize(blockBytes []byte) int {
	return BLOCKDATA_SSZ_HEAD_SIZE + 4 + len(blockBytes)
}

func (s *Sync) sendGetBlockDataRequest(stream network.Stream, pe *peers.Peer) (*pb.BlockDatas, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("get block date request rsp")
		return nil, e
	}
	msg := &pb.BlockDatas{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	return msg, nil
}

func (s *Sync) sendGetMerkleBlockDataRequest(ctx context.Context, id peer.ID, req *pb.MerkleBlockRequest) (*pb.MerkleBlockResponse, error) {
	return nil, nil
}

func (s *Sync) getBlockDataHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.GetBlockDatas)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.Hash")
		return ErrMessage(err)
	}
	locators, err := changePBHashsToHashs(m.Locator)
	if err != nil {
		return ErrMessage(fmt.Errorf("invalid getblockdatas locator: %w", err))
	}
	requestSize := s.p2p.Encoding().GetSize(m)
	log.Debug("[DEBUG-blockdata] inbound getblockdatas request",
		"protocol", stream.Protocol(),
		"peer", pe.GetID(),
		"remoteAddr", stream.Conn().RemoteMultiaddr(),
		"direction", stream.Stat().Direction,
		"requested", len(m.Locator),
		"requestFirst", debugHashPBAt(m.Locator, 0),
		"requestLast", debugHashPBAt(m.Locator, len(m.Locator)-1),
		"requestSize", requestSize)
	bds := make([]*pb.BlockData, 0, len(m.Locator))
	bd := &pb.BlockDatas{Locator: bds}
	responseSize := blockDatasSSZBaseSize
	bytesTotal := 0
	truncated := false
	for i, blockHash := range locators {
		blocks, err := s.p2p.BlockChain().FetchBlockBytesByHash(blockHash)
		if err != nil {
			log.Debug("[DEBUG-blockdata] inbound getblockdatas fetch failed",
				"protocol", stream.Protocol(),
				"peer", pe.GetID(),
				"remoteAddr", stream.Conn().RemoteMultiaddr(),
				"direction", stream.Stat().Direction,
				"index", i,
				"hash", blockHash.String(),
				"err", err)
			return ErrMessage(err)
		}
		pbbd := pb.BlockData{BlockBytes: blocks}
		entrySize := blockDatasEntrySSZSize(blocks)
		if uint64(responseSize+entrySize) >= s.p2p.Encoding().GetMaxChunkSize() {
			truncated = true
			break
		}
		bd.Locator = append(bd.Locator, &pbbd)
		responseSize += entrySize
		bytesTotal += len(blocks)
	}
	encodedSize := s.p2p.Encoding().GetSize(bd)
	log.Debug("[DEBUG-blockdata] inbound getblockdatas response",
		"protocol", stream.Protocol(),
		"peer", pe.GetID(),
		"remoteAddr", stream.Conn().RemoteMultiaddr(),
		"direction", stream.Stat().Direction,
		"requested", len(m.Locator),
		"returned", len(bd.Locator),
		"bytesTotal", bytesTotal,
		"truncated", truncated,
		"responseSize", encodedSize,
		"maxChunk", s.p2p.Encoding().GetMaxChunkSize())
	e := s.EncodeResponseMsg(stream, bd)
	if e != nil {
		log.Debug("[DEBUG-blockdata] inbound getblockdatas response encode failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"err", e.String())
	} else {
		log.Debug("[DEBUG-blockdata] inbound getblockdatas response encoded",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"returned", len(bd.Locator),
			"responseSize", encodedSize)
	}
	return e
}

func (s *Sync) getMerkleBlockDataHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {

	m, ok := msg.(*pb.MerkleBlockRequest)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.Hash")
		return ErrMessage(err)
	}
	hashes, err := changePBHashsToHashs(m.Hashes)
	if err != nil {
		return ErrMessage(fmt.Errorf("invalid merkle block hash: %w", err))
	}
	// filter := pe.Filter()
	// Do not send a response if the peer doesn't have a filter loaded.
	// if !filter.IsLoaded() {
	// 	log.Warn("filter not loaded!")
	// 	return nil
	// }
	bds := []*pb.MerkleBlock{}
	bd := &pb.MerkleBlockResponse{Data: bds}
	for _, blockHash := range hashes {
		block, err := s.p2p.BlockChain().FetchBlockByHash(blockHash)
		if err != nil {
			return ErrMessage(err)
		}
		// Generate a merkle block by filtering the requested block according
		// to the filter for the peer.
		// merkle, _ := bloom.NewMerkleBlock(block, filter)
		// Finally, send any matched transactions.
		// pbbd := pb.MerkleBlock{Header: merkle.Header.BlockData(),
		// 	Transactions: uint64(merkle.Transactions),
		// 	Hashes:       changeHashsToPBHashs(merkle.Hashes),
		// 	Flags:        merkle.Flags,
		// }
		pbbd := pb.MerkleBlock{
			Header:       block.Block().Header.BlockData(),
			Transactions: uint64(len(block.Transactions())),
			Hashes:       nil, // No Bloom filter hashes
			Flags:        nil, // No Bloom filter flags
		}
		bd.Data = append(bd.Data, &pbbd)
	}
	e := s.EncodeResponseMsg(stream, bd)
	if e != nil {
		return e
	}
	return nil
}

func (ps *PeerSync) processGetBlockDatas(pe *peers.Peer, blocks []*ethcommon.Hash) *ProcessResult {
	return newPeerBlockIntake(ps, pe, blocks).process()
}

func (ps *PeerSync) processGetMerkleBlockDatas(pe *peers.Peer, blocks []*ethcommon.Hash) error {
	if !ps.isSyncPeer(pe) || !pe.IsConnected() {
		err := fmt.Errorf("no sync peer")
		log.Trace("processGetMerkleBlockDatas unavailable", "error", lazyTraceValue(func() any { return err.Error() }))
		return err
	}
	// filter := pe.Filter()
	// Do not send a response if the peer doesn't have a filter loaded.
	// if !filter.IsLoaded() {
	// err := fmt.Errorf("filter not loaded")
	// log.Trace(err.Error())
	// return nil
	// }

	blocksReady := []*ethcommon.Hash{}

	for _, b := range blocks {
		if ps.sy.p2p.BlockChain().HaveBlock(b) {
			continue
		}
		blocksReady = append(blocksReady, b)
	}
	if len(blocksReady) <= 0 {
		return nil
	}

	bd, err := ps.sy.sendGetMerkleBlockDataRequest(ps.sy.p2p.Context(), pe.GetID(), &pb.MerkleBlockRequest{Hashes: changeHashsToPBHashs(blocksReady)})
	if err != nil {
		log.Warn(fmt.Sprintf("sendGetMerkleBlockDataRequest send:%v", err))
		return err
	}
	log.Debug(fmt.Sprintf("sendGetMerkleBlockDataRequest:%d", len(bd.Data)))
	return nil
}

// handleGetData is invoked when a peer receives a getdata Blockdag message and
// is used to deliver block and transaction information.
func (ps *PeerSync) OnGetData(sp *peers.Peer, invList []*pb.InvVect) error {
	for i, iv := range invList {
		if iv == nil {
			return fmt.Errorf("invalid getdata inventory at index %d: missing vector", i)
		}
		if _, err := changePBHashToHash(iv.Hash); err != nil {
			return fmt.Errorf("invalid getdata inventory hash at index %d: %w", i, err)
		}
	}
	txs := make([]*pb.Hash, 0)
	blocks := make([]*pb.Hash, 0)
	merkleBlocks := make([]*pb.Hash, 0)
	for _, iv := range invList {
		log.Trace("OnGetData", "type", InvType(iv.Type), "hash", iv.Hash)
		switch InvType(iv.Type) {
		case InvTypeTx:
			txs = append(txs, iv.Hash)
		case InvTypeBlock:
			blocks = append(blocks, iv.Hash)
		case InvTypeFilteredBlock:
			merkleBlocks = append(merkleBlocks, iv.Hash)
		default:
			log.Warn(fmt.Sprintf("Unknown type in inventory request %d",
				iv.Type))
			continue
		}
	}
	if len(txs) > 0 {
		hashes, err := changePBHashsToHashs(txs)
		if err != nil {
			return fmt.Errorf("invalid getdata transaction hash: %w", err)
		}
		err = ps.processGetTxs(sp, hashes)
		if err != nil {
			log.Info("processGetTxs Error", "err", err.Error())
			return err
		}
	}
	if len(blocks) > 0 {
		hashes, err := changePBHashsToHashs(blocks)
		if err != nil {
			return fmt.Errorf("invalid getdata block hash: %w", err)
		}
		ps.processGetBlockDatas(sp, hashes)
	}
	if len(merkleBlocks) > 0 {
		hashes, err := changePBHashsToHashs(merkleBlocks)
		if err != nil {
			return fmt.Errorf("invalid getdata merkle block hash: %w", err)
		}
		err = ps.processGetMerkleBlockDatas(sp, hashes)
		if err != nil {
			log.Info("processGetBlockDatas Error", "err", err.Error())
			return err
		}
	}
	return nil
}

func (s *Sync) sendBroadcastBlockRequest(stream network.Stream, pe *peers.Peer) *common.Error {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("get block date request rsp")
		return e
	}
	msg := new(uint64)
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return common.NewError(common.ErrStreamRead, err)
	}
	if *msg != 0 {
		log.Trace("broadcast block is added")
	}
	return nil
}

func (s *Sync) broadcastBlockHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	//This function is called when one peer is brodcasting the block to other peers
	//p2p -> service.go -> BrodcastBlock
	m, ok := msg.(*pb.BroadcastBlock)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.Hash")
		return ErrMessage(err)
	}
	block, err := types.NewBlockFromBytes(m.Block.BlockBytes)
	if err != nil {
		return ErrMessage(err)
	}
	ret := uint64(0)
	blockHash := block.Hash()
	if s.p2p.BlockChain().Blockdag().HasBlock(blockHash) ||
		s.p2p.BlockChain().IsOrphan(blockHash) ||
		s.p2p.BlockChain().HasBlockInDB(blockHash) {
		return s.EncodeResponseMsg(stream, ret)
	}
	for _, ph := range block.Block().Parents {
		if !s.p2p.BlockChain().Blockdag().HasBlock(ph) {
			return s.EncodeResponseMsg(stream, ret)
		}
	}
	peid := pe.GetID()
	go func() {
		if s.p2p.BlockChain().Blockdag().HasBlock(blockHash) {
			return
		}
		if err := s.peerSync.validatePeerEVMCheckpoint(pe); err != nil {
			s.peerSync.rejectPeerForEVMCheckpoint(pe, err)
			return
		}
		s.peerSync.recordPeerReorgDebugStatus(pe)
		_, _, err = s.p2p.BlockChain().ProcessBlock(block, blockchain.BFBroadcast, &peid)
		if err != nil {
			if blockchain.IsLocalEVMStateUnavailable(err) {
				log.Warn("Deferring broadcast block for local EVM state recovery", "block", block.Hash(), "err", err)
				return
			}
			if isEVMCheckpointProcessError(err) {
				s.peerSync.rejectPeerForEVMCheckpoint(pe, err)
				return
			}
			if isEVMStateGuardProcessError(err) {
				s.peerSync.rejectPeerForEVMStateGuard(pe, err)
				return
			}
			log.Trace("Failed to process block", "hash", lazyTraceValue(func() any { return block.Hash() }), "error", err)
			return
		}
		s.peerSync.clearPeerEVMStateGuardFailures(pe)
		if err := s.peerSync.validatePeerEVMCheckpoint(pe); err != nil {
			s.peerSync.rejectPeerForEVMCheckpoint(pe, err)
		}
	}()
	ret = 1
	return s.EncodeResponseMsg(stream, ret)
}
