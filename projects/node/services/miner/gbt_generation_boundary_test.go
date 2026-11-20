package miner

import (
	"errors"
	"github.com/BlockdagNetworkLabs/bdag/config"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"testing"
	"time"
)

func TestGBTBenignMempoolBuildFailurePreservesGeneration(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	prepareFeeTestMiner(m)
	m.cfg = &config.Config{Miner: true}
	m.worker = NewGBTWorker(m)
	m.msgChan = make(chan any)
	m.templateReqChan = make(chan struct{}, 1)
	m.quit = make(chan struct{})
	m.wg.Add(1)
	done := make(chan struct{})
	go func() { m.handler(); close(done) }()
	t.Cleanup(func() {
		close(m.quit)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("handler did not join")
		}
	})
	m.msgChan <- &MempoolChangeMsg{}
	reply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: reply}
	select {
	case result := <-reply:
		if result == nil || result.err != nil || result.result == nil {
			t.Fatal("benign mempool failure poisoned retained generation")
		}
	case <-time.After(time.Second):
		t.Fatal("reply timed out")
	}
	if m.gbtGenerationError() != nil || m.lastTemplateBuildErrorCode == "" {
		t.Fatal("actual failed mempool build was not observed or incorrectly held publication")
	}
}
func TestGBTGenerationHoldCannotBypassLocalRecovery(t *testing.T) {
	m := &Miner{consensus: heldRecoveryConsensus{}, cfg: &config.Config{SubmitNoSynced: true, AllowMiningWhenNearlySynced: true}}
	m.recordGBTGenerationFailure(errors.New("synthetic required failure"))
	r := m.miningRPCReadiness()
	if r.buildTemplateReady || r.getTemplateReady || r.allowed || r.canonicalBypassActive || !r.generationPublishHold || r.getTemplateCode != templateHealthNodeSyncing {
		t.Fatal("generation recovery relaxed local EVM guard")
	}
	if m.CanBuildTemplate("fixture") == nil {
		t.Fatal("replacement build bypassed local recovery")
	}
}

func TestGBTFailedPowRequestRestoresModeAndRetainedTemplate(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	prepareFeeTestMiner(m)
	m.cfg = &config.Config{Miner: true}
	m.powType = pow.SCRYPT
	old := m.template
	worker := NewGBTWorker(m)
	m.worker = worker
	reply := make(chan *gbtResponse, 1)
	worker.GetRequest(&corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD)}, reply)
	result := <-reply
	if result == nil || result.err == nil || result.result != nil || m.powType != pow.SCRYPT || m.template != old {
		t.Fatal("failed optional PoW request altered live mode or template")
	}
	if m.gbtGenerationError() != nil {
		t.Fatal("optional rejected PoW request poisoned required generation")
	}
	if !worker.TryLock() {
		t.Fatal("failed request retained worker mutex")
	}
	worker.Unlock()
	worker.GetRequest(&corejson.TemplateRequest{PowType: byte(pow.SCRYPT)}, reply)
	result = <-reply
	if result == nil || result.err != nil || result.result == nil {
		t.Fatal("original mode did not remain available")
	}
}
