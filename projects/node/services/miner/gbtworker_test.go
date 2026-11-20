package miner

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestGBTWorkerSuccessfulRequestedAddressRebuildReturns(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.cfg = &config.Config{Miner: true}
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minimumMedianTimeFn = func() time.Time { return time.Unix(1_770_000_000, 0) }
	m.txpool = &legacypool.LegacyPool{}
	m.bdagPool = &testPendingTemplateResetter{}
	m.powType = pow.BLAKE2BD
	w := NewGBTWorker(m)
	m.worker = w
	configured := m.coinbaseAddress
	requested := common.HexToAddress("0x1234")
	m.buildTemplate = func(address common.Address, _ bool) (*coretypes.BlockTemplate, error) {
		replacement := *newRemoteGBTResultTestMiner().template
		replacement.Block.Header.CoinbaseAddress = address
		return &replacement, nil
	}

	reply := make(chan *gbtResponse, 1)
	go w.GetRequest(&corejson.TemplateRequest{
		PowType: byte(pow.BLAKE2BD), PayAddress: requested.String(),
	}, reply)
	select {
	case response := <-reply:
		if response.err != nil || response.result == nil {
			t.Fatalf("successful rebuild response = %#v", response)
		}
		result := response.result.(*corejson.GetBlockTemplateResult)
		if result.CoinbaseAddress != requested || m.coinbaseAddress != configured {
			t.Fatal("requested payout was lost or changed the configured default")
		}
	case <-time.After(time.Second):
		t.Fatal("successful requested-address rebuild deadlocked before returning")
	}
}

func newRebuildingGBTWorker() (*Miner, *GBTWorker) {
	m := newRemoteGBTResultTestMiner()
	m.cfg = &config.Config{Miner: true}
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minimumMedianTimeFn = func() time.Time { return time.Unix(1_770_000_000, 0) }
	m.txpool = &legacypool.LegacyPool{}
	m.bdagPool = &testPendingTemplateResetter{}
	m.powType = pow.BLAKE2BD
	w := NewGBTWorker(m)
	m.worker = w
	m.buildTemplate = func(address common.Address, _ bool) (*coretypes.BlockTemplate, error) {
		replacement := *newRemoteGBTResultTestMiner().template
		replacement.Block.Header.CoinbaseAddress = address
		replacement.Block.Header.Pow = pow.GetInstance(m.powType, 0, 0, nil)
		return &replacement, nil
	}
	return m, w
}

func requestGBTWithinDeadline(t *testing.T, w *GBTWorker, request *corejson.TemplateRequest) *gbtResponse {
	t.Helper()
	reply := make(chan *gbtResponse, 1)
	go w.GetRequest(request, reply)
	select {
	case response := <-reply:
		return response
	case <-time.After(time.Second):
		t.Fatal("GBT request did not return")
		return nil
	}
}

func TestGBTWorkerSuccessfulRebuildSequencePreservesPayoutAndPoW(t *testing.T) {
	m, w := newRebuildingGBTWorker()
	configured := m.coinbaseAddress
	requested := common.HexToAddress("0x1234")
	steps := []struct {
		name    string
		request *corejson.TemplateRequest
		address common.Address
	}{
		{"explicit address", &corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD), PayAddress: requested.String()}, requested},
		{"repeat explicit", &corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD), PayAddress: requested.String()}, requested},
		{"restore default", &corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD)}, configured},
		{"switch proof of work", &corejson.TemplateRequest{PowType: byte(pow.BLAKE2S)}, configured},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			response := requestGBTWithinDeadline(t, w, step.request)
			if response.err != nil || response.result == nil {
				t.Fatalf("rebuild response = %#v", response)
			}
			result := response.result.(*corejson.GetBlockTemplateResult)
			if result.CoinbaseAddress != step.address || m.coinbaseAddress != configured || m.powType != pow.PowType(step.request.PowType) {
				t.Fatal("rebuild lost requested payout/PoW or mutated configured payout")
			}
		})
	}
}

