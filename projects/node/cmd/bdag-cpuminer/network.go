package main

import (
	"fmt"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"strings"
)

func powConfigForNetwork(network string) (*pow.PowConfig, error) {
	for _, networkParams := range params.AllNetParams {
		if networkParams.Name == network {
			return networkParams.PowConfig, nil
		}
	}
	return nil, fmt.Errorf("unsupported miner network %q", network)
}
func parseMiningPayAddresses(network, raw string) ([]common.Address, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if network != params.DevNetParams.Name {
		return nil, fmt.Errorf("MINING_PAY_ADDRESSES is restricted to devnet")
	}
	var addresses []common.Address
	seen := make(map[common.Address]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if !common.IsHexAddress(part) {
			return nil, fmt.Errorf("MINING_PAY_ADDRESSES contains an invalid address")
		}
		address := common.HexToAddress(part)
		if address == (common.Address{}) || seen[address] {
			return nil, fmt.Errorf("MINING_PAY_ADDRESSES requires nonzero distinct addresses")
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	return addresses, nil
}
func (c minerConfig) payAddress(round uint32) *common.Address {
	if len(c.payAddresses) == 0 {
		return nil
	}
	address := c.payAddresses[uint64(round)%uint64(len(c.payAddresses))]
	return &address
}

type miningTemplateClient interface {
	GetBlockTemplate([]string, byte) (*corejson.GetBlockTemplateResult, error)
	GetBlockTemplateForAddress([]string, byte, string) (*corejson.GetBlockTemplateResult, error)
}

func requestMiningTemplate(c miningTemplateClient, powType byte, address *common.Address) (*corejson.GetBlockTemplateResult, error) {
	if address == nil {
		return c.GetBlockTemplate([]string{"proposal"}, powType)
	}
	template, err := c.GetBlockTemplateForAddress([]string{"proposal"}, powType, address.Hex())
	if err != nil {
		return nil, err
	}
	if template == nil || template.CoinbaseAddress != *address {
		return nil, fmt.Errorf("template coinbase does not match requested mining payment address %s", address.Hex())
	}
	return template, nil
}
