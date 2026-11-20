package blockchain

import (
	"bytes"
	"container/list"
	"context"
	"encoding/gob"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/bevm/eth"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/core/serialization"
	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow/difficultymanager"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"golang.org/x/crypto/sha3"

	"github.com/BlockdagNetworkLabs/bdag/database/common"
	"github.com/BlockdagNetworkLabs/bdag/engine/txscript"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/progresslog"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/schollz/progressbar/v3"
)

const (

	// maxOrphanBlocks is the maximum number of orphan blocks that can be
	// queued.
	MaxOrphanBlocks = 500
	// minMemoryNodes is the minimum number of consecutive nodes needed
	// in memory in order to perform all necessary validation.  It is used
	// to determine when it's safe to prune nodes from memory without
	// causing constant dynamic reloading.  This value should be larger than
	// that for minMemoryStakeNodes.
	minMemoryNodes = 2880
)

// BlockChain provides functions such as rejecting duplicate blocks, ensuring
// blocks follow all rules, orphan handling, checkpoint handling, and best chain
// selection with reorganization.
type BlockChain struct {
	service.Service

	params *params.Params

	// The following fields are set when the instance is created and can't
	// be changed afterwards, so there is no need to protect them with a
	// separate mutex.
	checkpointsByLayer map[uint64]*params.Checkpoint

	dbInfo       *common.DatabaseInfo
	timeSource   model.MedianTimeSource
	events       *event.Feed
	sigCache     *txscript.SigCache
	indexManager model.IndexManager

	// subsidyCache is the cache that provides quick lookup of subsidy
	// values.
	subsidyCache *SubsidyCache

	// chainLock protects concurrent access to the vast majority of the
	// fields in this struct below this point.
	//
	// Lock ordering: chainLock is always the OUTER lock relative to
	// evmChainLock (below) and blockdag.BDAG.stateLock. maybeAcceptBlock
	// holds chainLock for the whole block-accept operation and, from
	// inside that, calls into both b.evmChainLock (see connectDagChain)
	// and b.bd's methods (which take stateLock internally). Never acquire
	// chainLock/ChainRLock from code that already holds stateLock or
	// evmChainLock -- that inverts this order and risks deadlock. This is
	// exactly why blockdag.DAGSync.CalcSyncBlocks takes an isCurrent bool
	// parameter instead of calling BlockChain.IsCurrent() itself (see
	// 26718da): CalcSyncBlocks runs under stateLock, and IsCurrent() needs
	// chainLock, so the caller must resolve it first.
	chainLock sync.RWMutex

	// These fields are configuration parameters that can be toggled at
	// runtime.  They are protected by the chain lock.
	noVerify      bool
	noCheckpoints bool

	// These fields are related to handling of orphan blocks.  They are
	// protected by a combination of the chain lock and the orphan lock.
	orphanLock   sync.RWMutex
	orphans      map[ethcommon.Hash]*orphanBlock
	oldestOrphan *orphanBlock

	// These fields are related to checkpoint handling.  They are protected
	// by the chain lock.
	nextCheckpoint *params.Checkpoint
	checkpointNode blockdag.IBlock

	// The following fields are used for headers-first mode.
	headersFirstMode bool
	headerList       *list.List
	startHeader      *list.Element

	// The state is used as a fairly efficient way to cache information
	// about the current best chain state that is returned to callers when
	// requested.  It operates on the principle of MVCC such that any time a
	// new block becomes the best block, the state pointer is replaced with
	// a new struct and the old state is left untouched.  In this way,
	// multiple callers can be pointing to different best chain states.
	// This is acceptable for most callers because the state is only being
	// queried at a specific point in time.
	//
	// In addition, some of the fields are stored in the database so the
	// chain state can be quickly reconstructed on load.
	stateLock     sync.RWMutex
	stateSnapshot *BestState

	// pruner is the automatic pruner for block nodes and stake nodes,
	// so that the memory may be restored by the garbage collector if
	// it is unlikely to be referenced in the future.
	pruner *chainPruner

	//block dag
	bd *blockdag.BDAG

	// cache notification
	CacheNotifications []*Notification

	notificationsLock sync.RWMutex
	notifications     []NotificationCallback

	// The ID of token state tip for the chain.
	TokenTipID uint32

	// Acct model.Acct

	consensus model.Consensus

	progressLogger *progresslog.BlockProgressLogger

	evmGenesisHeader *gethtypes.Header

	msgChan chan *processMsg
	wg      sync.WaitGroup
	quit    chan struct{}

	// evmChainLock serializes geth head recovery, template construction, and
	// block insertion so the BDAG and EVM views cannot race each other.
	//
	// Lock ordering: always the INNER lock relative to chainLock (see
	// chainLock's doc above) -- only acquire evmChainLock from code that
	// either already holds chainLock or holds neither, never the reverse.
	evmChainLock     evmMutationLock
	committedEVMHead atomic.Pointer[committedNativeEVMHead]
	localEVMRecovery atomic.Pointer[localEVMRecoveryHolds]
	bdagChain        *bdag.BdagChain
	evmCatchupWake   chan struct{}
	// evmNativeCatchupBatchOrders bounds how many DAG orders each EVM-native
	// catch-up batch replays while holding chainLock exclusively (see
	// runEVMNativeCatchupBatch). Configurable via config.EVMNativeCatchupBatchOrders
	// since the right value trades off against how far the EVM-native view
	// can fall behind under a large sync backlog -- not something to guess.
	evmNativeCatchupBatchOrders uint64
	evmPeerCheckpoints          sync.Map
	peerReorgStatuses           sync.Map
	evmStateGuard               *evmStateGuard
	difficultyManager           model.DifficultyManager

	processQueueMap sync.Map

	selfAdd atomic.Int64

	upgradeCacheMu   sync.Mutex
	upgradeTipID     uint
	upgradeWindows   map[string]upgradeWindowCache
	upgradeRebuildMu sync.Mutex

	no24hCheck bool
}

