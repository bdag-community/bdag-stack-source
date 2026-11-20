package miner

import (
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
)

func TestGBTRequestSuccessfulRebuildDoesNotReenterWorkerLock(t *testing.T) {
	for _, mode := range []string{"pow-change", "pay-address-change"} {
		t.Run(mode, func(t *testing.T) {
			m := newRemoteGBTResultTestMiner()
			m.consensus = newGBTGenerationRuntimeConsensus(t)
			m.cfg = &config.Config{Miner: true}
			m.policy = &mining.Policy{PeerID: "test-peer"}
			m.timeSource = m.consensus.MedianTimeSource()
			m.txpool = &legacypool.LegacyPool{}
			m.bdagPool = &testPendingTemplateResetter{}
			m.powType = pow.SCRYPT
			old := m.template
			requested := m.coinbaseAddress
			request := &corejson.TemplateRequest{PowType: byte(pow.SCRYPT)}
			if mode == "pow-change" {
				m.powType = pow.BLAKE2BD
			} else {
				// Replace an obsolete template address with the configured payout.
				// Publication must still reject an unconfigured payout address.
				old.Block.Header.CoinbaseAddress = common.HexToAddress("0x1234")
				request.PayAddress = requested.Hex()
			}
			builds := 0
			m.buildTemplate = func(address common.Address, _ bool) (*coretypes.BlockTemplate, error) {
				builds++
				return newGBTGenerationRuntimeTemplate(t, m.consensus, address), nil
			}
			worker := NewGBTWorker(m)
			m.worker = worker // Exercises the real nested callback, not an unowned fixture.
			reply := make(chan *gbtResponse, 1)
			done := make(chan struct{})
			go func() { worker.GetRequest(request, reply); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("GetRequest deadlocked rebuilding with worker mutex held")
			}
			response := <-reply
			if response.err != nil || response.result == nil {
				t.Fatalf("rebuild response error: %v; result: %#v", response.err, response.result)
			}
			if builds != 1 || m.template == old {
				t.Fatalf("replacement not built exactly once: %d", builds)
			}
			if m.template.Block.Header.CoinbaseAddress != requested {
				t.Fatal("requested coinbase was not preserved")
			}
			if m.gbtGenerationError() != nil || worker.generationFailure != nil {
				t.Fatal("valid replacement remained publication-held")
			}
			if !worker.TryLock() {
				t.Fatal("GetRequest did not release worker mutex")
			}
			worker.Unlock()
		})
	}
}
