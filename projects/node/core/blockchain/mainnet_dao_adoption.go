package blockchain

import (
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	ethcore "github.com/ethereum/go-ethereum/core"
)

// mainnetDAOAdoptionProof reads only the stored native tip. If the databases
// are not cleanly paired or the tip could already enforce the upgrade, the
// EVM compatibility check retains its normal fail-closed behavior.
func (b *BlockChain) mainnetDAOAdoptionProof() *ethcore.MainnetDAOAdoptionProof {
	g := evmparams.BdagMainnetDAO
	if params.ActiveNetParams != &params.MainNetParam || g == nil || b.bd == nil || b.DB() == nil {
		return nil
	}
	_, pending, err := b.pendingBlockApplyRecovery()
	if err != nil || pending {
		return nil
	}
	raw, err := b.DB().GetBestChainState()
	if err != nil || raw == nil {
		return nil
	}
	best, err := DeserializeBestChainState(raw)
	if err != nil {
		return nil
	}
	id, err := b.DB().GetDAGBlockIdByHash(&best.hash)
	if err != nil || id == 0 {
		return nil
	}
	tip, err := b.bd.ReadBlockById(id)
	if err != nil || tip == nil || tip.GetHash() == nil || *tip.GetHash() != best.hash || tip.GetState() == nil {
		return nil
	}
	layer := uint64(tip.GetLayer())
	if layer >= g.SignalStartLayer+99999+g.ActivationDelayLayers {
		return nil
	}
	state := tip.GetState()
	if state.GetEVMHash() == (common.Hash{}) {
		return nil
	}
	return &ethcore.MainnetDAOAdoptionProof{NativeLayer: layer, EVMHash: state.GetEVMHash(), EVMRoot: state.GetEVMRoot(), EVMNumber: state.GetEVMNumber()}
}
