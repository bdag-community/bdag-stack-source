package synch

import (
	"container/list"
	"errors"
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

type peerBlockIntake struct {
	ps            *PeerSync
	pe            *peers.Peer
	requested     []*ethcommon.Hash
	blocksReady   []*ethcommon.Hash
	blockDatas    *list.List
	blockDataByID map[ethcommon.Hash]*BlockData
	inFlight      []ethcommon.Hash
	index         int
	packageNumber int
}

type peerBlockPrepareStats struct {
	requested     int
	nilHash       int
	duplicate     int
	alreadyInDAG  int
	reserveFailed int
	reserved      int
	orphanCached  int
	orphanMissing int
	dbHit         int
	dbFetchErr    int
	missing       int
	interrupted   bool
}

func newPeerBlockIntake(ps *PeerSync, pe *peers.Peer, requested []*ethcommon.Hash) *peerBlockIntake {
	return &peerBlockIntake{
		ps:            ps,
		pe:            pe,
		requested:     requested,
		blockDatas:    list.New(),
		blockDataByID: make(map[ethcommon.Hash]*BlockData),
	}
}

func (in *peerBlockIntake) process() *ProcessResult {
	isSyncPeer := in.ps.isSyncPeer(in.pe)
	connected := in.pe.IsConnected()
	log.Debug("[DEBUG-blockdata] intake start",
		"peer", in.pe.GetID(),
		"requested", len(in.requested),
		"requestedFirst", debugHashAt(in.requested, 0),
		"requestedLast", debugHashAt(in.requested, len(in.requested)-1),
		"isSyncPeer", isSyncPeer,
		"connected", connected,
		"syncPoint", debugHash(in.pe.SyncPoint()),
		"peerGraphState", debugGraphState(in.pe.GraphState()),
		"localGraphState", debugGraphStatePB(in.ps.sy.getGraphState()),
		"processID", in.ps.processID)
	if !isSyncPeer {
		err := fmt.Errorf("no sync peer:%v", in.pe.GetID())
		log.Trace(err.Error(), "processID", in.ps.processID)
		log.Debug("[DEBUG-blockdata] intake abort unavailable peer",
			"peer", in.pe.GetID(),
			"requested", len(in.requested),
			"isSyncPeer", isSyncPeer,
			"connected", connected,
			"syncPoint", debugHash(in.pe.SyncPoint()),
			"peerGraphState", debugGraphState(in.pe.GraphState()),
			"processID", in.ps.processID)
		return nil
	}
	if !connected {
		log.Debug("[DEBUG-blockdata] intake continuing with disconnected peer",
			"peer", in.pe.GetID(),
			"requested", len(in.requested),
			"syncPoint", debugHash(in.pe.SyncPoint()),
			"peerGraphState", debugGraphState(in.pe.GraphState()),
			"processID", in.ps.processID)
	}

	stats := in.prepare()
	defer in.ps.releaseSyncBlocks(in.inFlight)
	blockDatasLen := in.blockDatas.Len()
	log.Debug("[DEBUG-blockdata] intake prepared",
		"peer", in.pe.GetID(),
		"requested", stats.requested,
		"nilHash", stats.nilHash,
		"duplicate", stats.duplicate,
		"alreadyInDAG", stats.alreadyInDAG,
		"reserveFailed", stats.reserveFailed,
		"reserved", stats.reserved,
		"orphanCached", stats.orphanCached,
		"orphanMissing", stats.orphanMissing,
		"dbHit", stats.dbHit,
		"dbFetchErr", stats.dbFetchErr,
		"missing", stats.missing,
		"interrupted", stats.interrupted,
		"blockDatas", blockDatasLen,
		"blocksReady", len(in.blocksReady),
		"inFlight", len(in.inFlight),
		"processID", in.ps.processID)
	if blockDatasLen <= 0 {
		log.Debug("[DEBUG-blockdata] intake result",
			"peer", in.pe.GetID(),
			"action", "continue",
			"reason", "no block data after prepare",
			"add", 0,
			"orphan", false,
			"blockDatas", blockDatasLen,
			"blocksReady", len(in.blocksReady),
			"processID", in.ps.processID)
		return processResult(ProcessResultActionContinue, "no block data after prepare", false, 0)
	}

	behaviorFlags := blockchain.BFP2PAdd
	add := 0
	hasOrphan := false
	processedOrphans := 0
	duplicateOrphans := 0
	duplicateKnown := 0
	noBytes := 0
	processFailures := 0
	i := 0
	for in.blockDatas.Len() > 0 {
		i++
		be := in.blockDatas.Front()
		b := be.Value.(*BlockData)
		in.blockDatas.Remove(be)

		if in.ps.IsInterrupt() || !in.ps.IsRunning() {
			log.Debug("[DEBUG-blockdata] intake abort interrupted",
				"peer", in.pe.GetID(),
				"add", add,
				"processedOrphans", processedOrphans,
				"duplicateOrphans", duplicateOrphans,
				"duplicateKnown", duplicateKnown,
				"noBytes", noBytes,
				"processFailures", processFailures,
				"remaining", in.blockDatas.Len(),
				"processID", in.ps.processID)
			return nil
		}
		block := b.Block
		if block == nil {
			if !in.fetchMissingBlocks() {
				log.Debug("[DEBUG-blockdata] intake result",
					"peer", in.pe.GetID(),
					"action", "tryagain",
					"reason", "fetch missing blocks failed",
					"add", add,
					"processedOrphans", processedOrphans,
					"duplicateOrphans", duplicateOrphans,
					"duplicateKnown", duplicateKnown,
					"noBytes", noBytes,
					"processFailures", processFailures,
					"blockDatas", blockDatasLen,
					"processID", in.ps.processID)
				return processResult(ProcessResultActionTryAgain, "fetch missing blocks failed", hasOrphan, add)
			}
			if b.Block == nil {
				noBytes++
				log.Trace(fmt.Sprintf("No block bytes:%d : %s", i, b.Hash.String()), "processID", in.ps.processID)
				continue
			}
			block = b.Block
		}

		pid := in.pe.GetID()
		if err := in.ps.validatePeerEVMCheckpoint(in.pe); err != nil {
			log.Debug("[DEBUG-blockdata] intake result",
				"peer", in.pe.GetID(),
				"action", "tryagain",
				"reason", "evm checkpoint rejected before process",
				"err", err,
				"add", add,
				"orphan", hasOrphan,
				"processID", in.ps.processID)
			in.ps.rejectPeerForEVMCheckpoint(in.pe, err)
			return processResult(ProcessResultActionTryAgain, "evm checkpoint rejected before process", hasOrphan, add).withErr(err)
		}
		in.ps.recordPeerReorgDebugStatus(in.pe)
		_, isOrphan, err := in.ps.sy.p2p.BlockChain().ProcessBlock(block, behaviorFlags, &pid)
		if err != nil {
			if blockchain.IsLocalEVMStateUnavailable(err) {
				log.Warn("Deferring peer block for local EVM state recovery", "block", block.Hash(), "err", err)
				return processResult(ProcessResultActionTryAgain, "local EVM state unavailable", hasOrphan, add).withBlock(block.Hash().String()).withErr(err)
			}
			if isEVMCheckpointProcessError(err) {
				log.Debug("[DEBUG-blockdata] intake result",
					"peer", in.pe.GetID(),
					"action", "tryagain",
					"reason", "evm checkpoint rejected by process block",
					"block", block.Hash().String(),
					"err", err,
					"add", add,
					"orphan", hasOrphan,
					"processID", in.ps.processID)
				in.ps.rejectPeerForEVMCheckpoint(in.pe, err)
				return processResult(ProcessResultActionTryAgain, "evm checkpoint rejected by process block", hasOrphan, add).withBlock(block.Hash().String()).withErr(err)
			}
			if isEVMStateGuardProcessError(err) {
				log.Debug("[DEBUG-blockdata] intake result",
					"peer", in.pe.GetID(),
					"action", "tryagain",
					"reason", "evm state commitment rejected by process block",
					"block", block.Hash().String(),
					"err", err,
					"add", add,
					"orphan", hasOrphan,
					"processID", in.ps.processID)
				in.ps.rejectPeerForEVMStateGuard(in.pe, err)
				return processResult(ProcessResultActionTryAgain, "evm state commitment rejected by process block", hasOrphan, add).withBlock(block.Hash().String()).withErr(err)
			}
			if isDuplicateBlockProcessError(err) {
				if isOrphan {
					hasOrphan = true
					duplicateOrphans++
					log.Debug(fmt.Sprintf("Skipping duplicate orphan block during sync:hash=%s", block.Hash()), "processID", in.ps.processID)
					continue
				}
				duplicateKnown++
				log.Trace(fmt.Sprintf("Skipping duplicate known block during sync:hash=%s", block.Hash()), "processID", in.ps.processID)
				continue
			}
			processFailures++
			log.Debug("[DEBUG-blockdata] process block failed",
				"peer", in.pe.GetID(),
				"block", block.Hash().String(),
				"isOrphan", isOrphan,
				"err", err,
				"processID", in.ps.processID)
			log.Error(fmt.Sprintf("Failed to process block:hash=%s err=%s", block.Hash(), err), "processID", in.ps.processID)
			continue
		}
		if isOrphan {
			hasOrphan = true
			processedOrphans++
			continue
		}
		in.ps.clearPeerEVMStateGuardFailures(in.pe)
		if err := in.ps.validatePeerEVMCheckpoint(in.pe); err != nil {
			log.Debug("[DEBUG-blockdata] intake result",
				"peer", in.pe.GetID(),
				"action", "tryagain",
				"reason", "evm checkpoint rejected after process",
				"err", err,
				"add", add,
				"orphan", hasOrphan,
				"processID", in.ps.processID)
			in.ps.rejectPeerForEVMCheckpoint(in.pe, err)
			return processResult(ProcessResultActionTryAgain, "evm checkpoint rejected after process", hasOrphan, add).withErr(err)
		}
		in.ps.sy.p2p.RegainMempool()

		add++
		in.ps.lastSync = time.Now()
	}
	log.Debug(fmt.Sprintf("getBlockDatas:%d/%d", add, blockDatasLen), "processID", in.ps.processID)

	if add > 0 {
		log.Debug("[DEBUG-blockdata] intake result",
			"peer", in.pe.GetID(),
			"action", "continue",
			"reason", "blocks added",
			"add", add,
			"orphan", hasOrphan,
			"processedOrphans", processedOrphans,
			"duplicateOrphans", duplicateOrphans,
			"duplicateKnown", duplicateKnown,
			"noBytes", noBytes,
			"processFailures", processFailures,
			"blockDatas", blockDatasLen,
			"processID", in.ps.processID)
		return processResult(ProcessResultActionContinue, "blocks added", hasOrphan, add)
	}
	if duplicateOrphans > 0 {
		if err := in.ps.sy.p2p.BlockChain().RefreshOrphans(); err != nil {
			log.Trace(fmt.Sprintf("Failed to refresh duplicate orphans during sync, err=%v", err), "processID", in.ps.processID)
		}
		log.Debug(fmt.Sprintf("getBlockDatas duplicate-orphan:%d/%d duplicate-known:%d", duplicateOrphans, blockDatasLen, duplicateKnown), "processID", in.ps.processID)
		log.Debug("[DEBUG-blockdata] intake result",
			"peer", in.pe.GetID(),
			"action", "continue",
			"reason", "duplicate orphan only",
			"add", 0,
			"orphan", true,
			"processedOrphans", processedOrphans,
			"duplicateOrphans", duplicateOrphans,
			"duplicateKnown", duplicateKnown,
			"noBytes", noBytes,
			"processFailures", processFailures,
			"blockDatas", blockDatasLen,
			"processID", in.ps.processID)
		return processResult(ProcessResultActionContinue, "duplicate orphan only", true, 0)
	}
	if duplicateKnown > 0 {
		log.Debug(fmt.Sprintf("getBlockDatas duplicate-known:%d/%d", duplicateKnown, blockDatasLen), "processID", in.ps.processID)
		log.Debug("[DEBUG-blockdata] intake result",
			"peer", in.pe.GetID(),
			"action", "continue",
			"reason", "duplicate known only",
			"add", 0,
			"orphan", hasOrphan,
			"processedOrphans", processedOrphans,
			"duplicateOrphans", duplicateOrphans,
			"duplicateKnown", duplicateKnown,
			"noBytes", noBytes,
			"processFailures", processFailures,
			"blockDatas", blockDatasLen,
			"processID", in.ps.processID)
		return processResult(ProcessResultActionContinue, "duplicate known only", hasOrphan, 0)
	}
	err := fmt.Errorf("no get blocks")
	log.Debug(err.Error(), "processID", in.ps.processID)
	log.Debug("[DEBUG-blockdata] intake result",
		"peer", in.pe.GetID(),
		"action", "tryagain",
		"reason", err.Error(),
		"add", 0,
		"orphan", hasOrphan,
		"processedOrphans", processedOrphans,
		"duplicateOrphans", duplicateOrphans,
		"duplicateKnown", duplicateKnown,
		"noBytes", noBytes,
		"processFailures", processFailures,
		"blockDatas", blockDatasLen,
		"processID", in.ps.processID)
	return processResult(ProcessResultActionTryAgain, err.Error(), hasOrphan, 0).withErr(err)
}

func isDuplicateBlockProcessError(err error) bool {
	if err == nil {
		return false
	}
	var ruleErr blockchain.RuleError
	return errors.As(err, &ruleErr) && ruleErr.ErrorCode == blockchain.ErrDuplicateBlock
}

func isEVMCheckpointProcessError(err error) bool {
	return isRuleErrorProcessError(err, blockchain.ErrEVMCheckpointMismatch)
}

func isEVMStateGuardProcessError(err error) bool {
	return isRuleErrorProcessError(err, blockchain.ErrEVMStateGuard)
}

func isRuleErrorProcessError(err error, code blockchain.ErrorCode) bool {
	if err == nil {
		return false
	}
	var ruleErr blockchain.RuleError
	return errors.As(err, &ruleErr) && ruleErr.ErrorCode == code
}

func (in *peerBlockIntake) prepare() peerBlockPrepareStats {
	stats := peerBlockPrepareStats{requested: len(in.requested)}
	seen := make(map[ethcommon.Hash]struct{}, len(in.requested))
	for _, b := range in.requested {
		if in.ps.IsInterrupt() {
			stats.interrupted = true
			return stats
		}
		if b == nil {
			stats.nilHash++
			continue
		}
		bh := *b
		if _, ok := seen[bh]; ok {
			stats.duplicate++
			continue
		}
		seen[bh] = struct{}{}
		if in.ps.sy.p2p.BlockChain().Blockdag().HasBlock(b) {
			stats.alreadyInDAG++
			continue
		}
		if !in.ps.reserveSyncBlock(bh) {
			stats.reserveFailed++
			continue
		}
		stats.reserved++
		in.inFlight = append(in.inFlight, bh)
		blkd := &BlockData{Hash: b}
		in.blockDataByID[*blkd.Hash] = blkd
		in.blockDatas.PushBack(blkd)

		if in.ps.sy.p2p.BlockChain().IsOrphan(b) {
			ob := in.ps.sy.p2p.BlockChain().GetOrphan(b)
			if ob != nil {
				stats.orphanCached++
				blkd.Block = ob
				continue
			}
			stats.orphanMissing++
		} else if in.ps.sy.p2p.BlockChain().HasBlockInDB(b) {
			sb, err := in.ps.sy.p2p.BlockChain().FetchBlockByHash(b)
			if err == nil {
				stats.dbHit++
				blkd.Block = sb
				continue
			}
			stats.dbFetchErr++
		} else {
			stats.missing++
		}
		in.blocksReady = append(in.blocksReady, b)
	}
	return stats
}

func (in *peerBlockIntake) fetchMissingBlocks() bool {
	readys := len(in.blocksReady)
	if readys <= 0 || in.index >= readys {
		log.Debug("[DEBUG-blockdata] getblockdatas outbound skipped",
			"peer", in.pe.GetID(),
			"blocksReady", readys,
			"index", in.index,
			"processID", in.ps.processID)
		return true
	}
	in.packageNumber++
	oldIndex := in.index
	sendBlocks := in.blocksReady[in.index:]
	log.Trace(fmt.Sprintf("processGetBlockDatas::sendGetBlockDataRequest peer=%v, blocks=%d [%s -> %s] ", in.pe.GetID(), len(sendBlocks), sendBlocks[0], sendBlocks[len(sendBlocks)-1]), "processID", in.ps.processID, "package number", in.packageNumber)
	req := &pb.GetBlockDatas{Locator: changeHashsToPBHashs(sendBlocks)}
	log.Debug("[DEBUG-blockdata] getblockdatas outbound request",
		"peer", in.pe.GetID(),
		"blocks", len(sendBlocks),
		"first", debugHashAt(sendBlocks, 0),
		"last", debugHashAt(sendBlocks, len(sendBlocks)-1),
		"index", in.index,
		"package", in.packageNumber,
		"requestSize", in.ps.sy.p2p.Encoding().GetSize(req),
		"processID", in.ps.processID)
	ret, err := in.ps.sy.Send(in.pe, RPCGetBlockDatas, req)
	if err != nil {
		log.Warn(fmt.Sprintf("getBlocks send:%v", err), "processID", in.ps.processID)
		log.Debug("[DEBUG-blockdata] getblockdatas outbound failed",
			"peer", in.pe.GetID(),
			"blocks", len(sendBlocks),
			"first", debugHashAt(sendBlocks, 0),
			"last", debugHashAt(sendBlocks, len(sendBlocks)-1),
			"index", in.index,
			"package", in.packageNumber,
			"err", err,
			"processID", in.ps.processID)
		if in.index == 0 {
			in.index++
			return false
		}
		in.index = readys
		return true
	}
	bd := ret.(*pb.BlockDatas)
	log.Trace(fmt.Sprintf("Received:Locator=%d", len(bd.Locator)), "processID", in.ps.processID)

	var lastBlockHash *ethcommon.Hash
	matched := 0
	unmatched := 0
	parseErr := 0
	for i, b := range bd.Locator {
		block, err := types.NewBlockFromBytes(b.BlockBytes)
		if err != nil {
			parseErr++
			log.Warn(fmt.Sprintf("getBlocks from:%v", err), "processID", in.ps.processID)
			log.Debug("[DEBUG-blockdata] getblockdatas response parse failed",
				"peer", in.pe.GetID(),
				"locatorIndex", i,
				"blockBytes", len(b.BlockBytes),
				"err", err,
				"processID", in.ps.processID)
			break
		}
		bdm, ok := in.blockDataByID[*block.Hash()]
		if ok {
			matched++
			bdm.Block = block
			delete(in.blockDataByID, *block.Hash())
		} else {
			unmatched++
		}
		if i+1 == len(bd.Locator) {
			lastBlockHash = block.Hash()
		}
	}
	if lastBlockHash != nil {
		in.index++
		for i := 0; i < readys; i++ {
			if lastBlockHash.Cmp(*in.blocksReady[i]) == 0 {
				in.index = i + 1
				return true
			}
		}
	} else {
		in.index = readys
	}
	log.Debug("[DEBUG-blockdata] getblockdatas outbound response",
		"peer", in.pe.GetID(),
		"requested", len(sendBlocks),
		"responseBlocks", len(bd.Locator),
		"matched", matched,
		"unmatched", unmatched,
		"parseErr", parseErr,
		"lastBlockHash", debugHash(lastBlockHash),
		"oldIndex", oldIndex,
		"newIndex", in.index,
		"blocksReady", readys,
		"responseSize", in.ps.sy.p2p.Encoding().GetSize(bd),
		"processID", in.ps.processID)
	return true
}
