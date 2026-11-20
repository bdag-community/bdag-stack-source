package main

import (
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/ethereum/go-ethereum/common"
	"testing"
)

func TestMiningPayAddressRotationAndValidation(t *testing.T) {
	a := "0x0000000000000000000000000000000000000001"
	b := "0x0000000000000000000000000000000000000002"
	addresses, err := parseMiningPayAddresses("devnet", a+","+b)
	if err != nil {
		t.Fatal(err)
	}
	cfg := minerConfig{payAddresses: addresses}
	for i, want := range []string{a, b, a, b} {
		if got := cfg.payAddress(uint32(i)); got == nil || *got != common.HexToAddress(want) {
			t.Fatalf("round%d selected %v", i, got)
		}
	}
	if (minerConfig{}).payAddress(0) != nil {
		t.Fatal("default must preserve node coinbase")
	}
	for _, raw := range []string{"bad", a + ",", a + "," + a, "0x0000000000000000000000000000000000000000"} {
		if _, err := parseMiningPayAddresses("devnet", raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := parseMiningPayAddresses("mainnet", a); err == nil {
		t.Fatal("rotation must be devnet opt-in")
	}
}

type templateRecorder struct {
	requested    string
	coinbase     common.Address
	defaultCalls int
}

func (r *templateRecorder) GetBlockTemplate([]string, byte) (*corejson.GetBlockTemplateResult, error) {
	r.defaultCalls++
	return &corejson.GetBlockTemplateResult{CoinbaseAddress: r.coinbase}, nil
}
func (r *templateRecorder) GetBlockTemplateForAddress(_ []string, _ byte, address string) (*corejson.GetBlockTemplateResult, error) {
	r.requested = address
	return &corejson.GetBlockTemplateResult{CoinbaseAddress: r.coinbase}, nil
}
func TestRequestedTemplateAddressMustMatch(t *testing.T) {
	address := common.HexToAddress("0x1234")
	r := &templateRecorder{coinbase: address}
	if _, err := requestMiningTemplate(r, 10, &address); err != nil {
		t.Fatal(err)
	}
	if r.requested != address.Hex() || r.defaultCalls != 0 {
		t.Fatal("explicit address not requested")
	}
	r.coinbase = common.HexToAddress("0x5678")
	if _, err := requestMiningTemplate(r, 10, &address); err == nil {
		t.Fatal("mismatched template coinbase accepted")
	}
	if _, err := requestMiningTemplate(r, 10, nil); err != nil || r.defaultCalls != 1 {
		t.Fatal("default request changed")
	}
}
