package blockchain

import (
	"errors"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/ethdb"
)

type failUpgradeBatch struct{ ethdb.Batch }

func (b failUpgradeBatch) Write() error { return errors.New("injected batch failure") }

type failUpgradeRawDB struct{ ethdb.Database }

func (d failUpgradeRawDB) NewBatch() ethdb.Batch { return failUpgradeBatch{d.Database.NewBatch()} }

type failUpgradeChainDB struct {
	model.DataBase
	raw ethdb.Database
}

func (d failUpgradeChainDB) DB() ethdb.Database { return failUpgradeRawDB{d.raw} }

func TestUpgradeCheckpointCommitIsAtomicOnWriteFailure(t *testing.T) {
	db, err := chaindb.New(&config.Config{DataDir: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{"vote": {Name: "vote", BitMask: 1, WindowSize: 4, Threshold: 3}})
	defer upgrades.ReplaceUpgrades(old)
	upgrades.RegisterPersist(nil)
	bc := &BlockChain{consensus: &mockConsensus{db: db, params: &params.Params{}}}
	initial := []upgradeRebuildResult{{Name: "vote"}}
	marker := upgradeRebuildState{Version: upgradeRebuildStateVersion, TipID: 1, TipLayer: 10, DefinitionsHash: upgradeDefinitionsHash()}
	if err := bc.commitUpgradeResults(marker, initial); err != nil {
		t.Fatal(err)
	}
	markerBefore, resultsBefore, ok := bc.loadUpgradeCheckpoint()
	if !ok || len(resultsBefore) != 1 {
		t.Fatal("initial checkpoint could not be loaded")
	}
	failed := &BlockChain{consensus: &mockConsensus{db: failUpgradeChainDB{DataBase: db, raw: db.DB()}, params: &params.Params{}}}
	next := []upgradeRebuildResult{{Name: "vote", LockedIn: true, AnchorSet: true, ThresholdAt: 11, ActivateAnchor: 11, Active: true}}
	marker.TipID = 2
	if err := failed.commitUpgradeResults(marker, next); err == nil {
		t.Fatal("expected batch failure")
	}
	markerAfter, resultsAfter, ok := bc.loadUpgradeCheckpoint()
	if !ok || *markerAfter != *markerBefore || len(resultsAfter) != 1 || resultsAfter[0] != resultsBefore[0] {
		t.Fatal("failed batch changed persisted checkpoint")
	}
	if upgrades.GetUpgrades()["vote"].LockedIn {
		t.Fatal("failed batch changed visible state")
	}
}
