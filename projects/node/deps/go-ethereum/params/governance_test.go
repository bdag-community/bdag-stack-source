package params

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"testing"
)

func TestGovernanceForkCompatibility(t *testing.T) {
	a := &ChainConfig{BdagGovernanceForkBlock: big.NewInt(20), BdagGovernanceFork: &BDAGGovernanceForkConfig{ManifestHash: common.HexToHash("0x01")}}
	b := *a
	next := *a.BdagGovernanceFork
	next.ManifestHash = common.HexToHash("0x02")
	b.BdagGovernanceFork = &next
	if a.CheckCompatible(&b, 19, 0) != nil {
		t.Fatal("future config cannot be replaced")
	}
	if a.CheckCompatible(&b, 20, 0) == nil {
		t.Fatal("active authority change accepted")
	}
	b = *a
	b.BdagGovernanceForkBlock = big.NewInt(21)
	if a.CheckCompatible(&b, 20, 0) == nil {
		t.Fatal("active height change accepted")
	}
	b = *a
	b.BdagGovernanceFork = nil
	if a.CheckCompatible(&b, 20, 0) == nil {
		t.Fatal("active config removal accepted")
	}
}
func TestStakingForkCompatibility(t *testing.T) {
	old := &ChainConfig{BdagStakingV2Block: big.NewInt(20)}
	for _, height := range []*big.Int{nil, big.NewInt(21)} {
		next := *old
		next.BdagStakingV2Block = height
		if old.CheckCompatible(&next, 19, 0) != nil {
			t.Fatal("future staking fork cannot be replaced")
		}
		if old.CheckCompatible(&next, 20, 0) == nil {
			t.Fatal("active staking fork removed or moved")
		}
	}
}
