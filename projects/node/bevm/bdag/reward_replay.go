package bdag

import (
	"errors"
	"fmt"
	"math/big"

	bparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
)

// This interface is private: replay is potentially expensive and is not a
// public RPC or a payment authorization.
type rewardReplayChain interface {
	GetBlockByHash(common.Hash) *types.Block
	GetBlockByNumber(uint64) *types.Block
	StateAt(common.Hash) (*state.StateDB, error)
	Processor() core.Processor
	Validator() core.Validator
	GetVMConfig() *vm.Config
}

// evmRewardReplayEvidence reports credits from a validated EVM replay. It does
// not establish native DAG identity, finality, staking ownership, spendable
// balance, or a right to pay any particular ASIC. OtherSenderFeesWei identifies
// a different sender address, not whether that address belongs to the pool.
type evmRewardReplayEvidence struct {
	BlockHash, ParentHash, StateRoot                common.Hash
	BlockNumber                                     uint64
	Coinbase                                        common.Address
	SubsidyWei, OtherSenderFeesWei, SelfPaidFeesWei *big.Int
}

// replayEVMReward reads a fresh parent-state view and never commits it. The
// supplied hash must independently be linked to the native block and checked
// for finality; canonical checks here are point-in-time EVM observations only.
// Missing historical state or ambiguous callback sequences fail closed.
func replayEVMReward(chain rewardReplayChain, blockHash common.Hash, wallet common.Address) (*evmRewardReplayEvidence, error) {
	if chain == nil || blockHash == (common.Hash{}) || wallet == (common.Address{}) || wallet == bparams.StakingContractAddr || wallet == bparams.SeparateStakingV2Addr {
		return nil, errors.New("invalid replay chain, block, or reward wallet")
	}
	block := chain.GetBlockByHash(blockHash)
	if block == nil || block.Hash() != blockHash || block.NumberU64() == 0 || block.Coinbase() != wallet {
		return nil, errors.New("block is missing, genesis, or has a different reward wallet")
	}
	number := block.NumberU64()
	canonical := chain.GetBlockByNumber(number)
	if canonical == nil || canonical.Hash() != blockHash {
		return nil, errors.New("block is not the observed canonical EVM block")
	}
	parent := chain.GetBlockByHash(block.ParentHash())
	if parent == nil || parent.Hash() != block.ParentHash() || parent.NumberU64() != number-1 {
		return nil, errors.New("parent block is missing or inconsistent")
	}
	stateDB, err := chain.StateAt(parent.Root())
	if err != nil {
		return nil, fmt.Errorf("parent state unavailable: %w", err)
	}
	if stateDB == nil || chain.Processor() == nil || chain.Validator() == nil || chain.GetVMConfig() == nil {
		return nil, errors.New("replay state, processor, validator, or VM config unavailable")
	}
	evidence := &evmRewardReplayEvidence{BlockHash: blockHash, ParentHash: parent.Hash(), StateRoot: block.Root(), BlockNumber: number, Coinbase: wallet, SubsidyWei: new(big.Int), OtherSenderFeesWei: new(big.Int), SelfPaidFeesWei: new(big.Int)}
	var currentSender common.Address
	var inTransaction bool
	var callbackErr error
	hooks := &tracing.Hooks{
		OnTxStart: func(_ *tracing.VMContext, _ *types.Transaction, sender common.Address) {
			if inTransaction {
				callbackErr = errors.New("overlapping transaction callbacks")
			}
			inTransaction, currentSender = true, sender
		},
		OnTxEnd: func(_ *types.Receipt, err error) {
			if !inTransaction || err != nil {
				callbackErr = errors.New("invalid transaction completion")
			}
			inTransaction = false
		},
		OnBalanceChange: func(address common.Address, before, after *big.Int, reason tracing.BalanceChangeReason) {
			if address != wallet || (reason != tracing.BalanceIncreaseRewardMineBlock && reason != tracing.BalanceIncreaseRewardTransactionFee) {
				return
			}
			if before == nil || after == nil || after.Cmp(before) <= 0 {
				callbackErr = errors.New("invalid reward balance change")
				return
			}
			delta := new(big.Int).Sub(after, before)
			switch reason {
			case tracing.BalanceIncreaseRewardMineBlock:
				if inTransaction {
					callbackErr = errors.New("block reward occurred inside a transaction")
					return
				}
				evidence.SubsidyWei.Add(evidence.SubsidyWei, delta)
			case tracing.BalanceIncreaseRewardTransactionFee:
				if !inTransaction {
					callbackErr = errors.New("fee credit without transaction attribution")
					return
				}
				if currentSender == wallet {
					evidence.SelfPaidFeesWei.Add(evidence.SelfPaidFeesWei, delta)
				} else {
					evidence.OtherSenderFeesWei.Add(evidence.OtherSenderFeesWei, delta)
				}
			}
		},
	}
	stateDB.SetLogger(hooks)
	defer stateDB.SetLogger(nil)
	config := *chain.GetVMConfig()
	config.Tracer = hooks
	result, err := chain.Processor().Process(block, stateDB, config)
	if err != nil {
		return nil, fmt.Errorf("block replay failed: %w", err)
	}
	if callbackErr != nil {
		return nil, callbackErr
	}
	if inTransaction {
		return nil, errors.New("transaction callback left open")
	}
	if err := stateDB.Error(); err != nil {
		return nil, fmt.Errorf("state read failed: %w", err)
	}
	if err := chain.Validator().ValidateState(block, stateDB, result, false); err != nil {
		return nil, fmt.Errorf("replayed state did not validate: %w", err)
	}
	if err := stateDB.Error(); err != nil {
		return nil, fmt.Errorf("state validation read failed: %w", err)
	}
	canonical = chain.GetBlockByNumber(number)
	if canonical == nil || canonical.Hash() != blockHash {
		return nil, errors.New("canonical EVM block changed during replay")
	}
	return evidence, nil
}
