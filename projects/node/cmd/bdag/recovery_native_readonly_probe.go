package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	dag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/dataset"
	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum/ethdb"
)

type nativeReadonlyProbeOptions struct {
	ChainDB              string
	ExpectGenesisHash    string
	ExpectOrder          uint64
	ExpectOrderSet       bool
	ExpectHash           string
	ExpectStateRoot      string
	ExpectEVMNumber      uint64
	ExpectEVMNumberSet   bool
	ExpectEVMHash        string
	ExpectEVMRoot        string
	RequireCleanShutdown bool
	PrintJSON            bool
}

type nativeReadonlyProbeResult struct {
	ChainDB                  string   `json:"chain_db"`
	DatabaseFormat           string   `json:"database_format,omitempty"`
	GenesisHash              string   `json:"genesis_hash,omitempty"`
	GenesisHashKnown         bool     `json:"genesis_hash_known"`
	BestStateHash            string   `json:"best_state_hash"`
	BestStateTotal           uint64   `json:"best_state_total"`
	MainTipID                uint64   `json:"main_tip_id"`
	MainTipHash              string   `json:"main_tip_hash"`
	MainTipOrder             uint64   `json:"main_tip_order"`
	MainTipHeight            uint64   `json:"main_tip_height"`
	MainTipLayer             uint64   `json:"main_tip_layer"`
	MainTipStateRoot         string   `json:"main_tip_state_root"`
	MainTipEVMNumber         uint64   `json:"main_tip_evm_number"`
	MainTipEVMHash           string   `json:"main_tip_evm_hash"`
	MainTipEVMRoot           string   `json:"main_tip_evm_root"`
	Tips                     []uint64 `json:"tips"`
	TipCount                 int      `json:"tip_count"`
	BestHashBlockID          uint64   `json:"best_hash_block_id,omitempty"`
	BestHashBlockIDKnown     bool     `json:"best_hash_block_id_known"`
	OrderIndexBlockID        uint64   `json:"order_index_block_id,omitempty"`
	OrderIndexBlockIDKnown   bool     `json:"order_index_block_id_known"`
	BestStateMatchesMainTip  bool     `json:"best_state_matches_main_tip"`
	OrderIndexMatchesMainTip bool     `json:"order_index_matches_main_tip"`
	ShutdownMarkerPending    bool     `json:"shutdown_marker_pending"`
	ShutdownMarkerForeign    bool     `json:"shutdown_marker_foreign"`
	ShutdownMarkerMalformed  bool     `json:"shutdown_marker_malformed"`
	ShutdownMarkerClass      string   `json:"shutdown_marker_classification,omitempty"`
	ShutdownMarker           string   `json:"shutdown_marker,omitempty"`
	DurableNative            bool     `json:"durable_native"`
}

func runNativeReadonlyHeadProbe(opts nativeReadonlyProbeOptions) error {
	db, chainDB, datadir, closeFn, err := openReadonlyNativeProbeDB(opts.ChainDB)
	if err != nil {
		return err
	}
	defer closeFn()

	result, err := inspectNativeReadonlyHead(db, chainDB, datadir)
	if err != nil {
		return err
	}
	reasons := validateNativeReadonlyProbe(result, opts)
	result.DurableNative = len(reasons) == 0
	if opts.PrintJSON {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("encode read-only native probe: %w", err)
		}
		fmt.Println(string(encoded))
	}
	log.Info("Read-only native chain probe",
		"durableNative", result.DurableNative,
		"order", result.MainTipOrder,
		"hash", result.MainTipHash,
		"stateRoot", result.MainTipStateRoot,
		"evmNumber", result.MainTipEVMNumber,
		"evmHash", result.MainTipEVMHash,
		"evmRoot", result.MainTipEVMRoot,
		"tips", result.TipCount)
	if len(reasons) > 0 {
		return fmt.Errorf("read-only native probe failed: %s", strings.Join(reasons, "; "))
	}
	return nil
}

