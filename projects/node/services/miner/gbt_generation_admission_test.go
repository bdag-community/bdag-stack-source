package miner

import (
	"github.com/BlockdagNetworkLabs/bdag/config"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"testing"
	"time"
)

func TestGBTRequiredBuildFailureCannotPublishRetainedGeneration(t *testing.T) {
	for _, asynchronous := range []bool{false, true} {
		name := "pending-request"
		if asynchronous {
			name = "chain-message"
		}
		t.Run(name, func(t *testing.T) {
			m := newRemoteGBTResultTestMiner()
			prepareFeeTestMiner(m)
			m.cfg = &config.Config{Miner: true}
			old := m.template
			worker := NewGBTWorker(m)
			m.worker = worker
			if _, err := worker.getResult(nil); err != nil {
				t.Fatal(err)
			}
			m.msgChan = make(chan any)
			m.templateReqChan = make(chan struct{}, 1)
			m.quit = make(chan struct{})
			if !asynchronous {
				m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, force: true, reason: "required parent rebuild", invalidationCause: templateInvalidationParentChanged})
				<-m.templateReqChan
			}
			m.wg.Add(1)
			done := make(chan struct{})
			go func() { m.handler(); close(done) }()
			defer func() {
				close(m.quit)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("handler did not stop")
				}
			}()
			if asynchronous {
				m.msgChan <- &BlockChainChangeMsg{}
			}
			for i := 0; i < 2; i++ {
				reply := make(chan *gbtResponse, 1)
				m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: reply}
				select {
				case response := <-reply:
					if response == nil || response.err == nil || response.result != nil {
						t.Errorf("request%d published retained generation after required rebuild failed", i)
					}
				case <-time.After(time.Second):
					t.Fatal("request timed out")
				}
			}
			if m.template != old {
				t.Error("failed rebuild must preserve template for submit classification")
			}
		})
	}
}
