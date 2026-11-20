package chaindb

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/dataset"
	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
	"github.com/BlockdagNetworkLabs/bdag/core/types"

	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/ethereum/go-ethereum/cmd/utils"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
)

var (
	DBDirectoryName = "BdagChain"
	CreateIfNoExist = true
)

type ChainDB struct {
	db ethdb.Database

	cfg  *config.Config
	lock sync.RWMutex
	// All open databases
	databases   map[*closeTrackingDB]struct{}
	closedState atomic.Bool

	hasInit bool

	diff            *diffLayer
	shutdownTracker *shutdown.Tracker
	recoveryMu      sync.Mutex
	recoveryMarker  *shutdown.Marker
	recoveryClass   shutdown.MarkerClassification
	ancient         bool
}

func (cdb *ChainDB) Name() string {
	return "Chain DB"
}

func (cdb *ChainDB) Init() error {
	log.Info("Init", "name", cdb.Name())
	if cdb.hasInit {
		return fmt.Errorf("%s: Need to thoroughly clean up old data", cdb.Name())
	}
	return nil
}

func (cdb *ChainDB) Close() {
	log.Info("Close", "name", cdb.Name())
	if cdb.diff != nil {
		err := cdb.diff.close()
		if err != nil {
			log.Error(err.Error())
		}
	}
	if cdb.closedState.Load() {
		log.Error("Already closed", "name", cdb.Name())
		return
	}
	cdb.closedState.Store(true)
	cdb.closeDatabases()
}

func (cdb *ChainDB) DB() ethdb.Database {
	return cdb.db
}

func (cdb *ChainDB) DBEngine() string {
	if cdb.cfg.DbType == "ffldb" {
		return "pebble"
	}
	return cdb.cfg.DbType
}

func (cdb *ChainDB) Snapshot() error {
	if cdb.cfg.SnapshotCache() > 0 {
		cdb.diff = newDiffLayer(cdb, cdb.cfg.SnapshotCache())
	}
	return nil
}

func (cdb *ChainDB) SnapshotInfo() string {
	if cdb.diff == nil {
		return "not active"
	}
	return fmt.Sprintf("mem=%s,objects=%d", cdb.diff.memorySize().String(), cdb.diff.objects())
}

// FlushRecoveryCheckpoint makes snapshot-layer writes durable without closing
// the database. Recovery batches use it after committing EVM state so a crash
// can leave EVM ahead of native metadata, but never native metadata pointing at
// EVM state that was not persisted.
func (cdb *ChainDB) FlushRecoveryCheckpoint() error {
	if cdb.diff == nil {
		return nil
	}
	return cdb.diff.flatten()
}

// wrapDatabase ensures the database will be auto-closed when Node is closed.
func (cdb *ChainDB) wrapDatabase(db ethdb.Database) ethdb.Database {
	wrapper := &closeTrackingDB{db, cdb}
	cdb.databases[wrapper] = struct{}{}
	return wrapper
}

// closeDatabases closes all open databases.
func (cdb *ChainDB) closeDatabases() (errors []error) {
	for db := range cdb.databases {
		delete(cdb.databases, db)
		if err := db.Database.Close(); err != nil {
			errors = append(errors, err)
		}
	}
	return errors
}

func (cdb *ChainDB) CloseDatabases() (errors []error) {
	return cdb.closeDatabases()
}

