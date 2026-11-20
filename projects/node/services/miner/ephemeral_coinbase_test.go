package miner

import (
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"github.com/ethereum/go-ethereum/common"
	"testing"
)

func TestGBTRequestedCoinbaseDoesNotChangeDefault(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	configured := m.coinbaseAddress
	requested := common.HexToAddress("0x1234")
	m.timeSource = blockchain.NewMedianTime()
	m.policy = &mining.Policy{}
	m.template.Block.Header.CoinbaseAddress = requested
	worker := &GBTWorker{miner: m}
	result, err := worker.getResultForAddress(nil, &requested)
	if err != nil {
		t.Fatal(err)
	}
	if result.CoinbaseAddress != requested {
		t.Fatal("requested payout lost")
	}
	if m.coinbaseAddress != configured {
		t.Fatal("configured default changed")
	}
	wrong := common.HexToAddress("0x5678")
	if _, err := worker.getResultForAddress(nil, &wrong); err == nil {
		t.Fatal("mismatched requested payout accepted")
	}
	zero := common.Address{}
	if _, err := worker.getResultForAddress(nil, &zero); err == nil {
		t.Fatal("zero requested payout accepted")
	}
	if _, err := worker.getResult(nil); err == nil {
		t.Fatal("default request accepted another payout")
	}
	m.template.Block.Header.CoinbaseAddress = configured
	if _, err := worker.getResult(nil); err != nil {
		t.Fatal("default behavior changed:", err)
	}
}
func TestGBTRejectsInvalidRequestedAddressBeforeBuilding(t *testing.T) {
	worker := &GBTWorker{miner: newRemoteGBTResultTestMiner()}
	for _, address := range []string{"garbage", "0x0000000000000000000000000000000000000000"} {
		reply := make(chan *gbtResponse, 1)
		worker.GetRequest(&corejson.TemplateRequest{PayAddress: address}, reply)
		if response := <-reply; response.err == nil {
			t.Fatal("invalid payout accepted")
		}
	}
}
