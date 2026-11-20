package blockchain

import (
	"errors"
	"fmt"
	bdag "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	dag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"strings"
	"testing"
)

func TestEVMNativeCatchupRetryableErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "busy",
			err:  errEVMNativeRebuildBusy,
			want: true,
		},
		{
			name: "wrapped stale anchor",
			err:  fmt.Errorf("retry later: %w", errEVMNativeRebuildStaleAnchor),
			want: true,
		},
		{
			name: "uncommitted history",
			err:  errEVMNativeCatchupUncommitted,
			want: true,
		},
		{
			name: "real failure",
			err:  errors.New("invalid merkle root"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEVMNativeCatchupRetryable(tt.err); got != tt.want {
				t.Fatalf("isEVMNativeCatchupRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type pathCatchupConsensus struct {
	mockConsensus
	cfg *config.Config
}

func (c *pathCatchupConsensus) Config() *config.Config { return c.cfg }

// Automatic replay may enter the PathDB route now that durable checkpoints
// preserve live diff layers. This deliberately incomplete fixture can fail at
// its missing native anchor, but never at the retired retention gate.
func TestEVMNativeCatchupAllowsPathRecoveryRoute(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), StateScheme: "path", DebugLevel: "error", EVMEnv: "--cache 64 --db.engine pebble"}
	evm, err := bdag.NewBdagChain(&pathCatchupConsensus{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer evm.ETHChain().Stop()
	db, err := chaindb.New(&config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bd := dag.New(dag.PHANTOM, -1, db, nil)
	_, _, tip, _ := bd.AddBlock(rollbackTestBlockData{hash: ethcommon.HexToHash("0x21"), timestamp: 1})
	if tip == nil {
		t.Fatal("no native fixture tip")
	}
	head := evm.GetCurHeader()
	tip.GetState().SetEVM(&gethtypes.Header{Number: new(big.Int).Add(head.Number, big.NewInt(1))})
	bc := &BlockChain{bd: bd, bdagChain: evm}
	err = bc.runEVMNativeCatchupBatch()
	if err != nil && strings.Contains(err.Error(), "PathDB checkpoint retention") {
		t.Fatalf("automatic Path replay stopped at retired retention gate: %v", err)
	}
	if isEVMStateGuardRuleError(err) {
		t.Fatal("local storage limitation classified as peer violation")
	}
	if evm.GetCurHeader().Hash() != head.Hash() {
		t.Fatal("retention gate moved EVM head")
	}
}