// evmMutationLock exposes an even generation only while no EVM mutation is in
// progress. Readers can reject work that crossed a mutation without taking a
// lock in the reverse order from chain acceptance.
type evmMutationLock struct {
	sync.Mutex
	generation atomic.Uint64
}

func (l *evmMutationLock) Lock() {
	l.Mutex.Lock()
	l.generation.Add(1)
}

func (l *evmMutationLock) TryLock() bool {
	if !l.Mutex.TryLock() {
		return false
	}
	l.generation.Add(1)
	return true
}

func (l *evmMutationLock) Unlock() {
	l.generation.Add(1)
	l.Mutex.Unlock()
}

func (l *evmMutationLock) Generation() uint64 {
	return l.generation.Load()
}

type blockApplyRecoveryDB interface {
	PendingBlockApplyRecovery() (string, bool, error)
	CompleteBlockApplyRecovery() error
}

type blockApplyRecoveryClassifier interface {
	ClassifyBlockApplyRecovery(shutdown.DatasetState) (string, shutdown.MarkerClassification, error)
}

func (b *BlockChain) Init() error {
	// Initialize the chain state from the passed database.  When the db
	// does not yet contain any chain state, both it and the chain state
	// will be initialized to contain only the genesis block.
	if err := b.initChainState(); err != nil {
		return err
	}
	b.publishCommittedEVMHead()

	upgrades.RegisterPersist(b.persistUpgradeState)
	if err := b.loadUpgradeStates(); err != nil {
		return err
	}
	b.MaybeRebuildUpgradesFromHeaders()
	// Recalculate upgrade state from the current chain tip.
	tip := b.bd.GetMainChainTip()
	if tip != nil {
		b.evaluateUpgrades(uint64(tip.GetLayer()))
	}

	// Initialize and catch up all of the currently active optional indexes
	// as needed.
	err := b.indexManager.Init()
	if err != nil {
		return err
	}
	b.pruner = newChainPruner(b)

	err = b.initCheckPoints()
	if err != nil {
		return err
	}

	//
	log.Info(fmt.Sprintf("DAG Type:%s", b.bd.GetName()))
	log.Info("Blockchain database version", "chain", b.dbInfo.Version(), "compression", b.dbInfo.CompVer(),
		"index", b.dbInfo.BidxVer())

	tips := b.bd.GetTipsList()
	log.Info(fmt.Sprintf("Chain state:totaltx=%d tipsNum=%d mainOrder=%d total=%d", b.BestSnapshot().TotalTxns, len(tips), b.bd.GetMainChainTip().GetOrder(), b.bd.GetBlockTotal()))

	for _, v := range tips {
		log.Info(fmt.Sprintf("hash=%s,order=%s,height=%d", v.GetHash(), blockdag.GetOrderLogStr(v.GetOrder()), v.GetHeight()))
	}

	b.difficultyManager = difficultymanager.NewDiffManager(b.Consensus(), b.params)
	return nil
}