func (cdb *ChainDB) OpenDatabaseWithFreezer(name string, cache, handles int, ancient string, namespace string, readonly bool, skipBackgroundFreeze bool) (ethdb.Database, error) {
	if !CreateIfNoExist {
		existingDb := rawdb.PreexistingDatabase(cdb.cfg.ResolveDataPath(name))
		if len(existingDb) <= 0 {
			return nil, ErrDBAbsent
		}
	}

	cdb.lock.Lock()
	defer cdb.lock.Unlock()
	if cdb.closedState.Load() {
		return nil, ErrDBClosed
	}

	var db ethdb.Database
	var err error
	if cdb.cfg.DataDir == "" {
		db = rawdb.NewMemoryDatabase()
	} else {
		db, err = rawdb.Open(rawdb.OpenOptions{
			Type:                 cdb.DBEngine(),
			Directory:            cdb.cfg.ResolveDataPath(name),
			AncientsDirectory:    cdb.ResolveAncient(name, ancient),
			Namespace:            namespace,
			Cache:                cache,
			Handles:              handles,
			ReadOnly:             readonly,
			SkipBackgroundFreeze: skipBackgroundFreeze,
		})
	}

	if err == nil {
		db = cdb.wrapDatabase(db)
	}
	return db, err
}

func (cdb *ChainDB) OpenDatabase(name string, cache, handles int, namespace string, readonly bool) (ethdb.Database, error) {
	cdb.lock.Lock()
	defer cdb.lock.Unlock()
	if cdb.closedState.Load() {
		return nil, ErrDBClosed
	}

	var db ethdb.Database
	var err error
	if cdb.cfg.DataDir == "" {
		db = rawdb.NewMemoryDatabase()
	} else {
		db, err = rawdb.Open(rawdb.OpenOptions{
			Type:      cdb.DBEngine(),
			Directory: cdb.cfg.ResolveDataPath(name),
			Namespace: namespace,
			Cache:     cache,
			Handles:   handles,
			ReadOnly:  readonly,
		})
	}

	if err == nil {
		db = cdb.wrapDatabase(db)
	}
	return db, err
}

func (cdb *ChainDB) ResolveAncient(name string, ancient string) string {
	if !cdb.ancient {
		return ""
	}
	switch {
	case ancient == "":
		ancient = filepath.Join(cdb.cfg.ResolveDataPath(name), "ancient")
	case !filepath.IsAbs(ancient):
		ancient = cdb.cfg.ResolveDataPath(ancient)
	}
	return ancient
}

func (cdb *ChainDB) CleanInvalidTxIdx() error {
	err := rawdb.CleanInvalidTxs(cdb.db)
	if err != nil {
		return err
	}
	return nil
}
func (cdb *ChainDB) Rebuild(mgr model.IndexManager) error {
	err := cdb.CleanInvalidTxIdx()
	if err != nil {
		return err
	}
	return cdb.CleanAddrIdx(false)
}

func (cdb *ChainDB) GetBestChainState() ([]byte, error) {
	if cdb.diff != nil {
		return cdb.diff.GetBestChainState()
	}
	return rawdb.ReadBestChainState(cdb.db), nil
}

func (cdb *ChainDB) PutBestChainState(data []byte) error {
	if cdb.diff != nil {
		return cdb.diff.PutBestChainState(data)
	}
	return rawdb.WriteBestChainState(cdb.db, data)
}

func (cdb *ChainDB) GetBlock(hash *ethcommon.Hash) (*types.SerializedBlock, error) {
	if cdb.diff != nil {
		return cdb.diff.GetBlock(hash)
	}
	return rawdb.ReadBody(cdb.db, hash), nil
}

func (cdb *ChainDB) GetBlockBytes(hash *ethcommon.Hash) ([]byte, error) {
	if cdb.diff != nil {
		return cdb.diff.GetBlockBytes(hash)
	}
	return rawdb.ReadBodyRaw(cdb.db, hash), nil
}

func (cdb *ChainDB) GetHeader(hash *ethcommon.Hash) (*types.BlockHeader, error) {
	if cdb.diff != nil {
		return cdb.diff.GetHeader(hash)
	}
	return rawdb.ReadHeader(cdb.db, hash), nil
}

func (cdb *ChainDB) PutBlock(block *types.SerializedBlock) error {
	if cdb.diff != nil {
		return cdb.diff.PutBlock(block)
	}
	return cdb.writeBlockToBatch(block)
}

func (cdb *ChainDB) writeBlockToBatch(block *types.SerializedBlock) error {
	batch := cdb.db.NewBatch()
	err := rawdb.WriteBlock(batch, block)
	if err != nil {
		return err
	}
	return batch.Write()
}

