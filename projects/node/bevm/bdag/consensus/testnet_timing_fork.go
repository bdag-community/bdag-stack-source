package consensus

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
)

var testnetTimingCommitmentSlot = crypto.Keccak256Hash([]byte("bdag.testnet.timing.v1.manifest"))

func timingConfigCommitment(config *ethparams.ChainConfig) common.Hash {
	encoded, _ := json.Marshal(config.BdagTestnetTiming)
	return crypto.Keccak256Hash(config.BdagTestnetTimingBlock.Bytes(), encoded)
}

// ApplyTestnetTimingFork is not scheduled by a shipped manifest.
func ApplyTestnetTimingFork(config *ethparams.ChainConfig, header *types.Header, s *state.StateDB) error {
	if config == nil || (config.BdagTestnetTiming == nil && config.BdagTestnetTimingBlock == nil) {
		return nil
	}
	if err := config.BdagTestnetTiming.Validate(config.ChainID); err != nil {
		return err
	}
	if qparams.ActiveNetParams == nil || qparams.ActiveNetParams.Net != qparams.TestNetParams.Net || params.StakingContractAddr != common.HexToAddress("0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7") {
		return fmt.Errorf("timing correction requires canonical native Testnet")
	}
	if config.BdagTestnetTimingBlock == nil || config.BdagTestnetTimingBlock.Sign() <= 0 || header == nil || header.Number == nil || header.Number.Sign() <= 0 || header.Number.BitLen() > 256 {
		return fmt.Errorf("invalid timing block")
	}
	if header.Number.Cmp(config.BdagTestnetTimingBlock) < 0 {
		return nil
	}
	if s == nil {
		return fmt.Errorf("timing correction requires state")
	}
	if config.BdagTestnetTiming.ArtifactSHA256 != testnetTimingArtifactSHA256 {
		return fmt.Errorf("release does not select the pinned timing artifact")
	}
	marker := s.GetState(params.ChainTallyAddr, testnetTimingAppliedSlot)
	commitment := timingConfigCommitment(config)
	if marker != (common.Hash{}) {
		if marker.Big().Cmp(header.Number) > 0 || marker.Big().Cmp(config.BdagTestnetTimingBlock) < 0 || s.GetState(params.ChainTallyAddr, testnetTimingCommitmentSlot) != commitment {
			return fmt.Errorf("timing activation marker or manifest commitment mismatch")
		}
		return nil // Do not overwrite later valid DAO code changes or append twice.
	}
	segment, err := planTestnetTimingClock(config, header, s, upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name])
	if err != nil || segment == nil {
		return err
	}
	if s.GetState(params.ChainTallyAddr, testnetTimingCommitmentSlot) != (common.Hash{}) {
		return fmt.Errorf("partial timing commitment")
	}
	rows, err := timingRuntimes()
	if err != nil {
		return err
	}
	// All historical runtime hashes must match, including unchanged authority and
	// the old escrow used by existing clones. No balance or nonce is overwritten.
	for _, old := range testnetGovernanceBootstrapAccounts {
		if s.GetCodeHash(old.address) != crypto.Keccak256Hash(old.code) || s.GetNonce(old.address) < old.nonce {
			return fmt.Errorf("unexpected timing pre-state code/nonce at %s", old.address)
		}
	}
	for _, address := range []common.Address{timingModuleAddress, timingEscrowAddress} {
		if s.Exist(address) {
			return fmt.Errorf("timing destination already exists at %s", address)
		}
	}
	// Original owner/proxy identity and retired-key absence are migration
	// preconditions, never administrative writes. User state remains untouched.
	checks := []struct {
		address     common.Address
		slot, value common.Hash
	}{
		{params.StakingContractAddr, common.Hash{}, common.BytesToHash(timingStakingLockAddress.Bytes())},
		{params.StakingContractAddr, erc1967ImplementationSlot, common.BytesToHash(params.StakingV2ImplAddr.Bytes())},
		{params.StakingContractAddr, params.SlotGenesisTime, common.BigToHash(big.NewInt(1758551400))},
		{params.BlocklistContractAddr, common.Hash{}, common.BytesToHash(timingMiningLockAddress.Bytes())},
		{params.SelectiveBlocklistContractAddr, common.Hash{}, common.BytesToHash(timingMiningLockAddress.Bytes())},
	}
	for _, slot := range []int64{22, 23, 41, 42} {
		checks = append(checks, struct {
			address     common.Address
			slot, value common.Hash
		}{params.StakingContractAddr, common.BigToHash(big.NewInt(slot)), common.Hash{}})
	}
	for _, address := range []common.Address{timingGovernorAddress, timingMiningLockAddress, timingStakingLockAddress} {
		old, _ := testnetBootstrapAccountAt(address)
		for _, slot := range old.storage {
			checks = append(checks, struct {
				address     common.Address
				slot, value common.Hash
			}{address, slot.key, slot.value})
		}
	}
	for _, check := range checks {
		if s.GetState(check.address, check.slot) != check.value {
			return fmt.Errorf("unexpected timing authority/storage at %s slot %s", check.address, check.slot)
		}
	}
	// The planner has checked all bounded clock arithmetic and marker state.
	// Nothing below can return an error. Block processing commits/discards these
	// writes with the candidate state as one atomic operation.
	for _, row := range rows {
		s.SetCode(row.Address, row.Code)
		if row.Mode == "create" {
			s.SetNonce(row.Address, 1)
			for slot, value := range row.NewStorage {
				s.SetState(row.Address, slot, value)
			}
		}
	}
	s.SetState(params.ChainTallyAddr, params.SlotRewardEpochScheduleLength, common.BigToHash(big.NewInt(2)))
	genesis := uint64(qparams.TestNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	for i, value := range []uint64{0, genesis, 60, segment.FirstEpoch, segment.StartTime, segment.Duration} {
		s.SetState(params.ChainTallyAddr, rewardScheduleWordSlot(int64(i)), common.BigToHash(new(big.Int).SetUint64(value)))
	}
	advanceRewardVotingEpoch(s, segment.FirstEpoch-1)
	s.SetState(params.ChainTallyAddr, params.SlotGovernanceTimeFloor, common.BigToHash(new(big.Int).SetUint64(max(header.Time, segment.StartTime-60))))
	s.SetState(params.ChainTallyAddr, testnetTimingCommitmentSlot, commitment)
	s.SetState(params.ChainTallyAddr, testnetTimingAppliedSlot, common.BigToHash(header.Number))
	return nil
}