// initChainState attempts to load and initialize the chain state from the
// database.  When the db does not yet contain any chain state, both it and the
// chain state are initialized to the genesis block.
func (b *BlockChain) initChainState() error {
	recoveryMarker, markerPresent, err := b.pendingBlockApplyRecovery()
	if err != nil {
		return err
	}
	recoveryPending := false
	wrapRecoveryErr := func(err error) error {
		if err == nil || !recoveryPending {
			return err
		}
		return fmt.Errorf("unclean shutdown during block connect (%s): chain database validation failed: %w; restore a recent chain snapshot or run --cleanup only if you intend to resync from scratch", recoveryMarker, err)
	}
	completeRecovery := func() error {
		if !recoveryPending {
			return nil
		}
		if err := b.completeBlockApplyRecovery(); err != nil {
			return fmt.Errorf("chain database validated after unclean shutdown during block connect (%s), but failed to clear recovery marker: %w", recoveryMarker, err)
		}
		return nil
	}
	classifyRecovery := func() error {
		recoveryClassifier, ok := b.DB().(blockApplyRecoveryClassifier)
		if !ok {
			recoveryPending = markerPresent
			return nil
		}
		current := shutdown.DatasetState{}
		if mainTip := b.bd.GetMainChainTip(); mainTip != nil {
			current.NativeOrder = uint64(mainTip.GetOrder())
			current.NativeHash = mainTip.GetHash().String()
			if tipState := mainTip.GetState(); tipState != nil {
				current.EVMNumber = tipState.GetEVMNumber()
				current.EVMHash = tipState.GetEVMHash().String()
				current.EVMRoot = tipState.GetEVMRoot().String()
			}
		}
		if b.bdagChain != nil {
			if header := b.bdagChain.GetCurHeader(); header != nil {
				if header.Number != nil {
					current.EVMNumber = header.Number.Uint64()
				}
				current.EVMHash = header.Hash().String()
				current.EVMRoot = header.Root.String()
			}
		}
		description, classification, err := recoveryClassifier.ClassifyBlockApplyRecovery(current)
		if err != nil {
			return err
		}
		recoveryMarker = description
		recoveryPending = classification == shutdown.MarkerApplicable
		return nil
	}

	// Determine the state of the database.
	var isStateInitialized bool
	dbInfo, err := b.DB().GetInfo()
	if err != nil {
		return wrapRecoveryErr(err)
	}
	// The database bucket for the versioning information is missing.
	if dbInfo != nil {
		// Don't allow downgrades of the blockchain database.
		if dbInfo.Version() > common.CurrentDatabaseVersion {
			return wrapRecoveryErr(fmt.Errorf("the current blockchain database is "+
				"no longer compatible with this version of "+
				"the software (%d > %d)", dbInfo.Version(),
				common.CurrentDatabaseVersion))
		}

		// Don't allow downgrades of the database compression version.
		if dbInfo.CompVer() > serialization.CurrentCompressionVersion {
			return wrapRecoveryErr(fmt.Errorf("the current database compression "+
				"version is no longer compatible with this "+
				"version of the software (%d > %d)",
				dbInfo.CompVer(), serialization.CurrentCompressionVersion))
		}

		// Don't allow downgrades of the block index.
		if dbInfo.BidxVer() > currentBlockIndexVersion {
			return wrapRecoveryErr(fmt.Errorf("the current database block index "+
				"version is no longer compatible with this "+
				"version of the software (%d > %d)",
				dbInfo.BidxVer(), currentBlockIndexVersion))
		}

		b.dbInfo = dbInfo
		isStateInitialized = true
	}

	// Initialize the database if it has not already been done.
	if !isStateInitialized {
		if err := b.createChainState(); err != nil {
			return wrapRecoveryErr(err)
		}
		if err := classifyRecovery(); err != nil {
			return err
		}
		return completeRecovery()
	}

	// Upgrade the database as needed.
	err = b.DB().TryUpgrade(dbInfo, b.consensus.Interrupt())
	if err != nil {
		return wrapRecoveryErr(err)
	}

	var state bestChainState
	// Attempt to load the chain state from the database.
	serializedData, err := b.DB().GetBestChainState()
	if err != nil {
		return wrapRecoveryErr(err)
	}
	if serializedData == nil {
		return wrapRecoveryErr(fmt.Errorf("No chain state data"))
	}
	log.Trace("Serialized chain state: ", "serializedData", fmt.Sprintf("%x", serializedData))
	state, err = DeserializeBestChainState(serializedData)
	if err != nil {
		return wrapRecoveryErr(err)
	}
	log.Trace(fmt.Sprintf("Load chain state:%s %d %d %s %s", state.hash.String(), state.total, state.totalTxns, state.tokenTipHash.String(), state.workSum.Text(16)))

	log.Info("Loading dag ...")
	bidxStart := roughtime.Now()

	err = b.bd.Load(uint(state.total), b.params.GenesisHash)
	if err != nil {
		return wrapRecoveryErr(fmt.Errorf("The dag data was damaged (%s). you can cleanup your block data base by '--cleanup'.", err))
	}
	if b.bd.GetMainChainTip().GetHash().Cmp(state.hash) != 0 {
		return wrapRecoveryErr(fmt.Errorf("The dag main tip %s is not the same. %s", state.hash.String(), b.bd.GetMainChainTip().GetHash().String()))
	}
	log.Info(fmt.Sprintf("Dag loaded:loadTime=%v", roughtime.Since(bidxStart)))
	// Set the best chain view to the stored best state.
	// Load the raw block bytes for the best block.
	mainTip := b.bd.GetMainChainTip()
	mainTipNode := b.GetBlockNode(mainTip)
	if mainTipNode == nil {
		return wrapRecoveryErr(fmt.Errorf("No main tip"))
	}
	block, err := dbFetchBlockByHash(b.DB(), mainTip.GetHash())
	if err != nil {
		return wrapRecoveryErr(err)
	}

	// Initialize the state related to the best block.
	blockSize := uint64(block.Block().SerializeSize())
	numTxns := uint64(len(block.Block().Transactions))

	b.TokenTipID = uint32(b.bd.GetBlockId(&state.tokenTipHash))
	b.stateSnapshot = newBestState(mainTip.GetHash(), mainTipNode.Difficulty(), blockSize, numTxns,
		b.CalcPastMedianTime(mainTip), state.totalTxns, b.bd.GetMainChainTip().GetState().GetWeight(),
		b.bd.GetGraphState(), &state.tokenTipHash, *mainTip.GetState().Root())
	if err := classifyRecovery(); err != nil {
		return err
	}
	return completeRecovery()
}

func (b *BlockChain) pendingBlockApplyRecovery() (string, bool, error) {
	recoveryDB, ok := b.DB().(blockApplyRecoveryDB)
	if !ok {
		return "", false, nil
	}
	return recoveryDB.PendingBlockApplyRecovery()
}

func (b *BlockChain) completeBlockApplyRecovery() error {
	recoveryDB, ok := b.DB().(blockApplyRecoveryDB)
	if !ok {
		return nil
	}
	return recoveryDB.CompleteBlockApplyRecovery()
}

// createChainState initializes both the database and the chain state to the
// genesis block.  This includes creating the necessary buckets and inserting
// the genesis block, so it must only be called on an uninitialized database.
func (b *BlockChain) createChainState() error {
	// Create the initial the database chain state including creating the
	// necessary index buckets and inserting the genesis block.
	err := b.DB().Init()
	if err != nil {
		return err
	}
	// Create a new node from the genesis block and set it as the best node.
	genesisBlock := b.params.GenesisBlock
	header := &genesisBlock.Block().Header
	node := NewBlockNode(genesisBlock)
	_, _, ib, _ := b.bd.AddBlock(node)
	ib.GetState().SetEVM(b.bdagChain.GetCurHeader())

	// Initialize the state related to the best block.  Since it is the
	// genesis block, use its timestamp for the median time.
	numTxns := uint64(len(genesisBlock.Block().Transactions))
	blockSize := uint64(genesisBlock.Block().SerializeSize())
	b.stateSnapshot = newBestState(node.GetHash(), node.Difficulty(), blockSize, numTxns,
		time.Unix(node.GetTimestamp(), 0), numTxns, 0, b.bd.GetGraphState(), node.GetHash(), *ib.GetState().Root())
	b.TokenTipID = 0
	b.dbInfo = common.NewDatabaseInfo(common.CurrentDatabaseVersion, serialization.CurrentCompressionVersion, currentBlockIndexVersion, roughtime.Now())
	err = b.DB().PutInfo(b.dbInfo)
	if err != nil {
		return err
	}
	// Store the current best chain state into the database.
	err = dbPutBestState(b.DB(), b.stateSnapshot, pow.CalcWork(header.Difficulty, header.Pow.GetPowType()))
	if err != nil {
		return err
	}
	// Store the genesis block into the database.
	err = b.DB().PutBlock(genesisBlock)
	if err != nil {
		return err
	}
	// genesis tx index
	err = b.DB().PutTxIdxEntrys(genesisBlock, ib)
	if err != nil {
		return err
	}
	return b.bd.Commit()
}

