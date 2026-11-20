package consensus

import (
	"path/filepath"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
)

func TestAloneConsensus(t *testing.T) {
	cfg := common.DefaultConfig(t.TempDir())
	cfg.NoFileLogging = true
	cfg.DataDir = util.CleanAndExpandPath(cfg.DataDir)
	cfg.DataDir = filepath.Join(cfg.DataDir, params.ActiveNetParams.Name)
	//
	db, err := chaindb.Open(cfg, system.InterruptListener())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cons := NewPure(cfg, db)
	err = cons.Init()
	if err != nil {
		t.Error(err)
	}
}
