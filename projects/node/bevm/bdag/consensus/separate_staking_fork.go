package consensus

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"math/big"
)

//go:embed bytecode/SeparateStakingV1.runtime-bundle.json
var separateStakingBundleJSON []byte
var separateStakingArtifactSHA256 = common.HexToHash("0x3e93657b71f9cdf3562a61c963541e9a41417c6a1ee98e6e1933ef1acedf7436")

type separatedRouteCopy struct{ FromSlot, ToSlot, Value common.Hash }
type separatedRuntimeBundle struct {
	SchemaVersion int             `json:"schemaVersion"`
	ChainID       uint64          `json:"chainId"`
	GenesisTime   uint64          `json:"genesisTime"`
	Runtimes      []timingRuntime `json:"runtimes"`
	PreservedCode []struct {
		Address  common.Address
		CodeHash common.Hash
	} `json:"preservedCode"`
	RouteCopies     []separatedRouteCopy `json:"routeCopies"`
	OwnerDomainCopy separatedRouteCopy   `json:"ownerDomainCopy"`
}

func separateStakingRuntimes() (*separatedRuntimeBundle, error) {
	if common.Hash(sha256.Sum256(separateStakingBundleJSON)) != separateStakingArtifactSHA256 {
		return nil, fmt.Errorf("separate staking artifact digest mismatch")
	}
	var b separatedRuntimeBundle
	if err := json.Unmarshal(separateStakingBundleJSON, &b); err != nil {
		return nil, err
	}
	if b.SchemaVersion != 1 || b.ChainID != 1043 || b.GenesisTime != 1758551400 || len(b.Runtimes) != 5 || len(b.RouteCopies) != 6 || len(b.PreservedCode) != 18 {
		return nil, fmt.Errorf("invalid separate staking artifact shape")
	}
	want := map[common.Address]string{params.ChainTallyAddr: "SeparatedChainTallyReader", params.SeparateStakingV2Addr: "UUPSProxy", params.SeparateStakingV2ImplAddr: "SeparatedStaking",
		common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3"): "SeparatedEpochCredential", common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"): "SeparatedEpochCredential"}
	for _, r := range b.Runtimes {
		if want[r.Address] != r.Contract || r.Contract == "" || len(r.Code) == 0 || len(r.Code) > 24576 || crypto.Keccak256Hash(r.Code) != r.CodeHash || len(r.NewStorage) != 0 {
			return nil, fmt.Errorf("invalid separate staking runtime %s", r.Address)
		}
		delete(want, r.Address)
		fresh := r.Address == params.SeparateStakingV2Addr || r.Address == params.SeparateStakingV2ImplAddr
		if (fresh && (r.Mode != "create" || r.PreviousCodeHash != nil)) || (!fresh && (r.Mode != "replace" || r.PreviousCodeHash == nil)) {
			return nil, fmt.Errorf("invalid separate staking replacement mode")
		}
	}
	if len(want) != 0 {
		return nil, fmt.Errorf("missing separate staking runtime")
	}
	return &b, nil
}

func separateStakingCommitment(c *ethparams.ChainConfig) common.Hash {
	raw, _ := json.Marshal(c.BdagSeparateStaking)
	return crypto.Keccak256Hash(c.BdagSeparateStakingBlock.Bytes(), raw)
}