func (b *BlockChain) Start() error {
	if err := b.Service.Start(); err != nil {
		return err
	}

	if err := b.DB().Snapshot(); err != nil {
		return err
	}

	b.wg.Add(1)
	go b.prepareStartupEVMEnvironment()

	b.wg.Add(1)
	go b.handler()
	b.wg.Add(1)
	go b.evmNativeCatchupLoop()
	return nil
}

func (b *BlockChain) prepareStartupEVMEnvironment() {
	defer b.wg.Done()

	mainTip := b.bd.GetMainChainTip()
	if mainTip == nil || mainTip.GetState() == nil {
		log.Warn("startup evm environment preparation skipped; main tip state unavailable")
		return
	}

	b.evmChainLock.Lock()
	evmHead, err := b.bdagChain.PrepareEnvironment(mainTip.GetState())
	b.evmChainLock.Unlock()
	if err != nil {
		log.Error("prepare startup evm environment failed", "mainTipOrder", mainTip.GetOrder(), "mainTipHash", mainTip.GetHash().String(), "err", err)
		return
	}
	log.Info("prepare evm environment", "mainTipOrder", mainTip.GetOrder(), "mainTipHash", mainTip.GetHash().String(), "hash", evmHead.Hash().String(), "number", evmHead.Number.Uint64(), "root", evmHead.Root.String())
}

func (b *BlockChain) Stop() error {
	log.Info("Try to stop BlockChain")
	close(b.quit)
	b.wg.Wait()
	//
	if err := b.Service.Stop(); err != nil {
		return err
	}
	return nil
}

func (b *BlockChain) IsShutdown() bool {
	return b.Service.IsShutdown() || system.InterruptRequested(b.consensus.Interrupt())
}

// HaveBlock returns whether or not the chain instance has the block represented
// by the passed hash.  This includes checking the various places a block can
// be like part of the main chain, on a side chain, or in the orphan pool.
//
// This function is safe for concurrent access.
func (b *BlockChain) HaveBlock(hash *ethcommon.Hash) bool {
	return b.bd.HasBlock(hash) || b.IsOrphan(hash)
}

func (b *BlockChain) HasBlockInDB(h *ethcommon.Hash) bool {
	return b.DB().HasBlock(h)
}

// IsCurrent returns whether or not the chain believes it is current.  Several
// factors are used to guess, but the key factors that allow the chain to
// believe it is current are:
//   - Latest block height is after the latest checkpoint (if enabled)
//   - Latest block has a timestamp newer than 24 hours ago
//
// This function is safe for concurrent access.
func (b *BlockChain) IsCurrent() bool {
	b.ChainRLock()
	defer b.ChainRUnlock()
	return b.isCurrent()
}

// isCurrent returns whether or not the chain believes it is current.  Several
// factors are used to guess, but the key factors that allow the chain to
// believe it is current are:
//   - Latest block height is after the latest checkpoint (if enabled)
//   - Latest block has a timestamp newer than 24 hours ago
//
// This function MUST be called with the chain state lock held (for reads).
func (b *BlockChain) isCurrent() bool {
	// Not current if the latest main (best) chain height is before the
	// latest known good checkpoint (when checkpoints are enabled).
	checkpoint := b.LatestCheckpoint()
	lastBlock := b.bd.GetMainChainTip()
	if checkpoint != nil && uint64(lastBlock.GetLayer()) < checkpoint.Layer {
		return false
	}
	// At genesis (no blocks after genesis), consider the chain current so
	// mining and block submission can proceed without --allowsubmitwhennotsynced.
	// This avoids the post-genesis freeze when the genesis block timestamp is
	// older than 24 hours.
	if b.GetMainOrder() <= 0 {
		return true
	}
	if b.no24hCheck {
		return true
	}
	// Not current if the latest best block has a timestamp before 24 hours
	// ago.
	//
	// The chain appears to be current if none of the checks reported
	// otherwise.
	minus24Hours := b.timeSource.AdjustedTime().Add(-24 * time.Hour).Unix()
	lastNode := b.GetBlockNode(lastBlock)
	if lastNode == nil {
		return false
	}
	return lastNode.GetTimestamp() >= minus24Hours
}

// TipGeneration returns the entire generation of blocks stemming from the
// parent of the current tip.
//
// The function is safe for concurrent access.
func (b *BlockChain) TipGeneration() ([]ethcommon.Hash, error) {
	tips := b.bd.GetTipsList()
	tiphashs := []ethcommon.Hash{}
	for _, block := range tips {
		tiphashs = append(tiphashs, *block.GetHash())
	}
	return tiphashs, nil
}

// MaximumBlockSize returns the maximum permitted block size for the block
// AFTER the given node.
//
// This function MUST be called with the chain state lock held (for reads).
func (b *BlockChain) maxBlockSize() (int64, error) {

	maxSize := int64(b.params.MaximumBlockSizes[0])

	// The max block size is not changed in any other cases.
	return maxSize, nil
}

// FetchSubsidyCache returns the current subsidy cache from the blockchain.
//
// This function is safe for concurrent access.
func (b *BlockChain) FetchSubsidyCache() *SubsidyCache {
	return b.subsidyCache
}

// reorganizeChain reorganizes the block chain by disconnecting the nodes in the
// detachNodes list and connecting the nodes in the attach list.  It expects
// that the lists are already in the correct order and are in sync with the
// end of the current best chain.  Specifically, nodes that are being
// disconnected must be in reverse order (think of popping them off the end of
// the chain) and nodes the are being attached must be in forwards order
// (think pushing them onto the end of the chain).
//
// This function MUST be called with the chain state lock held (for writes).

const maxFastEVMReorgAttachLen = 1

type fastEVMReorgBlock struct {
	node   blockdag.IBlock
	body   *BlockNode
	header *gethtypes.Header
}

