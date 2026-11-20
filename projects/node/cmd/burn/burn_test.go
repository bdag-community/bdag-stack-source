package main

import (
	"testing"
)

func TestGetParamsKnownNetworks(t *testing.T) {
	networks := []string{"mainnet", "testnet", "mixnet", "privnet", "devnet"}
	for _, network := range networks {
		p, err := getParams(network)
		if err != nil {
			t.Fatalf("expected network %s to be supported, got error: %v", network, err)
		}
		if p == nil || p.Name == "" {
			t.Fatalf("expected non-empty params for network %s", network)
		}
	}
}

func TestGetParamsUnknownNetwork(t *testing.T) {
	if _, err := getParams("0.9testnet"); err == nil {
		t.Fatal("expected unknown-network error for 0.9testnet")
	}
}