func (cdb *ChainDB) HasBlock(hash *ethcommon.Hash) bool {
	if cdb.diff != nil {
		return cdb.diff.HasBlock(hash)
	}
	return rawdb.HasHeader(cdb.db, hash)
}

func (cdb *ChainDB) GetDagInfo() ([]byte, error) {
	return rawdb.ReadDAGInfo(cdb.db), nil
}

func (cdb *ChainDB) PutDagInfo(data []byte) error {
	return rawdb.WriteDAGInfo(cdb.db, data)
}

func (cdb *ChainDB) GetDAGBlock(blockID uint) ([]byte, error) {
	if cdb.diff != nil {
		return cdb.diff.GetDAGBlock(blockID)
	}
	return rawdb.ReadDAGBlockBaw(cdb.db, uint64(blockID)), nil
}

func (cdb *ChainDB) PutDAGBlock(blockID uint, data []byte) error {
	if cdb.diff != nil {
		return cdb.diff.PutDAGBlock(blockID, data)
	}
	return rawdb.WriteDAGBlockRaw(cdb.db, blockID, data)
}

func (cdb *ChainDB) DeleteDAGBlock(blockID uint) error {
	if cdb.diff != nil {
		return cdb.diff.DeleteDAGBlock(blockID)
	}
	rawdb.DeleteDAGBlock(cdb.db, uint64(blockID))
	return nil
}

func (cdb *ChainDB) GetDAGBlockIdByHash(bh *ethcommon.Hash) (uint, error) {
	if cdb.diff != nil {
		return cdb.diff.GetDAGBlockIdByHash(bh)
	}
	blockID := rawdb.ReadBlockID(cdb.db, bh)
	if blockID == nil {
		return blockdag.MaxId, fmt.Errorf("No blockID:%s", bh.String())
	}
	return uint(*blockID), nil
}

func (cdb *ChainDB) PutDAGBlockIdByHash(bh *ethcommon.Hash, id uint) error {
	if cdb.diff != nil {
		return cdb.diff.PutDAGBlockIdByHash(bh, id)
	}
	rawdb.WriteBlockID(cdb.db, bh, uint64(id))
	return nil
}

func (cdb *ChainDB) DeleteDAGBlockIdByHash(bh *ethcommon.Hash) error {
	if cdb.diff != nil {
		return cdb.diff.DeleteDAGBlockIdByHash(bh)
	}
	rawdb.DeleteBlockID(cdb.db, bh)
	return nil
}

func (cdb *ChainDB) PutMainChainBlock(blockID uint) error {
	if cdb.diff != nil {
		return cdb.diff.PutMainChainBlock(blockID)
	}
	return rawdb.WriteMainChain(cdb.db, uint64(blockID))
}

func (cdb *ChainDB) HasMainChainBlock(blockID uint) bool {
	if cdb.diff != nil {
		return cdb.diff.HasMainChainBlock(blockID)
	}
	return rawdb.ReadMainChain(cdb.db, uint64(blockID))
}

func (cdb *ChainDB) DeleteMainChainBlock(blockID uint) error {
	if cdb.diff != nil {
		return cdb.diff.DeleteMainChainBlock(blockID)
	}
	rawdb.DeleteMainChain(cdb.db, uint64(blockID))
	return nil
}

func (cdb *ChainDB) PutBlockIdByOrder(order uint, id uint) error {
	if cdb.diff != nil {
		return cdb.diff.PutBlockIdByOrder(order, id)
	}
	return rawdb.WriteBlockOrderSnapshot(cdb.db, uint64(order), uint64(id))
}

func (cdb *ChainDB) GetBlockIdByOrder(order uint) (uint, error) {
	if cdb.diff != nil {
		return cdb.diff.GetBlockIdByOrder(order)
	}
	id := rawdb.ReadBlockOrderSnapshot(cdb.db, uint64(order))
	if id == nil {
		return blockdag.MaxId, nil
	}
	return uint(*id), nil
}

