package bdag

import (
	"fmt"
	"math"

	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
)

// The pending transaction-selection state is speculative, not a consensus
// block. Give its pre-block hooks the prospective native layer, not an EVM
// height. Actual native templates and imports independently bind and validate
// their own reward/layer in buildBlock. Do not change legacy template behavior
// on networks without the new timing transition.
func prepareTimingTemplateRewardExtra(config *ethparams.ChainConfig, header *types.Header, tip model.Block) error {
	if config == nil || (config.BdagTestnetTiming == nil && config.BdagMainnetDAO == nil) {
		return nil
	}
	if header == nil || tip == nil || uint64(tip.GetLayer()) == math.MaxUint64 {
		return fmt.Errorf("timing template requires a bounded native parent layer")
	}
	layer := uint64(tip.GetLayer()) + 1
	if upgrades.RewardLayerBindingEnforced(layer) {
		header.Extra = bdagconsensus.EncodeRewardExtraWithLayer(0, layer)
	}
	return nil
}
