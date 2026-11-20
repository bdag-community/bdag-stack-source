package chaos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum/ethdb"
)

// RunChaos executes the specified chaos mode on the database.
func RunChaos(datadir string, mode string) error {
	log.Info("Running chaos", "mode", mode, "datadir", datadir)

	// Ensure datadir exists
	if _, err := os.Stat(datadir); os.IsNotExist(err) {
		return fmt.Errorf("datadir does not exist: %s", datadir)
	}

	// Check if ancient folder exists to pass to opener
	ancient := filepath.Join(datadir, chaindb.DBDirectoryName, "ancient")

	// Open DB directly using rawdb
	dbPath := filepath.Join(datadir, chaindb.DBDirectoryName)
	db, err := rawdb.Open(rawdb.OpenOptions{
		Type:              "pebble",
		Directory:         dbPath,
		AncientsDirectory: ancient,
		Namespace:         "",
		Cache:             16,
		Handles:           16,
		ReadOnly:          false,
	})
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	switch mode {
	case "tip-delete":
		return corruptedTip(db)
	case "state-corrupt":
		return corruptedState(db)
	default:
		return fmt.Errorf("unknown mode: %s", mode)
	}
}

func corruptedState(db ethdb.Database) error {
	log.Info("Corrupting best chain state...")
	// Write garbage to the best chain state key
	// We need rawdb.WriteBestChainState but with garbage.
	// rawdb doesn't expose the key directly usually, but we can write using rawdb.WriteBestChainState if we pass garbage bytes?
	// attempting to pass invalid RLP bytes.
	garbage := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	// relying on rawdb.WriteBestChainState taking []byte?
	// rawdb.WriteBestChainState takes (db, []byte).
	if err := rawdb.WriteBestChainState(db, garbage); err != nil {
		return err
	}
	log.Info("Successfully wrote garbage to best chain state.")
	return nil
}

func corruptedTip(db ethdb.Database) error {
	// 1. Read Main Tip
	tipID := rawdb.ReadMainChainTip(db)
	if tipID == nil {
		return errors.New("main chain tip not found in DB")
	}

	log.Info("Current main chain tip", "id", *tipID)

	// 2. Read the DAG block hash directly (avoids decoding issues)
	hash, err := rawdb.ReadBlockHashByID(db, *tipID)
	if err != nil {
		return fmt.Errorf("read block hash failed: %w", err)
	}
	if hash == nil {
		return fmt.Errorf("dag block missing for id %d", *tipID)
	}

	log.Info("Deleting tip block", "hash", hash.String())

	// 3. Delete the block body and header, but KEEP the metadata (tips, main chain pointer)
	batch := db.NewBatch()

	rawdb.DeleteHeader(batch, hash)
	rawdb.DeleteBody(batch, hash)
	rawdb.DeleteDAGBlock(batch, *tipID)

	if err := batch.Write(); err != nil {
		return err
	}

	log.Info("Successfully deleted tip block data. Node should now fail with 'tip not found'.")
	return nil
}