func TestGBTWorkerGenerationGuardsStillRejectRebuildRequests(t *testing.T) {
	for _, minerGuard := range []bool{false, true} {
		m, w := newRebuildingGBTWorker()
		failure := errors.New("required generation is invalid")
		if minerGuard {
			m.recordGBTGenerationFailure(failure)
		} else {
			w.invalidateGeneration(failure)
		}
		builds := 0
		m.buildTemplate = func(common.Address, bool) (*coretypes.BlockTemplate, error) {
			builds++
			return nil, errors.New("must not build through generation hold")
		}
		response := requestGBTWithinDeadline(t, w, &corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD), PayAddress: m.coinbaseAddress.String()})
		if !errors.Is(response.err, failure) || response.result != nil || builds != 0 {
			t.Fatalf("generation guard bypassed: response=%#v builds=%d", response, builds)
		}
		// A guard return must release the cache mutex for external invalidation.
		done := make(chan struct{})
		go func() { w.Update(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("guard return retained worker mutex")
		}
	}
}

func TestMinerHandlerShutdownAfterSuccessfulRequestedAddressRebuild(t *testing.T) {
	m, w := newRebuildingGBTWorker()
	m.msgChan = make(chan any)
	m.quit = make(chan struct{})
	m.wg.Add(1)
	done := make(chan struct{})
	go func() { m.handler(); close(done) }()
	reply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD), PayAddress: m.coinbaseAddress.String()}, reply: reply}
	select {
	case response := <-reply:
		if response.err != nil || response.result == nil {
			t.Fatalf("handler rebuild response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("handler deadlocked during successful rebuild")
	}
	close(m.quit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish shutdown")
	}
	response := requestGBTWithinDeadline(t, w, &corejson.TemplateRequest{})
	if response.err == nil || response.result != nil {
		t.Fatal("stopped worker accepted a request")
	}
}

// constructorShapedFees mirrors mining.newBlockTemplate: Fees[0] is the
// negative total-fee offset, followed by one observation per transaction.
func constructorShapedFees(offset int64, perTransaction ...int64) []int64 {
	fees := make([]int64, 1, len(perTransaction)+1)
	fees[0] = offset
	return append(fees, perTransaction...)
}

func TestGBTEmptyDependenciesEncodeAsEmptyArray(t *testing.T) {
	payload, err := json.Marshal(corejson.GetBlockTemplateResultTx{
		Data:    "00",
		Hash:    "0x0",
		Depends: gbtEmptyTxDependencies,
	})
	if err != nil {
		t.Fatalf("marshal template tx: %v", err)
	}
	if !strings.Contains(string(payload), `"depends":[]`) {
		t.Fatalf("depends should encode as an empty array, got %s", payload)
	}
}

func TestGBTWorkerResultClonesAllMutableSlices(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
	parent := common.HexToHash("0x11")
	m.template.Block.Parents = []*common.Hash{&parent}
	to := common.HexToAddress("0x22")
	tx := gethtypes.NewTx(&gethtypes.LegacyTx{
		Nonce: 1, To: &to, Value: big.NewInt(1), Gas: 21000, GasPrice: big.NewInt(1),
	})
	m.template.Block.Transactions = gethtypes.Transactions{tx}
	m.template.Fees = constructorShapedFees(-7, 7)
	w := NewGBTWorker(m)

	first, err := w.getResult(nil)
	if err != nil {
		t.Fatalf("first getResult: %v", err)
	}
	payload, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal GBT result: %v", err)
	}
	if !strings.Contains(string(payload), `"depends":[]`) {
		t.Fatalf("depends should encode as an empty array, got %s", payload)
	}
	if first.Transactions[0].Depends == nil {
		t.Fatal("expected nonnil empty dependency slice")
	}

	first.Mutable[0] = "mutated"
	first.Capabilities[0] = "mutated"
	first.Parents[0].Hash = "mutated"
	first.Parents[0].Data = "mutated"
	first.Transactions[0].Data = "mutated"
	first.Transactions[0].Hash = "mutated"
	first.Transactions[0].Depends = append(first.Transactions[0].Depends, 7)

	second, err := w.getResult(nil)
	if err != nil {
		t.Fatalf("second getResult: %v", err)
	}
	if second.Mutable[0] == "mutated" || second.Capabilities[0] == "mutated" {
		t.Fatal("mutable metadata leaked into the next response")
	}
	if second.Parents[0].Hash == "mutated" || second.Parents[0].Data == "mutated" {
		t.Fatal("parent mutation leaked into the serialized cache")
	}
	if second.Transactions[0].Data == "mutated" || second.Transactions[0].Hash == "mutated" || len(second.Transactions[0].Depends) != 0 {
		t.Fatal("transaction mutation leaked into the serialized cache")
	}
	m.template.Fees = nil
	if _, err := w.getResult(nil); err == nil {
		t.Fatal("malformed transaction fee observations were accepted")
	}
	if w.cache != nil {
		t.Fatal("malformed fee observations retained serialized cache")
	}
}

