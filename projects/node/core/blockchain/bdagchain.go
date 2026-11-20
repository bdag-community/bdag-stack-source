package blockchain

import (
	"errors"
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
)

func (b *BlockChain) BdagChain() model.BdagChain {
	return b.bdagChain
}

func (b *BlockChain) EVMChain() *bdag.BdagChain {
	return b.bdagChain
}

func (b *BlockChain) bdagCheckConnectBlock(block *BlockNode, layer uint64) error {
	eb, err := bdag.BuildEVMBlock(block.GetBody())
	if err != nil {
		return err
	}
	block.SetbdagBlock(eb)
	admissibility := bdag.ConsensusGovernanceAdmissibilityAtLayer(layer)
	err = b.bdagChain.CheckConnectBlock(eb, block.block.Block().Header.CoinbaseAddress, block.block.Block().Header.Reward, admissibility)
	return classifyGovernanceAdmissibilityError(err)
}

func (b *BlockChain) bdagCheckConnectBlockWithParent(block *BlockNode, parent *gethtypes.Header, layer uint64) error {
	eb, err := bdag.BuildEVMBlock(block.GetBody())
	if err != nil {
		return err
	}
	block.SetbdagBlock(eb)
	admissibility := bdag.ConsensusGovernanceAdmissibilityAtLayer(layer)
	err = b.bdagChain.CheckConnectBlockWithParent(parent, eb, block.block.Block().Header.CoinbaseAddress, block.block.Block().Header.Reward, admissibility)
	return classifyGovernanceAdmissibilityError(err)
}

func (b *BlockChain) bdagConnectBlock(block *BlockNode, checkpoint *EVMCheckpoint) (uint64, error) {
	eb := block.GetbdagBlock()
	if eb == nil {
		return 0, nil
	}
	if err := validateBuiltEVMCheckpoint(eb.EvmBlock, checkpoint); err != nil {
		return 0, err
	}
	return b.bdagChain.ConnectBlock(eb)
}

func validateBuiltEVMCheckpoint(block *gethtypes.Block, checkpoint *EVMCheckpoint) error {
	if block == nil || checkpoint == nil || block.NumberU64() != checkpoint.Number {
		return nil
	}
	if block.Hash() != checkpoint.Hash {
		return classifyEVMCheckpointError(fmt.Errorf("block %d hash %s, want %s", checkpoint.Number, block.Hash(), checkpoint.Hash))
	}
	if checkpoint.Root != (common.Hash{}) && block.Root() != checkpoint.Root {
		return classifyEVMCheckpointError(fmt.Errorf("block %d root %s, want %s", checkpoint.Number, block.Root(), checkpoint.Root))
	}
	return nil
}

func classifyGovernanceAdmissibilityError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, bdag.ErrBlockedAddress) ||
		errors.Is(err, bdag.ErrRestrictedAddress) ||
		errors.Is(err, vm.ErrNativeTransferBlocked) {
		return ruleErrorWithCause(
			ErrGovernanceAdmissibility,
			fmt.Sprintf("governance admissibility rejected block: %v", err),
			err,
		)
	}
	return err
}

func classifyEVMCheckpointError(err error) error {
	if err == nil {
		return nil
	}
	return ruleErrorWithCause(
		ErrEVMCheckpointMismatch,
		fmt.Sprintf("evm checkpoint rejected block: %v", err),
		err,
	)
}

func isGovernanceAdmissibilityRuleError(err error) bool {
	var ruleErr RuleError
	if errors.As(err, &ruleErr) && ruleErr.ErrorCode == ErrGovernanceAdmissibility {
		return true
	}
	var ruleErrPtr *RuleError
	return errors.As(err, &ruleErrPtr) && ruleErrPtr != nil && ruleErrPtr.ErrorCode == ErrGovernanceAdmissibility
}

func isEVMCheckpointRuleError(err error) bool {
	var ruleErr RuleError
	if errors.As(err, &ruleErr) && ruleErr.ErrorCode == ErrEVMCheckpointMismatch {
		return true
	}
	var ruleErrPtr *RuleError
	return errors.As(err, &ruleErrPtr) && ruleErrPtr != nil && ruleErrPtr.ErrorCode == ErrEVMCheckpointMismatch
}