func (cdb *ChainDB) PutDAGTip(id uint, isMain bool) error {
	if cdb.diff != nil {
		return cdb.diff.PutDAGTip(id, isMain)
	}
	ctips := rawdb.ReadDAGTips(cdb.db)
	temp := []uint64{}
	for i := 0; i < len(ctips); i++ {
		if ctips[i] == uint64(id) {
			if i == 0 {
				temp = append(temp, uint64(blockdag.MaxId))
			}
			continue
		}
		temp = append(temp, ctips[i])
	}
	tips := []uint64{}
	if len(temp) > 0 {
		tips = append(tips, temp...)
		if isMain {
			tips[0] = uint64(id)
		} else {
			tips = append(tips, uint64(id))
		}
	} else {
		if isMain {
			tips = append(tips, uint64(id))
		} else {
			tips = append(tips, uint64(blockdag.MaxId))
			tips = append(tips, uint64(id))
		}
	}
	return rawdb.WriteDAGTips(cdb.db, tips)
}

func (cdb *ChainDB) GetDAGTips() ([]uint, error) {
	if cdb.diff != nil {
		return cdb.diff.GetDAGTips()
	}
	tips := rawdb.ReadDAGTips(cdb.db)
	if len(tips) <= 0 {
		return nil, fmt.Errorf("No tips")
	}
	if tips[0] == uint64(blockdag.MaxId) {
		return nil, fmt.Errorf("Can't find main tip")
	}
	result := []uint{}
	for i := 0; i < len(tips); i++ {
		result = append(result, uint(tips[i]))
	}
	return result, nil
}

func (cdb *ChainDB) DeleteDAGTip(id uint) error {
	if cdb.diff != nil {
		return cdb.diff.DeleteDAGTip(id)
	}
	tips := rawdb.ReadDAGTips(cdb.db)
	result := []uint64{}
	dirty := false
	for i := 0; i < len(tips); i++ {
		if tips[i] == uint64(id) {
			dirty = true
			if i == 0 {
				result = append(result, uint64(blockdag.MaxId))
			}
			continue
		}
		result = append(result, tips[i])
	}
	if dirty {
		rawdb.WriteDAGTips(cdb.db, result)
	}

	return nil
}

func (cdb *ChainDB) PutDiffAnticone(id uint) error {
	cda := rawdb.ReadDiffAnticone(cdb.db)
	temp := []uint64{}
	for i := 0; i < len(cda); i++ {
		if cda[i] == uint64(id) {
			continue
		}
		temp = append(temp, cda[i])
	}
	da := []uint64{}
	if len(temp) > 0 {
		da = append(da, temp...)
	}
	da = append(da, uint64(id))
	return rawdb.WriteDiffAnticone(cdb.db, da)
}

func (cdb *ChainDB) GetDiffAnticones() ([]uint, error) {
	da := rawdb.ReadDiffAnticone(cdb.db)
	result := []uint{}
	for i := 0; i < len(da); i++ {
		result = append(result, uint(da[i]))
	}
	return result, nil
}

func (cdb *ChainDB) DeleteDiffAnticone(id uint) error {
	da := rawdb.ReadDiffAnticone(cdb.db)
	result := []uint64{}
	dirty := false
	for i := 0; i < len(da); i++ {
		if da[i] == uint64(id) {
			dirty = true
			continue
		}
		result = append(result, da[i])
	}
	if dirty {
		rawdb.WriteDiffAnticone(cdb.db, result)
	}

	return nil
}

func (cdb *ChainDB) Get(key []byte) ([]byte, error) {
	return cdb.db.Get(key)
}

func (cdb *ChainDB) Put(key []byte, value []byte) error {
	return cdb.db.Put(key, value)
}

func (cdb *ChainDB) IsLegacy() bool {
	return false
}