// All fallible checks precede writes; builders and importers call the same
// pre-transaction hook. Retiring balances and position storage are not written.
func ApplySeparateStakingFork(c *ethparams.ChainConfig, h *types.Header, s *state.StateDB) error {
	if c == nil || (c.BdagSeparateStakingBlock == nil && c.BdagSeparateStaking == nil) {
		if s != nil && s.GetState(params.ChainTallyAddr, separateStakingAppliedSlot) != (common.Hash{}) {
			return fmt.Errorf("persisted separate staking requires its sealed policy")
		}
		return nil
	}
	if err := c.BdagSeparateStaking.Validate(c.ChainID); err != nil {
		return err
	}
	if c.BdagSeparateStakingBlock == nil || c.BdagSeparateStakingBlock.Sign() <= 0 || h == nil || h.Number == nil || h.Number.Sign() <= 0 || h.Number.BitLen() > 256 {
		return fmt.Errorf("invalid separate staking block")
	}
	if s == nil {
		return fmt.Errorf("separate staking requires state")
	}
	if c.BdagSeparateStaking.ArtifactSHA256 != separateStakingArtifactSHA256 {
		return fmt.Errorf("release does not pin separate staking artifact")
	}
	commitment := separateStakingCommitment(c)
	marker := s.GetState(params.ChainTallyAddr, separateStakingAppliedSlot)
	if marker != (common.Hash{}) {
		if marker.Big().Cmp(h.Number) > 0 || marker.Big().Cmp(c.BdagSeparateStakingBlock) < 0 || s.GetState(params.ChainTallyAddr, separateStakingCommitmentSlot) != commitment || s.GetState(params.ChainTallyAddr, separateStakingActiveAddressSlot) != common.BytesToHash(params.SeparateStakingV2Addr.Bytes()) || s.GetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot) == (common.Hash{}) {
			return fmt.Errorf("separate staking marker/manifest mismatch")
		}
		return nil
	}
	if h.Number.Cmp(c.BdagSeparateStakingBlock) < 0 {
		return nil
	}
	epoch, ready, err := planSeparateStaking(c, h, s, upgrades.GetUpgrades()[upgrades.SeparateStakingV1Name])
	if err != nil || !ready {
		return err
	}
	b, err := separateStakingRuntimes()
	if err != nil {
		return err
	}
	for _, r := range b.Runtimes {
		if r.Mode == "create" {
			if s.Exist(r.Address) {
				return fmt.Errorf("occupied separate staking destination %s", r.Address)
			}
		} else if s.GetCodeHash(r.Address) != *r.PreviousCodeHash {
			return fmt.Errorf("unexpected old staking-source code at %s", r.Address)
		}
	}
	for _, r := range b.PreservedCode {
		if s.GetCodeHash(r.Address) != r.CodeHash {
			return fmt.Errorf("unexpected preserved runtime %s", r.Address)
		}
	}
	for _, copy := range append(append([]separatedRouteCopy{}, b.RouteCopies...), b.OwnerDomainCopy) {
		if copy.Value == (common.Hash{}) || copy.FromSlot == copy.ToSlot || s.GetState(timingGovernorAddress, copy.FromSlot) != copy.Value || s.GetState(timingGovernorAddress, copy.ToSlot) != (common.Hash{}) {
			return fmt.Errorf("invalid reviewed staking route pre-state")
		}
	}
	if s.GetState(params.StakingContractAddr, common.Hash{}) != common.BytesToHash(timingStakingLockAddress.Bytes()) || s.GetState(params.StakingContractAddr, params.SlotERC1967Implementation) != common.BytesToHash(params.StakingV2ImplAddr.Bytes()) {
		return fmt.Errorf("unexpected retiring staking authority/implementation")
	}
	for _, slot := range []int64{22, 23, 28} {
		if s.GetState(params.StakingContractAddr, common.BigToHash(big.NewInt(slot))) != (common.Hash{}) {
			return fmt.Errorf("retiring staking has pending/retired authority")
		}
	}
	for _, check := range []struct {
		slot  common.Hash
		value uint64
	}{{params.SlotMinerSplit, 70}, {params.SlotStakerSplit, 30}} {
		if s.GetState(params.StakingContractAddr, check.slot) != common.BigToHash(new(big.Int).SetUint64(check.value)) {
			return fmt.Errorf("unexpected consensus subsidy split")
		}
	}
	// No fallible operations below: code, constructor state, routes and markers
	// become visible together as part of the block's state transition.
	for _, r := range b.Runtimes {
		s.SetCode(r.Address, r.Code)
		if r.Mode == "create" {
			s.SetNonce(r.Address, 1)
		}
	}
	s.SetState(params.SeparateStakingV2ImplAddr, params.SlotInitializableStorage, initializableDisabled)
	for slot, value := range map[common.Hash]common.Hash{
		common.Hash{}:                    common.BytesToHash(timingStakingLockAddress.Bytes()),
		params.SlotERC1967Implementation: common.BytesToHash(params.SeparateStakingV2ImplAddr.Bytes()),
		params.SlotMinerSplit:            common.BigToHash(big.NewInt(70)), params.SlotStakerSplit: common.BigToHash(big.NewInt(30)),
		params.SlotGenesisTime:          common.BigToHash(new(big.Int).SetUint64(b.GenesisTime)),
		params.SlotInitializableStorage: common.BigToHash(big.NewInt(2)), params.SlotReentrancyGuardState: common.BigToHash(big.NewInt(1)),
		common.BigToHash(big.NewInt(18)): common.BigToHash(new(big.Int).SetUint64(h.Time)),
		common.BigToHash(big.NewInt(32)): common.BigToHash(big.NewInt(256)),
	} {
		s.SetState(params.SeparateStakingV2Addr, slot, value)
	}
	for _, copy := range append(append([]separatedRouteCopy{}, b.RouteCopies...), b.OwnerDomainCopy) {
		s.SetState(timingGovernorAddress, copy.ToSlot, copy.Value)
	}
	s.SetState(params.ChainTallyAddr, separateStakingActiveAddressSlot, common.BytesToHash(params.SeparateStakingV2Addr.Bytes()))
	s.SetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot, common.BigToHash(new(big.Int).SetUint64(epoch+1)))
	s.SetState(params.ChainTallyAddr, separateStakingCommitmentSlot, commitment)
	s.SetState(params.ChainTallyAddr, separateStakingAppliedSlot, common.BigToHash(h.Number))
	return nil
}