func (b *BlockChain) tryFastEVMReorg(ib blockdag.IBlock, attachNodes *list.List, newBlock *BlockNode, connectedBlocks *list.List, anchor model.BlockState) (bool, error) {
	if b == nil || b.bdagChain == nil || attachNodes == nil || attachNodes.Len() == 0 || attachNodes.Len() > maxFastEVMReorgAttachLen || anchor == nil {
		return false, nil
	}
	parentHeader, err := b.bdagChain.EVMHeaderForState(anchor)
	if err != nil {
		log.Debug("[DEBUG-evm-reorg] fast path unavailable", "reason", "anchor header unavailable", "err", err)
		return false, nil
	}

	fastBlocks := make([]fastEVMReorgBlock, 0, attachNodes.Len())
	for e := attachNodes.Front(); e != nil; e = e.Next() {
		nodeBlock := e.Value.(blockdag.IBlock)
		if !nodeBlock.IsOrdered() || nodeBlock.GetState().GetStatus().KnownInvalid() {
			return false, nil
		}

		block := newBlock
		if nodeBlock.GetID() != ib.GetID() {
			block = b.GetBlockNode(nodeBlock)
		}
		if block == nil {
			return false, nil
		}

		buildStart := time.Now()
		err = b.bdagCheckConnectBlockWithParent(block, parentHeader, uint64(nodeBlock.GetLayer()))
		evmReorgFastBuild.UpdateSince(buildStart)
		if err != nil {
			log.Debug("[DEBUG-evm-reorg] fast path validation failed", "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder(), "err", err)
			return false, nil
		}

		validationStart := time.Now()
		err = b.checkEVMStateGuardCandidateWithBuiltBlock(nodeBlock, block)
		evmReorgValidation.UpdateSince(validationStart)
		if err != nil {
			log.Debug("[DEBUG-evm-reorg] fast path state guard failed", "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder(), "err", err)
			return false, nil
		}

		evmBlock := block.GetbdagBlock()
		if evmBlock == nil || evmBlock.EvmBlock == nil {
			return false, nil
		}
		insertStart := time.Now()
		err = b.bdagChain.InsertBlockWithoutSetHead(evmBlock)
		evmReorgFastInsert.UpdateSince(insertStart)
		if err != nil {
			log.Debug("[DEBUG-evm-reorg] fast path side insert failed", "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder(), "err", err)
			return false, nil
		}

		parentHeader = evmBlock.EvmBlock.Header()
		fastBlocks = append(fastBlocks, fastEVMReorgBlock{node: nodeBlock, body: block, header: parentHeader})
	}
	if len(fastBlocks) == 0 {
		return false, nil
	}

	setCanonicalStart := time.Now()
	err = b.bdagChain.SetCanonicalBlock(fastBlocks[len(fastBlocks)-1].body.GetbdagBlock())
	evmReorgFastSetCanonical.UpdateSince(setCanonicalStart)
	if err != nil {
		return true, err
	}

	for _, fastBlock := range fastBlocks {
		if err = b.connectBlockAfterEVMFastPath(fastBlock.node, fastBlock.body, connectedBlocks); err != nil {
			return true, err
		}
		b.bd.ValidBlock(fastBlock.node)
		if err = b.updateBlockStateWithEVMHeader(fastBlock.node, fastBlock.body.GetBody(), fastBlock.header); err != nil {
			log.Error(err.Error())
		}
		log.Debug("attach block", "hash", fastBlock.node.GetHash().String(), "order", fastBlock.node.GetOrder(), "status", fastBlock.node.GetState().GetStatus().String(), "evmReorgFastPath", true)
	}
	log.Info("[DEBUG-evm-reorg] fast path applied", "attachLen", attachNodes.Len(), "anchorEVMNumber", anchor.GetEVMNumber(), "anchorEVMHash", anchor.GetEVMHash().String(), "newHeadEVMNumber", fastBlocks[len(fastBlocks)-1].header.Number.Uint64(), "newHeadEVMHash", fastBlocks[len(fastBlocks)-1].header.Hash().String())
	return true, nil
}

func (b *BlockChain) connectBlockAfterEVMFastPath(node blockdag.IBlock, blockNode *BlockNode, connectedBlocks *list.List) error {
	block := blockNode.GetBody()
	if !node.GetState().GetStatus().KnownInvalid() {
		err := b.indexManager.ConnectBlock(block, node)
		if err != nil {
			return fmt.Errorf("%v. (Attempt to execute --droptxindex)", err)
		}
	} else {
		err := b.indexManager.ConnectBlock(block, node)
		if err != nil {
			return err
		}
		return nil
	}
	connectedBlocks.PushBack([]any{block, b.bd.IsOnMainChain(node.GetID()), node})
	return nil
}

func (b *BlockChain) reorganizeChain(ib blockdag.IBlock, detachNodes *list.List, attachNodes *list.List, newBlock *BlockNode, connectedBlocks *list.List) error {
	reorgStart := time.Now()
	defer evmReorgTotal.UpdateSince(reorgStart)
	evmReorgStartState, evmReorgStartBlock, err := b.evmReorgAnchor(attachNodes)
	if err != nil {
		return err
	}

	oldBlocks := []*ethcommon.Hash{}
	for e := detachNodes.Front(); e != nil; e = e.Next() {
		ob := e.Value.(*blockdag.BlockOrderHelp)
		oldBlocks = append(oldBlocks, ob.Block.GetHash())
	}

	b.sendNotification(Reorganization, &ReorganizationNotifyData{
		OldBlocks: oldBlocks,
		NewBlock:  newBlock.GetHash(),
		NewOrder:  uint64(ib.GetOrder()),
	})

	// Why the old order is the order that was removed by the new block, because the new block
	// must be one of the tip of the dag.This is very important for the following understanding.
	// In the two case, the perspective is the same.In the other words, the future can not
	// affect the past.
	var block *BlockNode

	for e := detachNodes.Back(); e != nil; e = e.Prev() {
		n := e.Value.(*blockdag.BlockOrderHelp)
		if n == nil {
			panic(fmt.Errorf("No BlockOrderHelp"))
		}

		er := b.updateDefaultBlockState(n.Block)
		if er != nil {
			log.Error(er.Error())
		}
		//
		blockNode := b.GetBlockNode(n.Block)
		if blockNode == nil {
			panic(fmt.Errorf("no block node:%s", n.Block.GetHash()))
		}
		block := blockNode.GetBody()
		log.Debug("detach block", "hash", n.Block.GetHash().String(), "old order", n.OldOrder, "status", n.Block.GetState().GetStatus().String())
		// if upgrades.IsUpgradeActive(upgrades.UpgradeHeaderStatus.Name, uint64(block.Block().Header.Timestamp.Unix())) {
		// 	block.SetIsBlue(false)
		// }

		b.bd.ValidBlock(n.Block)
		err = b.disconnectBlock(n.Block, block)
		if err != nil {
			log.Error(fmt.Sprintf("Disconnect block error:%s", err.Error()))
			return err
		}
	}
	if evmReorgStartState != nil {
		fastStart := time.Now()
		fast, fastErr := b.tryFastEVMReorg(ib, attachNodes, newBlock, connectedBlocks, evmReorgStartState)
		evmReorgFastPath.UpdateSince(fastStart)
		if fastErr != nil {
			return fastErr
		}
		if fast {
			log.Info(fmt.Sprintf("End DAG REORGANIZE: Old Len= %d;New Len= %d", detachNodes.Len(), attachNodes.Len()))
			return nil
		}

		rewindStart := time.Now()
		err = b.rewindEVMStateGuardReorgStart(evmReorgStartState, evmReorgStartBlock)
		evmReorgRewind.UpdateSince(rewindStart)
		if err != nil {
			return err
		}
	}
	isEVMInit := false
	var governanceErr error
	for e := attachNodes.Front(); e != nil; e = e.Next() {
		nodeBlock := e.Value.(blockdag.IBlock)
		if nodeBlock.GetID() == ib.GetID() {
			block = newBlock
		} else {
			// If any previous nodes in attachNodes failed validation,
			// mark this one as having an invalid ancestor.
			block = b.GetBlockNode(nodeBlock)

			if block == nil {
				return fmt.Errorf("No block node:%s", nodeBlock.GetHash())
			}
		}
		if !nodeBlock.IsOrdered() {
			er := b.updateDefaultBlockState(nodeBlock)
			if er != nil {
				log.Error(er.Error())
			}
			continue
		}
		if !isEVMInit {
			isEVMInit = true
			err = b.prepareEVMEnvironment(nodeBlock)
			if err != nil {
				if isEVMStateGuardRuleError(err) {
					return b.rewindEVMStateGuardRejection(evmReorgStartState, nodeBlock, err)
				}
				return err
			}
		}

		err = b.checkConnectBlock(nodeBlock, block)
		if err != nil {
			if b.enforcingEVMStateGuard(nodeBlock) && !isGovernanceAdmissibilityRuleError(err) {
				guardErr := b.handleEVMStateGuardError(nodeBlock, fmt.Errorf("dry-connect EVM block: %w", err))
				guardErr = b.rejectEVMStateGuardCandidate(evmReorgStartState, nodeBlock, guardErr)
				return guardErr
			} else {
				b.bd.InvalidBlock(nodeBlock)
				governanceErr = b.bindLocalGovernanceRejection(governanceErr, nodeBlock, err)
			}
		} else if err = b.checkEVMStateGuardCandidateWithBuiltBlock(nodeBlock, block); err != nil {
			if isEVMStateGuardRuleError(err) {
				err = b.rejectEVMStateGuardCandidate(evmReorgStartState, nodeBlock, err)
				return err
			} else {
				b.bd.InvalidBlock(nodeBlock)
			}
		}

		err = b.connectBlock(nodeBlock, block, connectedBlocks, nil)
		if err != nil {
			if isEVMStateGuardRuleError(err) {
				err = b.rejectEVMStateGuardCandidate(evmReorgStartState, nodeBlock, err)
				return err
			}
			b.bd.InvalidBlock(nodeBlock)
			er := b.updateDefaultBlockState(nodeBlock)
			if er != nil {
				log.Error(er.Error())
			}
			return err
		}
		if !nodeBlock.GetState().GetStatus().KnownInvalid() {
			b.bd.ValidBlock(nodeBlock)
		}
		er := b.updateBlockState(nodeBlock, block.GetBody())
		if er != nil {
			log.Error(er.Error())
		}
		log.Debug("attach block", "hash", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder(), "status", nodeBlock.GetState().GetStatus().String())
	}

	// Log the point where the chain forked and old and new best chain
	// heads.
	log.Info(fmt.Sprintf("End DAG REORGANIZE: Old Len= %d;New Len= %d", detachNodes.Len(), attachNodes.Len()))

	if governanceErr != nil {
		return governanceErr
	}
	return nil
}

func (b *BlockChain) ChainLock() {
	b.chainLock.Lock()
}

func (b *BlockChain) ChainUnlock() {
	b.chainLock.Unlock()
}

func (b *BlockChain) ChainRLock() {
	b.chainLock.RLock()
}

func (b *BlockChain) ChainRUnlock() {
	b.chainLock.RUnlock()
}

func (b *BlockChain) CalculateFees(block *types.SerializedBlock) (int64, error) {
	totalFees := int64(0)

	for _, ethTx := range block.Transactions() {
		// Assert that the transaction is an Ethereum transaction

		// Fetch the transaction receipt to get the GasUsed
		receipt, err := b.bdagChain.GetEthClient().TransactionReceipt(context.Background(), ethTx.Hash())
		if err != nil {
			return 0, fmt.Errorf("failed to get receipt for transaction %s: %w", ethTx.Hash().Hex(), err)
		}

		// Calculate the fee: GasPrice * GasUsed
		txFee := new(big.Int).Mul(ethTx.GasPrice(), big.NewInt(int64(receipt.GasUsed)))

		// Add to the total fees (convert Wei to your AmountMap type)
		totalFees += txFee.Int64()
	}

	return totalFees, nil
}

// GetFees
func (b *BlockChain) GetFees(h *ethcommon.Hash) int64 {
	ib := b.GetBlock(h)
	if ib == nil {
		return 0
	}
	if ib.GetState().GetStatus().KnownInvalid() {
		return 0
	}
	bn := b.GetBlockNode(ib)
	if bn == nil {
		return 0
	}

	fees, _ := b.CalculateFees(bn.GetBody())
	return fees
}

func (b *BlockChain) CalcWeight(ib blockdag.IBlock, bi *blockdag.BlueInfo) int64 {
	if ib.GetState().GetStatus().KnownInvalid() {
		return 0
	}
	bn := b.GetBlockNode(ib)
	if bn == nil {
		log.Error(fmt.Sprintf("CalcWeight:%v", ib.GetHash().String()))
		return 0
	}
	return b.subsidyCache.CalcBlockSubsidy(bi, ib.GetData().GetTimestamp())
}

func (b *BlockChain) CalculateTokenStateRoot(txs []*types.Tx) *ethcommon.Hash {
	return hash.ZeroHash.ToEVMHashPtr()
}

// ComputeKeccak256Hash hashes the given data using Keccak-256
func ComputeKeccak256Hash(data []byte) *ethcommon.Hash {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	hash := ethcommon.BytesToHash(h.Sum(nil))
	return &hash
}

// serialize serializes any struct into bytes
func serialize(data any) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(data)
	return buf.Bytes(), err
}

func (b *BlockChain) CalculateStateRoot(block *types.Block) *ethcommon.Hash {
	// Convert block to JSON (ensuring all fields are included)
	// Create a copy of the block header without the StateRoot
	headerCopy := block.Header
	headerCopy.StateRoot = ethcommon.Hash{} // Zero out StateRoot

	// Serialize the modified header
	headerBytes, _ := serialize(headerCopy)

	// Serialize Parents (convert []*common.Hash to []common.Hash)
	var parentHashes []ethcommon.Hash
	for _, p := range block.Parents {
		parentHashes = append(parentHashes, *p)
	}
	parentBytes, _ := serialize(parentHashes)

	// Serialize Transactions
	var txHashes []ethcommon.Hash
	for _, tx := range block.Transactions {
		txHashes = append(txHashes, tx.Hash())
	}
	txBytes, _ := serialize(txHashes)

	// Combine all components
	finalBytes := append(headerBytes, parentBytes...)
	finalBytes = append(finalBytes, txBytes...)

	// Compute hash of serialized block data
	return ComputeKeccak256Hash(finalBytes)
}

// CalcPastMedianTime calculates the median time of the previous few blocks
// prior to, and including, the block node.
//
// This function is safe for concurrent access.
func (b *BlockChain) CalcPastMedianTime(block blockdag.IBlock) time.Time {
	// Create a slice of the previous few block timestamps used to calculate
	// the median per the number defined by the constant medianTimeBlocks.
	timestamps := make([]int64, medianTimeBlocks)
	numNodes := 0
	iterBlock := block
	for i := 0; i < medianTimeBlocks && iterBlock != nil; i++ {
		iterNode := b.GetBlockHeader(iterBlock)
		if iterNode == nil {
			break
		}
		timestamps[i] = iterNode.Timestamp.Unix()
		numNodes++

		iterBlock = b.bd.GetBlockById(iterBlock.GetMainParent())
	}

	// Prune the slice to the actual number of available timestamps which
	// will be fewer than desired near the beginning of the block chain
	// and sort them.
	timestamps = timestamps[:numNodes]
	sort.Sort(util.TimeSorter(timestamps))

	// NOTE: The consensus rules incorrectly calculate the median for even
	// numbers of blocks.  A true median averages the middle two elements
	// for a set with an even number of elements in it.   Since the constant
	// for the previous number of blocks to be used is odd, this is only an
	// issue for a few blocks near the beginning of the chain.  I suspect
	// this is an optimization even though the result is slightly wrong for
	// a few of the first blocks since after the first few blocks, there
	// will always be an odd number of blocks in the set per the constant.
	//
	// This code follows suit to ensure the same rules are used, however, be
	// aware that should the medianTimeBlocks constant ever be changed to an
	// even number, this code will be wrong.
	medianTimestamp := timestamps[numNodes/2]
	return time.Unix(medianTimestamp, 0)
}

// BestSnapshot returns information about the current best chain block and
// related state as of the current point in time.  The returned instance must be
// treated as immutable since it is shared by all callers.
//
// This function is safe for concurrent access.
func (b *BlockChain) BestSnapshot() *BestState {
	b.stateLock.RLock()
	snapshot := b.stateSnapshot
	b.stateLock.RUnlock()
	return snapshot
}

func (b *BlockChain) GetSubsidyCache() *SubsidyCache {
	return b.subsidyCache
}

func (b *BlockChain) DB() model.DataBase {
	return b.consensus.DatabaseContext()
}

func (b *BlockChain) IndexManager() model.IndexManager {
	return b.indexManager
}

// Return chain params
func (b *BlockChain) ChainParams() *params.Params {
	return b.params
}

// Return the dag instance
func (b *BlockChain) Blockdag() *blockdag.BDAG {
	return b.bd
}

// Return median time source
func (b *BlockChain) TimeSource() model.MedianTimeSource {
	return b.timeSource
}

func (b *BlockChain) Rebuild() error {
	gib := b.Blockdag().GetBlockById(0)
	if gib == nil {
		return fmt.Errorf("No genesis block")
	}

	logLvl := l.Glogger().GetVerbosity()
	bar := progressbar.Default(int64(b.GetMainOrder()), fmt.Sprintf("Rebuild:"))
	l.Glogger().Verbosity(l.LvlCrit)
	eth.InitLog(l.LvlCrit.String(), b.consensus.Config().DebugPrintOrigins)

	defer func() {
		l.Glogger().Verbosity(logLvl)
		eth.InitLog(logLvl.String(), b.consensus.Config().DebugPrintOrigins)
	}()

	b.evmChainLock.Lock()
	defer b.evmChainLock.Unlock()

	var block *types.SerializedBlock
	for i := uint(0); i <= b.GetMainOrder(); i++ {
		bar.Add(1)
		if system.InterruptRequested(b.consensus.Interrupt()) {
			return fmt.Errorf("interrupt rebuild")
		}
		ib := b.bd.GetBlockByOrder(i)
		if ib == nil {
			return fmt.Errorf("No block order:%d", i)
		}

		blockNode := b.GetBlockNode(ib)
		if blockNode == nil {
			return fmt.Errorf("No block node:%s", ib.GetHash())
		}
		block = blockNode.GetBody()
		if i == 0 {
			err := b.indexManager.ConnectBlock(block, ib)
			if err != nil {
				return err
			}
			continue
		}
		if ib.GetState().GetStatus().KnownInvalid() {
			connectedBlocks := list.New()
			if err := b.connectBlock(ib, blockNode, connectedBlocks, nil); err != nil {
				return err
			}
			if err := b.updateBlockState(ib, blockNode.GetBody()); err != nil {
				log.Error(err.Error())
			}
			if err := b.bd.Commit(); err != nil {
				return err
			}
			continue
		}

		err := b.checkConnectBlock(ib, blockNode)
		if err != nil {
			if b.evmStateGuard.enforcingCommitment(uint64(ib.GetOrder())) && !isGovernanceAdmissibilityRuleError(err) {
				err = b.handleEVMStateGuardError(ib, fmt.Errorf("dry-connect EVM block during rebuild: %w", err))
				b.bd.InvalidBlock(ib)
				return err
			}
			b.bd.InvalidBlock(ib)
		} else if err = b.checkEVMStateGuardCandidateWithBuiltBlock(ib, blockNode); err != nil {
			if isEVMStateGuardRuleError(err) {
				b.bd.InvalidBlock(ib)
				return err
			}
			b.bd.InvalidBlock(ib)
		}
		connectedBlocks := list.New()
		err = b.connectBlock(ib, blockNode, connectedBlocks, nil)
		if err != nil {
			b.bd.InvalidBlock(ib)
			return err
		}
		if !ib.GetState().GetStatus().KnownInvalid() {
			b.bd.ValidBlock(ib)
		}
		err = b.updateBlockState(ib, blockNode.GetBody())
		if err != nil {
			log.Error(err.Error())
		}
		err = b.bd.Commit()
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *BlockChain) GetBlockState(order uint64) model.BlockState {
	block := b.Blockdag().GetBlockByOrder(uint(order))
	if block == nil {
		return nil
	}
	return block.GetState()
}

func (b *BlockChain) Consensus() model.Consensus {
	return b.consensus
}

func (b *BlockChain) ProcessQueueSize() int {
	size := 0
	b.processQueueMap.Range(func(key, value any) bool {
		size++
		return true
	})
	return size
}

// New returns a BlockChain instance using the provided configuration details.
func New(consensus model.Consensus) (*BlockChain, error) {
	// Enforce required config fields.
	if consensus.DatabaseContext() == nil {
		return nil, model.AssertError("blockchain.New database is nil")
	}
	if consensus.Params() == nil {
		return nil, model.AssertError("blockchain.New chain parameters nil")
	}

	// Generate a checkpoint by height map from the provided checkpoints.
	par := consensus.Params()
	var checkpointsByLayer map[uint64]*params.Checkpoint
	var prevCheckpointLayer uint64
	if len(par.Checkpoints) > 0 {
		checkpointsByLayer = make(map[uint64]*params.Checkpoint)
		for i := range par.Checkpoints {
			checkpoint := &par.Checkpoints[i]
			if checkpoint.Layer <= prevCheckpointLayer {
				return nil, model.AssertError("blockchain.New " +
					"checkpoints are not sorted by height")
			}
			checkpointsByLayer[checkpoint.Layer] = checkpoint
			prevCheckpointLayer = checkpoint.Layer
		}
	}

	config := consensus.Config()
	evmStateGuard := newEVMStateGuard(par.EVMStateCommitmentStartOrder)
	b := BlockChain{
		consensus:          consensus,
		checkpointsByLayer: checkpointsByLayer,
		params:             par,
		timeSource:         consensus.MedianTimeSource(),
		events:             consensus.Events(),
		sigCache:           consensus.SigCache(),
		indexManager:       consensus.IndexManager(),
		orphans:            make(map[ethcommon.Hash]*orphanBlock),
		CacheNotifications: []*Notification{},
		headerList:         list.New(),
		progressLogger:     progresslog.NewBlockProgressLogger("Processed", log),
		msgChan:            make(chan *processMsg),
		quit:               make(chan struct{}),
		evmCatchupWake:     make(chan struct{}, 1),
		no24hCheck:         config.No24hCheck,
		evmStateGuard:      evmStateGuard,
	}
	if config.EVMNativeCatchupBatchOrders > 0 {
		b.evmNativeCatchupBatchOrders = config.EVMNativeCatchupBatchOrders
	} else {
		b.evmNativeCatchupBatchOrders = evmNativeCatchupBatchOrdersDefault
	}
	b.selfAdd.Store(0)

	b.subsidyCache = NewSubsidyCache(0, b.params)

	b.bd = blockdag.New(config.DAGType, 1.0/float64(par.TargetTimePerBlock/time.Second), b.DB(), b.getBlockData)
	b.bd.SetCacheSize(config.DAGCacheSize, config.BlockDataCacheSize)

	b.InitServices()
	b.Services().RegisterService(b.bd)

	mchain, err := bdag.NewBdagChain(consensus, b.mainnetDAOAdoptionProof())
	if err != nil {
		return nil, err
	}
	b.bdagChain = mchain
	b.Services().RegisterService(b.bdagChain)
	b.resetUpgradeCache()
	if ethChain := b.bdagChain.ETHChain(); ethChain != nil && ethChain.Ether() != nil && ethChain.Ether().BlockChain() != nil {
		if genesis := ethChain.Ether().BlockChain().Genesis(); genesis != nil {
			b.evmGenesisHeader = genesis.Header()
		}
	}
	return &b, nil
}

func init() {
	blockdag.SetBlockStateFactory(state.CreateBlockState, state.CreateBlockStateFromBytes)
}