func (cdb *ChainDB) GetEstimateFee() ([]byte, error) {
	return rawdb.ReadEstimateFee(cdb.db), nil
}

func (cdb *ChainDB) PutEstimateFee(data []byte) error {
	return rawdb.WriteEstimateFee(cdb.db, data)
}

func (cdb *ChainDB) DeleteEstimateFee() error {
	return rawdb.DeleteEstimateFee(cdb.db)
}

func (cdb *ChainDB) StartTrack(info string) error {
	if cdb.diff != nil {
		return nil
	}
	if cdb.shutdownTracker != nil {
		return cdb.shutdownTracker.Wait(info)
	}
	return nil
}

func (cdb *ChainDB) StartBlockApplyTrack(blockHash string, previousBestHash string, previous shutdown.DatasetState) error {
	if cdb.diff != nil {
		return nil
	}
	if cdb.shutdownTracker != nil {
		return cdb.shutdownTracker.WaitBlockApplyState(blockHash, previousBestHash, previous)
	}
	return nil
}

func newShutdownTracker(datadir string) (*shutdown.Tracker, error) {
	identity, present, err := dataset.LoadContentIdentity(datadir)
	if err != nil {
		return nil, fmt.Errorf("load dataset identity for recovery markers: %w", err)
	}
	if !present {
		return shutdown.NewTracker(datadir), nil
	}
	return shutdown.NewTrackerWithDatasetState(datadir, shutdown.DatasetState{
		DatasetRootSHA256: identity.DirectoryRootSHA256,
		ManifestSHA256:    identity.ManifestSHA256,
	}), nil
}

func (cdb *ChainDB) StopTrack() error {
	if cdb.diff != nil {
		return nil
	}
	if cdb.shutdownTracker != nil {
		return cdb.shutdownTracker.Done()
	}
	return nil
}

func (cdb *ChainDB) PendingBlockApplyRecovery() (string, bool, error) {
	if cdb.shutdownTracker == nil {
		return "", false, nil
	}
	marker, pending, err := cdb.shutdownTracker.Pending()
	if err != nil {
		return "", false, err
	}
	if !pending {
		return "", false, nil
	}
	return marker.Description(), true, nil
}

// ClassifyBlockApplyRecovery validates the pending marker against the opened
// chain state. Foreign markers are moved aside atomically and therefore never
// participate in recovery or marker completion.
func (cdb *ChainDB) ClassifyBlockApplyRecovery(current shutdown.DatasetState) (string, shutdown.MarkerClassification, error) {
	if cdb.shutdownTracker == nil {
		return "", "", nil
	}
	marker, pending, err := cdb.shutdownTracker.Pending()
	if err != nil {
		return "", "", err
	}
	if !pending {
		cdb.recoveryMu.Lock()
		cdb.recoveryMarker = nil
		cdb.recoveryClass = ""
		cdb.recoveryMu.Unlock()
		return "", "", nil
	}
	identity := cdb.shutdownTracker.DatasetIdentity()
	if current.DatasetRootSHA256 == "" {
		current.DatasetRootSHA256 = identity.DatasetRootSHA256
	}
	if current.ManifestSHA256 == "" {
		current.ManifestSHA256 = identity.ManifestSHA256
	}
	classification := shutdown.ClassifyMarker(marker, current)
	if classification == shutdown.MarkerForeign {
		if err := cdb.shutdownTracker.QuarantineForeign(marker); err != nil {
			return marker.Description(), classification, fmt.Errorf("quarantine foreign recovery marker: %w", err)
		}
		cdb.recoveryMu.Lock()
		cdb.recoveryMarker = nil
		cdb.recoveryClass = classification
		cdb.recoveryMu.Unlock()
		return marker.Description(), classification, nil
	}
	markerCopy := *marker
	cdb.recoveryMu.Lock()
	cdb.recoveryMarker = &markerCopy
	cdb.recoveryClass = classification
	cdb.recoveryMu.Unlock()
	return marker.Description(), classification, nil
}

