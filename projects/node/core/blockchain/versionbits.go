package blockchain

import "github.com/BlockdagNetworkLabs/bdag/upgrades"

const (
	// VBTopBits defines the bits to set in the version to signal that the
	// version bits scheme is being used.
	VBTopBits uint32 = 0x20000000
)

// CalcNextBlockVersion returns the version for the next block, combining the
// top bits with any upgrade signaling bits.
func (b *BlockChain) CalcNextBlockVersion() (uint32, error) {
	version := VBTopBits
	layer := uint64(1)
	if b.bd != nil {
		if tip := b.bd.GetMainChainTip(); tip != nil {
			layer = uint64(tip.GetLayer()) + 1
		}
	}
	signaling := upgrades.GetSignalingBits(layer)
	// CanonicalEVMStateV1 is retained only as a persisted-state compatibility
	// definition. Its commitment is not stable under native DAG reordering, so
	// new blocks must not signal or activate it.
	signaling &^= upgrades.CanonicalEVMStateV1BitMask
	version |= signaling
	return version, nil
}
