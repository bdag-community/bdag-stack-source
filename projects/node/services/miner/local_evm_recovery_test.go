package miner

import (
	"context"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
)

type heldRecoveryChain struct{ model.BlockChain }

func (heldRecoveryChain) LocalEVMRecoveryStatus() (bool, string) {
	return true, "exact local EVM anchor state is unavailable"
}
func (heldRecoveryChain) GetMainOrder() uint { return 0 }
func (heldRecoveryChain) IsCurrent() bool    { return true }

type heldRecoveryConsensus struct{ model.Consensus }

func (heldRecoveryConsensus) BlockChain() model.BlockChain { return heldRecoveryChain{} }

func TestLocalEVMRecoveryBlocksEveryMiningEntryPoint(t *testing.T) {
	m := &Miner{consensus: heldRecoveryConsensus{}, cfg: &config.Config{SubmitNoSynced: true, AllowMiningWhenNearlySynced: true}}
	readiness := m.miningRPCReadiness()
	if readiness.allowed || readiness.getTemplateReady || readiness.canonicalBackendReady || readiness.canonicalBypassActive {
		t.Fatal("recovery hold was bypassed by mining policy")
	}
	if err := m.CanMining("test"); err == nil {
		t.Fatal("submission readiness accepted hold")
	}
	if err := m.CanBuildTemplate("test"); err == nil {
		t.Fatal("GBT readiness accepted hold")
	}
	if m.canonicalMiningTemplateBypassAllowed(time.Now()) {
		t.Fatal("canonical bypass accepted hold")
	}
	result, err := m.submitBlockContext(context.Background(), nil)
	if err == nil {
		t.Fatal("direct submitBlock bypassed hold")
	}
	if result.Code != submitOutcomeNodeSyncing || result.Final || !result.Retryable {
		t.Fatalf("local hold produced a terminal or wrong submission outcome: %+v", result)
	}
}