func (cdb *ChainDB) CompleteBlockApplyRecovery() error {
	if cdb.shutdownTracker == nil {
		return nil
	}
	cdb.recoveryMu.Lock()
	if cdb.recoveryClass != shutdown.MarkerApplicable || cdb.recoveryMarker == nil {
		cdb.recoveryMu.Unlock()
		return nil
	}
	marker := *cdb.recoveryMarker
	cdb.recoveryMu.Unlock()
	log.Warn("Chain database validated after unclean block application; clearing recovery marker", "marker", marker.Description())
	if err := cdb.shutdownTracker.DoneExpected(&marker); err != nil {
		return err
	}
	cdb.recoveryMu.Lock()
	cdb.recoveryMarker = nil
	cdb.recoveryClass = ""
	cdb.recoveryMu.Unlock()
	return nil
}

func New(cfg *config.Config) (*ChainDB, error) {
	cdb := &ChainDB{
		cfg:       cfg,
		databases: make(map[*closeTrackingDB]struct{}),
		hasInit:   bdag.Exist(cfg),
		ancient:   true,
	}
	if len(cfg.DataDir) > 0 {
		tracker, trackerErr := newShutdownTracker(cfg.DataDir)
		if trackerErr != nil {
			return nil, trackerErr
		}
		cdb.shutdownTracker = tracker
	}
	cdb.closedState.Store(false)

	var err error
	cdb.db, err = cdb.OpenDatabaseWithFreezer(DBDirectoryName, cfg.DatabaseCache(),
		utils.MakeDatabaseHandles(0), "", "/", false, false)
	if err != nil {
		return nil, err
	}
	if cdb.shutdownTracker != nil {
		err = cdb.shutdownTracker.Check()
		if err != nil {
			return nil, err
		}
	}
	return cdb, nil
}

// NewForSnapshotImport opens the chain DB like New but disables the background
// chain freezer during import. Snapshot import writes KV first then replays
// frozen segments; leaving the freezer goroutine enabled causes the freezer
// to fill from KV and later replay fails with out-of-order append errors.
func NewForSnapshotImport(cfg *config.Config) (*ChainDB, error) {
	cdb := &ChainDB{
		cfg:       cfg,
		databases: make(map[*closeTrackingDB]struct{}),
		hasInit:   bdag.Exist(cfg),
		ancient:   true,
	}
	if len(cfg.DataDir) > 0 {
		var err error
		cdb.shutdownTracker, err = newShutdownTracker(cfg.DataDir)
		if err != nil {
			return nil, err
		}
	}
	cdb.closedState.Store(false)

	var err error
	cdb.db, err = cdb.OpenDatabaseWithFreezer(DBDirectoryName, cfg.DatabaseCache(),
		utils.MakeDatabaseHandles(0), "", "/", false, true)
	if err != nil {
		return nil, err
	}
	if cdb.shutdownTracker != nil {
		err = cdb.shutdownTracker.Check()
		if err != nil {
			return nil, err
		}
	}
	return cdb, nil
}

// NewForExport opens the chain database for offline snapshot export. Unlike
// New, it refuses to create a fresh chaindata directory: if --datadir points
// at the wrong path, Open fails with ErrDBAbsent instead of silently
// materializing an empty database.
func NewForExport(cfg *config.Config) (*ChainDB, error) {
	prev := CreateIfNoExist
	CreateIfNoExist = false
	defer func() { CreateIfNoExist = prev }()
	return New(cfg)
}

func NewNaked(cfg *config.Config) (*ChainDB, error) {
	cdb := &ChainDB{
		cfg:       cfg,
		databases: make(map[*closeTrackingDB]struct{}),
		hasInit:   false,
	}
	cdb.closedState.Store(false)

	var err error
	cdb.db, err = cdb.OpenDatabaseWithFreezer(DBDirectoryName, cfg.DatabaseCache(),
		utils.MakeDatabaseHandles(0), "", "/", false, false)
	if err != nil {
		return nil, err
	}
	return cdb, nil
}