func TestGBTWorkerResultUsesConstructorFeeShape(t *testing.T) {
	to := common.HexToAddress("0x44")
	tx1 := gethtypes.NewTx(&gethtypes.LegacyTx{
		Nonce: 1, To: &to, Value: big.NewInt(1), Gas: 21000, GasPrice: big.NewInt(1),
	})
	tx2 := gethtypes.NewTx(&gethtypes.LegacyTx{
		Nonce: 2, To: &to, Value: big.NewInt(2), Gas: 21000, GasPrice: big.NewInt(1),
	})
	tests := []struct {
		name string
		txs  gethtypes.Transactions
		fees []int64
	}{
		{name: "zero transactions", txs: nil, fees: nil},
		{name: "one transaction", txs: gethtypes.Transactions{tx1}, fees: constructorShapedFees(-7, 7)},
		{name: "two transactions", txs: gethtypes.Transactions{tx1, tx2}, fees: constructorShapedFees(-11, 5, 6)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newRemoteGBTResultTestMiner()
			m.policy = &mining.Policy{PeerID: "test-peer"}
			m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
			m.minTimestamp = time.Unix(1_770_000_000, 0)
			m.lastTemplate = time.Unix(1_770_000_001, 0)
			m.template.Block.Transactions = test.txs
			m.template.Fees = append([]int64(nil), test.fees...)
			w := NewGBTWorker(m)

			result, err := w.getResult(nil)
			if err != nil {
				t.Fatalf("getResult: %v", err)
			}
			if len(result.Transactions) != len(test.txs) {
				t.Fatalf("transaction count = %d, want %d", len(result.Transactions), len(test.txs))
			}
			for i, tx := range test.txs {
				if result.Transactions[i].Hash != tx.Hash().String() {
					t.Fatalf("transaction %d hash = %s, want %s", i, result.Transactions[i].Hash, tx.Hash())
				}
				if result.Transactions[i].Fee != test.fees[i+1] {
					t.Fatalf("transaction %d fee = %d, want constructor transaction slot Fees[%d]=%d", i, result.Transactions[i].Fee, i+1, test.fees[i+1])
				}
			}
			if len(test.txs) == 1 {
				previous := w.cache
				m.template.Fees[1] = 19
				result, err = w.getResult(nil)
				if err != nil {
					t.Fatalf("fee-only mutation getResult: %v", err)
				}
				if w.cache == previous {
					t.Fatal("fee-only mutation reused serialized cache")
				}
				if result.Transactions[0].Fee != 19 {
					t.Fatalf("fee-only mutation returned fee %d, want 19", result.Transactions[0].Fee)
				}
			}
		})
	}
}

func TestGBTWorkerRejectsLegacyNFeeObservations(t *testing.T) {
	to := common.HexToAddress("0x55")
	tx := gethtypes.NewTx(&gethtypes.LegacyTx{
		Nonce: 1, To: &to, Value: big.NewInt(1), Gas: 21000, GasPrice: big.NewInt(1),
	})
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
	m.template.Block.Transactions = gethtypes.Transactions{tx}
	m.template.Fees = []int64{13}

	result, err := NewGBTWorker(m).getResult(nil)
	if err == nil || result != nil {
		t.Fatal("legacy N-slot fee layout must be rejected")
	}
}

