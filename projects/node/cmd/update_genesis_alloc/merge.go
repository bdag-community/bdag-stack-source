package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// genesisFiles are updated in place. genesis_alloc.go holds the authoritative
// embedded JSON; genesis.json is a human-readable mirror of the same data.
var genesisFiles = []string{"bevm/bdag/genesis_alloc.go", "bevm/bdag/genesis.json"}

// mergeAllocIntoGenesis merges the accounts in allocFile into the "alloc" object
// of the named network inside each genesis file. Only the bytes of that one
// alloc object are rewritten, so every other network section stays byte-identical
// (their genesis hashes cannot move). Existing accounts with the same address
// (case-insensitive) are replaced; all others are preserved.
func mergeAllocIntoGenesis(network, allocFile string) error {
	raw, err := os.ReadFile(allocFile)
	if err != nil {
		return err
	}
	var incoming map[string]*GenesisAccount
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return fmt.Errorf("parse %s: %w", allocFile, err)
	}
	for _, f := range genesisFiles {
		if _, err := os.Stat(f); err != nil {
			fmt.Printf("skip %s (%v)\n", f, err)
			continue
		}
		if err := mergeAllocIntoFile(f, network, incoming); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("updated %s (%s alloc: %d incoming accounts)\n", f, network, len(incoming))
	}
	return nil
}

func mergeAllocIntoFile(path, network string, incoming map[string]*GenesisAccount) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	start, end, indent, err := locateAlloc(content, network)
	if err != nil {
		return err
	}
	var existing map[string]*GenesisAccount
	if err := json.Unmarshal(content[start:end], &existing); err != nil {
		return fmt.Errorf("parse existing alloc: %w", err)
	}
	if existing == nil {
		existing = map[string]*GenesisAccount{}
	}
	for addr, acct := range incoming {
		for k := range existing {
			if strings.EqualFold(k, addr) {
				delete(existing, k)
			}
		}
		existing[common.HexToAddress(addr).Hex()] = acct
	}
	// Match the file's 2-space style; prefix aligns nested lines with the "alloc" key.
	out, err := json.MarshalIndent(existing, indent, "  ")
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.Write(content[:start])
	buf.Write(out)
	buf.Write(content[end:])
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// locateAlloc returns the byte range [start,end) of the JSON object that is the
// value of "alloc" inside the section whose "network" is name, plus the
// indentation of the "alloc" key line.
func locateAlloc(content []byte, network string) (int, int, string, error) {
	marker := []byte(fmt.Sprintf("\"network\": \"%s\"", network))
	ni := bytes.Index(content, marker)
	if ni < 0 {
		return 0, 0, "", fmt.Errorf("network %q not found", network)
	}
	ai := bytes.Index(content[ni:], []byte("\"alloc\":"))
	if ai < 0 {
		return 0, 0, "", fmt.Errorf("alloc for %q not found", network)
	}
	ai += ni
	// indentation of the line holding "alloc"
	ls := bytes.LastIndexByte(content[:ai], '\n') + 1
	indent := string(content[ls:ai])
	if strings.TrimSpace(indent) != "" {
		indent = ""
	}
	ob := bytes.IndexByte(content[ai:], '{')
	if ob < 0 {
		return 0, 0, "", fmt.Errorf("alloc object start not found")
	}
	start := ai + ob
	depth := 0
	inStr := false
	for i := start; i < len(content); i++ {
		c := content[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return start, i + 1, indent, nil
			}
		}
	}
	return 0, 0, "", fmt.Errorf("unbalanced braces in alloc")
}
