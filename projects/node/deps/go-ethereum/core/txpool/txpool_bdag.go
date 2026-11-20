package txpool

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// GovernanceStateReader is the minimal state view required by BDAG governance
// admission policy.
type GovernanceStateReader interface {
	GetState(common.Address, common.Hash) common.Hash
}

// GovernanceAdmissibilityHook validates a transaction against chain-governed
// admission policy using the subpool's current state and signer.
type GovernanceAdmissibilityHook func(GovernanceStateReader, types.Signer, *types.Transaction) error

// GovernanceAdmissibilityConfigurer is implemented by subpools that can apply
// chain-governed admission policy before accepting transactions.
type GovernanceAdmissibilityConfigurer interface {
	SetGovernanceAdmissibilityHook(GovernanceAdmissibilityHook)
}

func (txPool *TxPool) Subpools() []SubPool {
	return txPool.subpools
}

// SetGovernanceAdmissibilityHook installs one chain-governed admission policy
// hook into every subpool that supports it.
func (txPool *TxPool) SetGovernanceAdmissibilityHook(hook GovernanceAdmissibilityHook) {
	for _, subpool := range txPool.subpools {
		configurer, ok := subpool.(GovernanceAdmissibilityConfigurer)
		if !ok {
			continue
		}
		configurer.SetGovernanceAdmissibilityHook(hook)
	}
}
