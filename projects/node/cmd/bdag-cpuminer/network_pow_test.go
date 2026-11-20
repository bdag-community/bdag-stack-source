package main

import (
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"testing"
)

// This reproduces the real failure: an easier valid devnet template was rejected
// before hashing because the miner always installed mainnet's smaller PoW limit.
func TestMinerUsesConfiguredNetworkPowLimit(t *testing.T) {
	original := params.DevNetParams.PowConfig
	fixture := *original
	fixture.ScryptPowLimit = pow.CompactToBig(0x207fffff)
	fixture.ScryptPowLimitBits = 0x207fffff
	params.DevNetParams.PowConfig = &fixture
	t.Cleanup(func() { params.DevNetParams.PowConfig = original })
	tpl := &corejson.GetBlockTemplateResult{Version: 0x20000000, Height: 1, CurTime: 1788808074, PowDiffReference: corejson.PowDiffReference{NBits: "207fffff"}}
	for _, network := range []string{params.DevNetParams.Name, params.MainNetParams.Name, params.MainNetIsolatedParams.Name, "unknown-network"} {
		t.Run(network, func(t *testing.T) {
			t.Setenv("RPC_NETWORK", network)
			cfg := configFromEnv()
			if cfg.network != network {
				t.Fatal("network parse")
			}
			header, err := headerFromTemplate(tpl, pow.SCRYPT, 7, cfg.network)
			if network == "unknown-network" {
				if err == nil {
					t.Fatal("unknown network must fail closed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			solved := false
			for nonce := uint32(0); nonce < 64; nonce++ {
				header.Pow.SetNonce(nonce)
				if header.Pow.FindSolver(header.BlockData(), header.BlockHash(), header.Difficulty) {
					solved = true
					break
				}
			}
			if network == params.DevNetParams.Name && !solved {
				t.Fatal("valid devnet target rejected by wrong network PoW limit")
			}
			if network != params.DevNetParams.Name && solved {
				t.Fatal("mainnet accepted devnet-only easier target")
			}
		})
	}
}
