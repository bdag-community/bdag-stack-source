package node

import (
	"fmt"
	"math/big"
	"reflect"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
)

type nodeInfoChain interface {
	BestSnapshot() *blockchain.BestState
	HeaderByHash(*common.Hash) (types.BlockHeader, error)
}

// nodeInfoSnapshot reads the immutable snapshot and its exact tip header using
// concurrency-safe readers. Do not scan ancestors for a hard-coded PoW type:
// on Scrypt-mined chains that scan can reach genesis while holding chainLock,
// delaying both RPC and block processing. P2P metadata needs no chain lock.
func nodeInfoSnapshot(chain nodeInfoChain) (*blockchain.BestState, *big.Int, pow.PowType, error) {
	best := chain.BestSnapshot()
	if best == nil {
		return nil, nil, 0, fmt.Errorf("node info: best snapshot is unavailable")
	}
	header, err := chain.HeaderByHash(&best.Hash)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("node info: tip header: %w", err)
	}
	target, powType, ok := currentHeaderDifficulty(&header)
	if !ok {
		return nil, nil, 0, fmt.Errorf("node info: tip difficulty metadata is unavailable")
	}
	return best, target, powType, nil
}

func currentHeaderDifficulty(header *types.BlockHeader) (*big.Int, pow.PowType, bool) {
	if header == nil || powInstanceIsNil(header.Pow) {
		return nil, 0, false
	}
	target := pow.CompactToBig(header.Difficulty)
	if target == nil || target.Sign() <= 0 {
		return nil, 0, false
	}
	return target, header.Pow.GetPowType(), true
}

// nodeInfoDifficulty converts one immutable tip target without walking chain
// history. The algorithm name makes the reported denominator explicit.
func nodeInfoDifficulty(target *big.Int, powType pow.PowType, network *params.Params) (*json.PowDiff, error) {
	if target == nil || target.Sign() <= 0 || network == nil || network.PowConfig == nil {
		return nil, fmt.Errorf("node info: tip difficulty unavailable")
	}
	powName := pow.GetPowName(powType)
	if powName == "" {
		return nil, fmt.Errorf("node info: unknown tip proof of work %d", powType)
	}
	instance := pow.GetInstance(powType, 0, 0, nil)
	if powInstanceIsNil(instance) {
		return nil, fmt.Errorf("node info: tip proof of work unavailable")
	}
	instance.SetParams(network.PowConfig)
	denominator := instance.GetSafeDiff(0)
	if denominator == nil || denominator.Sign() <= 0 {
		return nil, fmt.Errorf("node info: proof of work %s is not configured", powName)
	}
	return &json.PowDiff{
		CurrentDiff: getDifficultyRatio(target, network, powType),
		PowType:     powName,
	}, nil
}

func powInstanceIsNil(instance pow.IPow) bool {
	if instance == nil {
		return true
	}
	value := reflect.ValueOf(instance)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