func openReadonlyNativeProbeDB(chainDB string) (ethdb.Database, string, string, func(), error) {
	cfg := config.Cfg
	chainDBFromFlag := chainDB != ""
	dbType := ""
	datadir := ""
	if cfg != nil {
		datadir = cfg.DataDir
		if !chainDBFromFlag {
			dbType = cfg.DbType
			if dbType == "ffldb" {
				dbType = "pebble"
			}
		}
	}
	if chainDB == "" {
		if cfg == nil {
			return nil, "", "", nil, fmt.Errorf("config is unavailable")
		}
		chainDB = cfg.ResolveDataPath(chaindb.DBDirectoryName)
	}
	if abs, err := filepath.Abs(chainDB); err == nil {
		chainDB = abs
	}
	if datadir == "" {
		datadir = filepath.Dir(chainDB)
	}
	db, err := bdagrawdb.Open(bdagrawdb.OpenOptions{
		Type:                 dbType,
		Directory:            chainDB,
		AncientsDirectory:    filepath.Join(chainDB, "ancient"),
		Namespace:            "/",
		Cache:                128,
		Handles:              512,
		ReadOnly:             true,
		SkipBackgroundFreeze: true,
	})
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("open read-only native chain DB %s: %w", chainDB, err)
	}
	return db, chainDB, datadir, func() { db.Close() }, nil
}

func inspectNativeReadonlyHead(db ethdb.Database, chainDB string, datadir string) (*nativeReadonlyProbeResult, error) {
	dag.SetBlockStateFactory(state.CreateBlockState, state.CreateBlockStateFromBytes)

	serialized := bdagrawdb.ReadBestChainState(db)
	if serialized == nil {
		return nil, fmt.Errorf("best-chain state is unavailable")
	}
	bestState, err := blockchain.DeserializeBestChainState(serialized)
	if err != nil {
		return nil, err
	}
	bestHash, err := blockchain.BestChainStateHash(serialized)
	if err != nil {
		return nil, err
	}
	tips := bdagrawdb.ReadDAGTips(db)
	if len(tips) == 0 {
		return nil, fmt.Errorf("DAG tips are unavailable")
	}
	if tips[0] == uint64(dag.MaxId) {
		return nil, fmt.Errorf("DAG tips do not contain a designated main tip")
	}
	mainTipID := tips[0]
	mainTip := bdagrawdb.ReadDAGBlock(db, mainTipID)
	if mainTip == nil {
		return nil, fmt.Errorf("DAG main tip block %d is unavailable", mainTipID)
	}
	mainTipState := mainTip.GetState()
	if mainTipState == nil {
		return nil, fmt.Errorf("DAG main tip block %d has no state", mainTipID)
	}

	result := &nativeReadonlyProbeResult{
		ChainDB:          chainDB,
		DatabaseFormat:   bdagrawdb.PreexistingDatabase(chainDB),
		BestStateHash:    bestHash.String(),
		BestStateTotal:   bestState.GetTotal(),
		MainTipID:        mainTipID,
		MainTipHash:      mainTip.GetHash().String(),
		MainTipOrder:     uint64(mainTip.GetOrder()),
		MainTipHeight:    uint64(mainTip.GetHeight()),
		MainTipLayer:     uint64(mainTip.GetLayer()),
		MainTipStateRoot: mainTipState.Root().String(),
		MainTipEVMNumber: mainTipState.GetEVMNumber(),
		MainTipEVMHash:   mainTipState.GetEVMHash().String(),
		MainTipEVMRoot:   mainTipState.GetEVMRoot().String(),
		Tips:             tips,
		TipCount:         len(tips),
	}
	if genesisID := bdagrawdb.ReadBlockOrderSnapshot(db, 0); genesisID != nil {
		if genesis := bdagrawdb.ReadDAGBlock(db, *genesisID); genesis != nil {
			result.GenesisHash = genesis.GetHash().String()
			result.GenesisHashKnown = true
		}
	}
	if bestHashID := bdagrawdb.ReadBlockID(db, &bestHash); bestHashID != nil {
		result.BestHashBlockID = *bestHashID
		result.BestHashBlockIDKnown = true
	}
	if orderID := bdagrawdb.ReadBlockOrderSnapshot(db, result.MainTipOrder); orderID != nil {
		result.OrderIndexBlockID = *orderID
		result.OrderIndexBlockIDKnown = true
	}
	result.BestStateMatchesMainTip = equalHex(result.BestStateHash, result.MainTipHash) &&
		result.BestHashBlockIDKnown && result.BestHashBlockID == result.MainTipID
	result.OrderIndexMatchesMainTip = result.OrderIndexBlockIDKnown && result.OrderIndexBlockID == result.MainTipID

	if datadir != "" {
		identity, _, identityErr := dataset.LoadContentIdentity(datadir)
		if identityErr != nil {
			return nil, fmt.Errorf("load dataset identity for recovery marker classification: %w", identityErr)
		}
		if marker, pending, err := shutdown.NewTracker(datadir).Pending(); err != nil {
			var malformed *shutdown.MalformedMarkerError
			if !errors.As(err, &malformed) {
				return nil, err
			}
			result.ShutdownMarkerPending = true
			result.ShutdownMarkerMalformed = true
			result.ShutdownMarkerClass = "malformed_marker"
			result.ShutdownMarker = malformed.Error()
		} else if pending {
			classification := shutdown.ClassifyMarker(marker, shutdown.DatasetState{
				DatasetRootSHA256: identity.DirectoryRootSHA256,
				ManifestSHA256:    identity.ManifestSHA256,
				NativeOrder:       result.MainTipOrder,
				NativeHash:        result.MainTipHash,
				EVMNumber:         result.MainTipEVMNumber,
				EVMHash:           result.MainTipEVMHash,
				EVMRoot:           result.MainTipEVMRoot,
			})
			result.ShutdownMarkerClass = string(classification)
			result.ShutdownMarkerPending = classification == shutdown.MarkerApplicable
			result.ShutdownMarkerForeign = classification == shutdown.MarkerForeign
			result.ShutdownMarker = marker.Description()
		}
	}
	return result, nil
}

