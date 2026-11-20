package consensus

import (
	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"math/big"
)

var separateStakingAppliedSlot = crypto.Keccak256Hash([]byte("bdag.separate.staking.v1.activation"))
var separateStakingCommitmentSlot = crypto.Keccak256Hash([]byte("bdag.separate.staking.v1.manifest"))
var separateStakingActiveAddressSlot = common.BigToHash(big.NewInt(10))
var separateStakingCutoverEpochPlusSlot = common.BigToHash(big.NewInt(11))

// The production transition has its own manifest/activation namespace; it must
// not impersonate the historical Testnet SeparateStakingV1 upgrade.
func stakingTransitionMarker(s *state.StateDB) common.Hash {
	marker := s.GetState(params.ChainTallyAddr, separateStakingAppliedSlot)
	mainnet := s.GetState(params.ChainTallyAddr, mainnetDAOAppliedSlot)
	if marker != (common.Hash{}) && mainnet != (common.Hash{}) {
		panic("conflicting staking activation namespaces")
	}
	if mainnet != (common.Hash{}) {
		return mainnet
	}
	return marker
}

// Resolve from the block's canonical state, never process environment or a
// mutable latest-head cache. The transition installs all markers atomically.
func activeStakingAddress(header *types.Header, s *state.StateDB) common.Address {
	if s == nil {
		return params.StakingContractAddr
	}
	marker := stakingTransitionMarker(s)
	address := s.GetState(params.ChainTallyAddr, separateStakingActiveAddressSlot)
	epoch := s.GetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot)
	if marker == (common.Hash{}) {
		if address != (common.Hash{}) || epoch != (common.Hash{}) {
			panic("partial separate staking activation")
		}
		return params.StakingContractAddr
	}
	if header == nil || header.Number == nil || header.Number.Sign() <= 0 {
		panic("separate staking requires block identity")
	}
	if header.Number.Cmp(marker.Big()) < 0 {
		return params.StakingContractAddr
	}
	if address != common.BytesToHash(params.SeparateStakingV2Addr.Bytes()) || epoch == (common.Hash{}) || len(s.GetCode(params.SeparateStakingV2Addr)) == 0 {
		panic("invalid separate staking destination")
	}
	return params.SeparateStakingV2Addr
}

func stakingAddressForRewardEpoch(s *state.StateDB, epoch uint64) common.Address {
	if stakingTransitionMarker(s) == (common.Hash{}) {
		return params.StakingContractAddr
	}
	cutover := s.GetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot).Big()
	if cutover.Sign() == 0 || cutover.BitLen() > 64 {
		panic("invalid staking source epoch")
	}
	if epoch < cutover.Uint64()-1 {
		return params.StakingContractAddr
	}
	if s.GetState(params.ChainTallyAddr, separateStakingActiveAddressSlot) != common.BytesToHash(params.SeparateStakingV2Addr.Bytes()) {
		panic("invalid staking reward source")
	}
	return params.SeparateStakingV2Addr
}
