package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

type Genesis struct {
	Config     json.RawMessage            `json:"config"`
	Nonce      string                     `json:"nonce"`
	Timestamp  string                     `json:"timestamp"`
	ExtraData  string                     `json:"extraData"`
	GasLimit   string                     `json:"gasLimit"`
	Difficulty string                     `json:"difficulty"`
	Mixhash    string                     `json:"mixHash"`
	Coinbase   string                     `json:"coinbase"`
	Alloc      map[string]*GenesisAccount `json:"alloc"`
	Number     string                     `json:"number"`
	GasUsed    string                     `json:"gasUsed"`
	ParentHash string                     `json:"parentHash"`
}

type GenesisAccount struct {
	Code    string            `json:"code,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
	Balance string            `json:"balance,omitempty"`
	Nonce   uint64            `json:"nonce,omitempty"`
}

type GenesisData struct {
	Genesis Genesis `json:"genesis"`
}

type NetworkConfig struct {
	Network string      `json:"network"`
	Data    GenesisData `json:"data"`
}

var (
	StakingProxyAddr    = "0x1000000000000000000000000000000000000001"
	GovernanceProxyAddr = "0x1000000000000000000000000000000000000005"
	StakingImplAddr     = "0x1000000000000000000000000000000000000011"
	GovernanceImplAddr  = "0x1000000000000000000000000000000000000015" // Placeholder

	// OpenZeppelin TransparentProxy Implementation Slot
	ImplSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	// OpenZeppelin Admin Slot (Optional for UUPS, used as record)
	AdminSlot = "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"
)

func main() {
	network := flag.String("network", "privnet", "Network to update")
	artifacts := flag.String("artifacts", "", "Path to artifacts directory")
	owner := flag.String("owner", "0x0000000000000000000000000000000000000000", "Proxy Owner address")
	mergeAlloc := flag.String("merge-alloc", "", "JSON file {address: {balance,code,storage,nonce}} to merge into the network's genesis alloc (text-preserving: other network sections are left byte-identical)")
	flag.Parse()

	if *mergeAlloc != "" {
		if err := mergeAllocIntoGenesis(*network, *mergeAlloc); err != nil {
			fmt.Println("merge failed:", err)
			os.Exit(1)
		}
		return
	}

	if *artifacts == "" {
		fmt.Println("Artifacts path required")
		os.Exit(1)
	}

	fmt.Printf("Updating genesis for network: %s\n", *network)

	// 1. Load Bytecode
	proxyCode := loadArtifact(*artifacts, "proxy/UUPSProxy.sol/UUPSProxy.json")
	stakingPlaceholderCode := loadArtifact(*artifacts, "staking/StakingPlaceholder.sol/StakingPlaceholder.json")
	governanceCode := loadArtifact(*artifacts, "governance/GovernanceV1.sol/GovernanceV1.json")

	// 2. Read genesis_alloc.go
	content, err := os.ReadFile("bevm/bdag/genesis_alloc.go")
	if err != nil {
		panic(err)
	}

	// 3. Parse JSON from Go file
	// Extract JSON between first and last backticks
	start := strings.Index(string(content), "`") + 1
	end := strings.LastIndex(string(content), "`")
	jsonStr := string(content)[start:end]

	var configs []NetworkConfig
	if err := json.Unmarshal([]byte(jsonStr), &configs); err != nil {
		panic(err)
	}

	// 4. Update Alloc
	found := false
	for i, cfg := range configs {
		if cfg.Network == *network {
			found = true

			// PRESERVE ALLOC: Use existing map, create if nil.
			alloc := cfg.Data.Genesis.Alloc
			if alloc == nil {
				alloc = make(map[string]*GenesisAccount)
			}

			// -- Staking Proxy --
			alloc[StakingProxyAddr] = &GenesisAccount{
				Code: proxyCode,
				Storage: map[string]string{
					ImplSlot:  common.HexToHash(StakingImplAddr).Hex(),
					AdminSlot: common.HexToHash(*owner).Hex(),
				},
				Balance: "0",
			}

			// -- Staking Implementation (Placeholder) --
			alloc[StakingImplAddr] = &GenesisAccount{
				Code:    stakingPlaceholderCode,
				Balance: "0",
			}

			// -- Governance Proxy --
			// Using same proxy code for now
			alloc[GovernanceProxyAddr] = &GenesisAccount{
				Code: proxyCode,
				Storage: map[string]string{
					ImplSlot:  common.HexToHash(GovernanceImplAddr).Hex(),
					AdminSlot: common.HexToHash(*owner).Hex(),
				},
				Balance: "0",
			}

			// -- Governance Implementation (Placeholder) --
			alloc[GovernanceImplAddr] = &GenesisAccount{
				Code:    governanceCode,
				Balance: "0",
			}

			configs[i].Data.Genesis.Alloc = alloc
		}
	}

	if !found {
		fmt.Printf("Network %s not found in genesis config\n", *network)
		return
	}

	// 5. Serialize back to JSON
	newJson, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		panic(err)
	}

	// 6. Write back to Go file
	newContent := string(content[:start]) + string(newJson) + string(content[end:])
	err = os.WriteFile("bevm/bdag/genesis_alloc.go", []byte(newContent), 0644)
	if err != nil {
		panic(err)
	}
	fmt.Println("Successfully updated genesis_alloc.go")
}

func loadArtifact(basePath, artifactPath string) string {
	fullPath := filepath.Join(basePath, artifactPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		fmt.Printf("Warning: artifact not found: %s\n", fullPath)
		return ""
	}
	var artifact struct {
		Bytecode string `json:"bytecode"`
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		panic(err)
	}
	return artifact.Bytecode
}