func TestGBTWorkerFailedGetRequestUpdateClearsCache(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
	m.powType = pow.SCRYPT
	w := NewGBTWorker(m)
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("seed getResult: %v", err)
	}
	if w.cache == nil {
		t.Fatal("expected serialized cache before failed update")
	}

	reply := make(chan *gbtResponse, 1)
	w.GetRequest(&corejson.TemplateRequest{PowType: byte(pow.BLAKE2BD)}, reply)
	response := <-reply
	if response.err == nil {
		t.Fatal("expected template update failure")
	}
	if m.powType != pow.SCRYPT {
		t.Fatalf("pow type after failed update = %v, want %v for retry", m.powType, pow.SCRYPT)
	}
	if w.cache != nil {
		t.Fatal("failed template update retained stale serialized cache")
	}
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("reseed getResult: %v", err)
	}
	w.GetRequest(&corejson.TemplateRequest{
		PowType: byte(pow.SCRYPT), PayAddress: common.HexToAddress("0x123").String(),
	}, reply)
	response = <-reply
	if response.err == nil {
		t.Fatal("expected pay-address template update failure")
	}
	if w.cache != nil {
		t.Fatal("failed pay-address update retained stale serialized cache")
	}
}

func TestGBTWorkerSerializationFailureClearsCache(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
	w := NewGBTWorker(m)
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("seed getResult: %v", err)
	}
	if w.cache == nil {
		t.Fatal("expected serialized cache before serialization failure")
	}
	m.template.Block.Parents = []*common.Hash{nil}
	if _, err := w.getResult(nil); err == nil {
		t.Fatal("expected nil parent serialization failure")
	}
	if w.cache != nil {
		t.Fatal("serialization failure retained stale serialized cache")
	}
}

func TestGBTWorkerFingerprintTracksSameShapeContentChanges(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
	firstParent := common.HexToHash("0x11")
	secondParent := common.HexToHash("0x22")
	m.template.Block.Parents = []*common.Hash{&firstParent, &secondParent}
	to := common.HexToAddress("0x33")
	m.template.Block.Transactions = gethtypes.Transactions{gethtypes.NewTx(&gethtypes.LegacyTx{
		Nonce: 1, To: &to, Value: big.NewInt(1), Gas: 21000, GasPrice: big.NewInt(1),
	})}
	m.template.Fees = constructorShapedFees(-3, 0)
	w := NewGBTWorker(m)
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("initial getResult: %v", err)
	}
	previous := w.cache
	assertCacheReplaced := func(label string) {
		t.Helper()
		if _, err := w.getResult(nil); err != nil {
			t.Fatalf("%s getResult: %v", label, err)
		}
		if w.cache == previous {
			t.Fatalf("%s did not replace serialized cache", label)
		}
		previous = w.cache
	}

	m.template.Block.Transactions[0] = gethtypes.NewTx(&gethtypes.LegacyTx{
		Nonce: 2, To: &to, Value: big.NewInt(1), Gas: 21000, GasPrice: big.NewInt(1),
	})
	m.template.Fees[0] = 1
	assertCacheReplaced("same-shape transaction and fee change")
	m.template.Block.Parents = []*common.Hash{&secondParent, &firstParent}
	assertCacheReplaced("parent reorder")
	m.coinbaseAddress = common.HexToAddress("0x44")
	m.template.Block.Header.CoinbaseAddress = m.coinbaseAddress
	assertCacheReplaced("coinbase change")
	m.powType = pow.SCRYPT
	m.template.Block.Header.Pow = pow.GetInstance(pow.SCRYPT, 0, 0, nil)
	assertCacheReplaced("pow change")
	m.template.Block.Header.StateRoot = common.HexToHash("0x55")
	assertCacheReplaced("root change")
	m.lastTemplate = m.lastTemplate.Add(time.Second)
	assertCacheReplaced("generation change")
}
