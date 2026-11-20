// Copyright (c) 2024 The bdag developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package chaindb

import (
	"fmt"
	"os"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
	"github.com/ethereum/go-ethereum/common"
)

// Open opens (or initialises) the chain database for the given config.
//
// The legacy FFLDB / legacychaindb code path was removed; chaindb is the
// only supported backend. When cfg.Cleanup or cfg.CleanupTips is set,
// Open still performs the requested housekeeping and then returns nil so
// callers don't accidentally treat the cleanup invocation as a normal open.
func Open(cfg *config.Config, interrupt <-chan struct{}) (model.DataBase, error) {
	if cfg.Cleanup {
		Cleanup(cfg)
		return nil, nil
	}
	if cfg.CleanupTips {
		db, err := New(cfg)
		if err != nil {
			log.Error(err.Error())
			return nil, err
		}
		CleanupTips(db)
		ReconstructTips(db)
		db.Close()
		return nil, nil
	}
	return New(cfg)
}

// Cleanup wipes the chain database and the embedded EVM data so the data
// directory can be re-initialised from scratch (e.g. before importing a
// snapshot).
func Cleanup(cfg *config.Config) {
	dbPaths := []string{cfg.ResolveDataPath(DBDirectoryName)}
	for _, dbPath := range dbPaths {
		if util.FileExists(dbPath) {
			if err := removeDB(dbPath); err != nil {
				log.Error(err.Error())
			}
		}
	}

	bdag.Cleanup(cfg)
	if err := shutdown.NewTracker(cfg.DataDir).Done(); err != nil {
		log.Error(err.Error())
	}
	log.Info("Finished cleanup")
}

// removeDB removes the existing database at dbPath.
func removeDB(dbPath string) error {
	fi, err := os.Stat(dbPath)
	if err == nil {
		log.Info(fmt.Sprintf("Removing block database from '%s'", dbPath))
		if fi.IsDir() {
			return os.RemoveAll(dbPath)
		}
		return os.Remove(dbPath)
	}
	return nil
}

// CleanupTips removes any DAG tips whose backing block data is missing.
func CleanupTips(db model.DataBase) {
	log.Info("Start cleanup tips...")
	tips, err := db.GetDAGTips()
	if err != nil {
		log.Error(err.Error())
		return
	}

	count := 0
	deleted := 0
	for _, tip := range tips {
		count++
		data, _ := db.GetDAGBlock(tip)
		if data == nil {
			log.Warn("Tip is missing block data, removing...", "tip", tip)
			if err := db.DeleteDAGTip(tip); err != nil {
				log.Error("Failed to delete tip", "tip", tip, "error", err)
			} else {
				deleted++
			}
		}
	}
	log.Info("Finished cleanup tips", "total", count, "deleted", deleted)
}

// ReconstructTips reuses the tips stored in the chain database, defaulting
// the main tip from the persisted best-chain state when possible.
//
// chaindb keeps an authoritative tip set so the rebuild now just normalises
// the existing list and ensures a designated main tip.
func ReconstructTips(db model.DataBase) {
	log.Info("Start reconstructing DAG tips...")

	mainTipID := uint(blockdag.MaxId)
	if serialized, err := db.GetBestChainState(); err != nil {
		log.Warn("Failed to read best chain state", "error", err)
	} else if serialized != nil {
		// The tip hash occupies the first 32 bytes of the serialized
		// best-chain state (see blockchain.serializeBestChainState).
		if len(serialized) >= common.HashLength {
			var tipHash common.Hash
			copy(tipHash[:], serialized[:common.HashLength])
			if id, err := db.GetDAGBlockIdByHash(&tipHash); err != nil {
				log.Warn("Failed to map best chain hash to block id", "hash", tipHash, "error", err)
			} else {
				mainTipID = id
			}
		}
	}

	tips, err := db.GetDAGTips()
	if err != nil {
		log.Error("Failed to fetch tips", "error", err)
		return
	}
	if len(tips) == 0 {
		log.Warn("No tips found; cannot reconstruct")
		return
	}
	if mainTipID == blockdag.MaxId {
		mainTipID = tips[0]
	}
	rewriteTips(db, tips, mainTipID)
}

// rewriteTips clears and writes the tips list with the provided main tip id.
func rewriteTips(db model.DataBase, tips []uint, mainTipID uint) {
	if len(tips) == 0 {
		log.Warn("No tips found during reconstruction! Chain might be empty or corrupted.")
		return
	}
	exTips, _ := db.GetDAGTips()
	for _, t := range exTips {
		db.DeleteDAGTip(t)
	}

	for _, tip := range tips {
		if err := db.PutDAGTip(tip, tip == mainTipID); err != nil {
			log.Error("Failed to put tip", "tip", tip, "error", err)
		}
	}
	log.Info("Successfully gathered reconstructed tips")
}
