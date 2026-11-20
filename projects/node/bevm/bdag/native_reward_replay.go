package bdag

import (
	"errors"
	"fmt"

	bconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
)

// The production native blockchain implements these methods. Lock only while
// copying identity fields, never while reexecuting an EVM block.
type nativeRewardChain interface {
	ChainRLock()
	ChainRUnlock()
	GetBlockOrderByHash(*common.Hash) (uint, error)
	GetBlockByOrder(uint64) model.Block
	GetBlockHeader(model.Block) *native.BlockHeader
}

type nativeRewardSnapshot struct {
	BlockHash, PreviousBlockHash                       common.Hash
	Order, Height                                      uint64
	EVMHash, EVMRoot, PreviousEVMHash, PreviousEVMRoot common.Hash
	EVMNumber, PreviousEVMNumber                       uint64
	Wallet                                             common.Address
	RewardAtoms, Timestamp                             uint64
}

// observeNativeReward copies a coherent native-chain view under its read
// lock. It reports an ordered, non-invalid single EVM advance; it does not
// establish finality, reward maturity, or payment eligibility.
func observeNativeReward(chain nativeRewardChain, hash common.Hash, wallet common.Address) (nativeRewardSnapshot, error) {
	var empty nativeRewardSnapshot
	if chain == nil || hash == (common.Hash{}) || wallet == (common.Address{}) {
		return empty, errors.New("missing native chain, block, or wallet")
	}
	chain.ChainRLock()
	defer chain.ChainRUnlock()
	order, err := chain.GetBlockOrderByHash(&hash)
	if err != nil || order == 0 || order >= blockdag.MaxBlockOrder {
		return empty, errors.New("native block has no usable order")
	}
	block := chain.GetBlockByOrder(uint64(order))
	previous := chain.GetBlockByOrder(uint64(order - 1))
	if block == nil || previous == nil || block.GetHash() == nil || previous.GetHash() == nil || *block.GetHash() != hash || block.GetOrder() != order || previous.GetOrder() != order-1 {
		return empty, errors.New("ordered native blocks are missing or changed")
	}
	currentState, previousState := block.GetState(), previous.GetState()
	if currentState == nil || previousState == nil || !currentState.IsOrdered() || !previousState.IsOrdered() || currentState.GetOrder() != uint64(order) || previousState.GetOrder() != uint64(order-1) || currentState.GetID() != uint64(block.GetID()) || previousState.GetID() != uint64(previous.GetID()) || currentState.GetStatus().KnownInvalid() {
		return empty, errors.New("native block state is missing, invalid, or inconsistent")
	}
	header := chain.GetBlockHeader(block)
	if header == nil || header.Pow == nil || len(header.Pow.BlockData()) < 5 || header.Timestamp.Unix() < 0 || header.BlockHash() != hash || header.CoinbaseAddress != wallet {
		return empty, errors.New("native header does not match the block and reward wallet")
	}
	result := nativeRewardSnapshot{
		BlockHash: hash, PreviousBlockHash: *previous.GetHash(), Order: uint64(order), Height: header.BlockHeight,
		EVMHash: currentState.GetEVMHash(), EVMRoot: currentState.GetEVMRoot(), EVMNumber: currentState.GetEVMNumber(),
		PreviousEVMHash: previousState.GetEVMHash(), PreviousEVMRoot: previousState.GetEVMRoot(), PreviousEVMNumber: previousState.GetEVMNumber(),
		Wallet: wallet, RewardAtoms: header.Reward, Timestamp: uint64(header.Timestamp.Unix()),
	}
	if result.PreviousBlockHash == (common.Hash{}) || result.EVMHash == (common.Hash{}) || result.EVMRoot == (common.Hash{}) || result.PreviousEVMHash == (common.Hash{}) || result.PreviousEVMRoot == (common.Hash{}) || result.EVMNumber == 0 || result.EVMNumber-1 != result.PreviousEVMNumber {
		return empty, errors.New("native block does not prove a single EVM advance")
	}
	return result, nil
}

type nativeRewardReplayEvidence struct {
	Native nativeRewardSnapshot
	EVM    *evmRewardReplayEvidence
}

// replayNativeReward performs short, locked native observations around an
// unlocked EVM replay. It intentionally remains private and unconnected to the
// pool or any payer. Two matching observations are not a finality guarantee.
func replayNativeReward(nativeChain nativeRewardChain, evm rewardReplayChain, nativeHash common.Hash, wallet common.Address) (*nativeRewardReplayEvidence, error) {
	before, err := observeNativeReward(nativeChain, nativeHash, wallet)
	if err != nil {
		return nil, err
	}
	if evm == nil {
		return nil, errors.New("missing EVM chain")
	}
	block := evm.GetBlockByHash(before.EVMHash)
	parent := evm.GetBlockByHash(before.PreviousEVMHash)
	if block == nil || parent == nil || block.Hash() != before.EVMHash || block.Root() != before.EVMRoot || block.NumberU64() != before.EVMNumber || block.ParentHash() != before.PreviousEVMHash || parent.Hash() != before.PreviousEVMHash || parent.Root() != before.PreviousEVMRoot || parent.NumberU64() != before.PreviousEVMNumber || block.Coinbase() != wallet || block.Time() != before.Timestamp {
		return nil, errors.New("native and EVM block commitments do not match")
	}
	if err := bconsensus.ValidateRewardExtra(block.Extra(), before.RewardAtoms); err != nil {
		return nil, fmt.Errorf("native and EVM reward mismatch: %w", err)
	}
	evidence, err := replayEVMReward(evm, before.EVMHash, wallet)
	if err != nil {
		return nil, err
	}
	after, err := observeNativeReward(nativeChain, nativeHash, wallet)
	if err != nil {
		return nil, fmt.Errorf("native block changed during EVM replay: %w", err)
	}
	if before != after {
		return nil, errors.New("native block commitment changed during EVM replay")
	}
	return &nativeRewardReplayEvidence{Native: before, EVM: evidence}, nil
}
