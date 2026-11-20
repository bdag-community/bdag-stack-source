package bdag

import (
	"github.com/ethereum/go-ethereum/common"
	"strings"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
)

func TestMainnetRejectsRehearsalConfiguration(t *testing.T) {
	previous := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	names := []string{"STAKING_CONTRACT_ADDR", "STAKING_V2_FORK_BLOCK", "GOVERNANCE_FORK_BLOCK", "GOVERNANCE_FORK_CONFIG_JSON", "BLOCKLIST_GENESIS_BLOCKED", "BLOCKLIST_GENESIS_OWNER", "BLOCKLIST_CONTRACT_CODE"}
	for _, name := range names {
		t.Setenv(name, "")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "rehearsal-only")
			if _, err := MakeConfig(&config.Config{}); err == nil {
				t.Fatalf("Mainnet accepted rehearsal override %s", name)
			} else if strings.Contains(err.Error(), "rehearsal-only") {
				t.Fatal("error exposed the override value")
			}
		})
	}
	t.Run("genesis allocation", func(t *testing.T) {
		if _, err := MakeConfig(&config.Config{GenesisAlloc: []string{"0x0000000000000000000000000000000000000001:100000"}}); err == nil {
			t.Fatal("Mainnet accepted a test wallet genesis allocation")
		}
	})
	t.Run("ordinary Mainnet", func(t *testing.T) {
		c, err := MakeConfig(&config.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if c.Eth.Genesis.Config.ChainID.Uint64() != 1404 {
			t.Fatal("wrong Mainnet chain")
		}
		if qparams.MainNetParam.TargetTimePerBlock != time.Second || qparams.MainNetParam.StakingEpochDurationSeconds != 604800 {
			t.Fatal("Mainnet block/epoch policy changed to rehearsal timing")
		}
		if c.Eth.Genesis.GasLimit != 45_000_000 {
			t.Fatal("Mainnet gas policy changed")
		}
		if c.Eth.Genesis.Config.BdagSeparateStaking != nil || c.Eth.Genesis.Config.BdagSeparateStakingBlock != nil {
			t.Fatal("preparation build copied the Testnet separate-staking schedule onto Mainnet")
		}
		if c.Eth.Genesis.Config.BdagGovernanceForkBlock != nil || c.Eth.Genesis.Config.BdagTestnetTimingBlock != nil {
			t.Fatal("preparation build scheduled an unqualified Mainnet activation")
		}
	})
	t.Run("whitespace contract override", func(t *testing.T) {
		t.Setenv("STAKING_CONTRACT_ADDR", "  ")
		if _, err := MakeConfig(&config.Config{}); err == nil {
			t.Fatal("Mainnet accepted whitespace address override")
		}
	})
	t.Run("testnet fixtures still work", func(t *testing.T) {
		qparams.ActiveNetParams = &qparams.TestNetParam
		t.Cleanup(func() { qparams.ActiveNetParams = &qparams.MainNetParam })
		c, err := MakeConfig(&config.Config{GenesisAlloc: []string{"0x0000000000000000000000000000000000000001:100000"}})
		if err != nil {
			t.Fatal(err)
		}
		if c.Eth.Genesis.Config.ChainID.Uint64() != 1043 || c.Eth.Genesis.Alloc[common.HexToAddress("0x1")].Balance == nil {
			t.Fatal("Testnet fixture support changed")
		}
	})

}