func validateNativeReadonlyProbe(result *nativeReadonlyProbeResult, opts nativeReadonlyProbeOptions) []string {
	var reasons []string
	if opts.ExpectGenesisHash != "" && (!result.GenesisHashKnown || !equalHex(result.GenesisHash, opts.ExpectGenesisHash)) {
		reasons = append(reasons, fmt.Sprintf("native genesis hash %s differs from expected %s", result.GenesisHash, opts.ExpectGenesisHash))
	}
	if !result.BestStateMatchesMainTip {
		reasons = append(reasons, fmt.Sprintf("best-chain state hash/id does not match DAG main tip: best=%s bestIDKnown=%t bestID=%d tip=%s tipID=%d",
			result.BestStateHash, result.BestHashBlockIDKnown, result.BestHashBlockID, result.MainTipHash, result.MainTipID))
	}
	if !result.OrderIndexMatchesMainTip {
		reasons = append(reasons, fmt.Sprintf("order index for order %d does not point to DAG main tip id %d", result.MainTipOrder, result.MainTipID))
	}
	if opts.RequireCleanShutdown && result.ShutdownMarkerMalformed {
		reasons = append(reasons, fmt.Sprintf("malformed marker: %s", result.ShutdownMarker))
	} else if opts.RequireCleanShutdown && result.ShutdownMarkerPending {
		reasons = append(reasons, fmt.Sprintf("shutdown marker is pending: %s", result.ShutdownMarker))
	}
	if opts.ExpectOrderSet && result.MainTipOrder != opts.ExpectOrder {
		reasons = append(reasons, fmt.Sprintf("native order %d differs from expected %d", result.MainTipOrder, opts.ExpectOrder))
	}
	if opts.ExpectHash != "" && !equalHex(result.MainTipHash, opts.ExpectHash) {
		reasons = append(reasons, fmt.Sprintf("native hash %s differs from expected %s", result.MainTipHash, opts.ExpectHash))
	}
	if opts.ExpectStateRoot != "" && !equalHex(result.MainTipStateRoot, opts.ExpectStateRoot) {
		reasons = append(reasons, fmt.Sprintf("native state root %s differs from expected %s", result.MainTipStateRoot, opts.ExpectStateRoot))
	}
	if opts.ExpectEVMNumberSet && result.MainTipEVMNumber != opts.ExpectEVMNumber {
		reasons = append(reasons, fmt.Sprintf("native-recorded EVM number %d differs from expected %d", result.MainTipEVMNumber, opts.ExpectEVMNumber))
	}
	if opts.ExpectEVMHash != "" && !equalHex(result.MainTipEVMHash, opts.ExpectEVMHash) {
		reasons = append(reasons, fmt.Sprintf("native-recorded EVM hash %s differs from expected %s", result.MainTipEVMHash, opts.ExpectEVMHash))
	}
	if opts.ExpectEVMRoot != "" && !equalHex(result.MainTipEVMRoot, opts.ExpectEVMRoot) {
		reasons = append(reasons, fmt.Sprintf("native-recorded EVM root %s differs from expected %s", result.MainTipEVMRoot, opts.ExpectEVMRoot))
	}
	return reasons
}
