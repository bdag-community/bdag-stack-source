package consensus

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// Candidate artifacts are separate from the immutable V3 bootstrap. No release
// manifest selects them yet. Metadata and ABIs are hashed with the bytecode.
//
//go:embed bytecode/TestnetTimingV1.runtime-bundle.json
var testnetTimingBundleJSON []byte

var testnetTimingArtifactSHA256 = common.HexToHash("0xb8d100d8c69746070d02b497ee3e4c2837f9020981d036eff7d5a94ef61a5275")
var timingModuleAddress = common.HexToAddress("0x1000000000000000000000000000000000000018")
var timingEscrowAddress = common.HexToAddress("0x1000000000000000000000000000000000000019")
var timingGovernorAddress = common.HexToAddress("0x5FC8d32690cc91D4c39d9d3abcBD16989F875707")
var timingMiningLockAddress = common.HexToAddress("0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0")
var timingStakingLockAddress = common.HexToAddress("0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9")

type timingRuntime struct {
	Address          common.Address              `json:"address"`
	Contract         string                      `json:"contract"`
	Mode             string                      `json:"mode"`
	Code             hexutil.Bytes               `json:"code"`
	CodeHash         common.Hash                 `json:"codeHash"`
	PreviousCodeHash *common.Hash                `json:"previousCodeHash"`
	NewStorage       map[common.Hash]common.Hash `json:"newStorage"`
}

// An immutable digest commits the entire artifact. Parsing uses only its
// installation fields; metadata/ABIs never become runtime authority.
func timingRuntimes() ([]timingRuntime, error) {
	if common.Hash(sha256.Sum256(testnetTimingBundleJSON)) != testnetTimingArtifactSHA256 {
		return nil, fmt.Errorf("timing artifact digest mismatch")
	}
	var bundle struct {
		SchemaVersion int             `json:"schemaVersion"`
		ChainID       uint64          `json:"chainId"`
		Runtimes      []timingRuntime `json:"runtimes"`
	}
	if err := json.Unmarshal(testnetTimingBundleJSON, &bundle); err != nil {
		return nil, err
	}
	if bundle.SchemaVersion != 1 || bundle.ChainID != 1043 || len(bundle.Runtimes) != 10 {
		return nil, fmt.Errorf("invalid timing artifact shape")
	}
	want := map[common.Address]string{
		params.ChainTallyAddr: "VersionedChainTallyReader", params.StakingV2ImplAddr: "VersionedTestnetStaking",
		timingModuleAddress: "VersionedGovernanceTiming", timingEscrowAddress: "VersionedProjectEscrow",
		timingGovernorAddress: "VersionedEpochGovernor", timingMiningLockAddress: "VersionedGovernanceTimelock", timingStakingLockAddress: "VersionedGovernanceTimelock",
		common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3"): "VersionedEpochCredential",
		common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"): "VersionedEpochCredential",
		common.HexToAddress("0x8A791620dd6260079BF849Dc5567aDC3F2FdC318"): "VersionedProjectEscrowFactory",
	}
	for _, row := range bundle.Runtimes {
		if want[row.Address] != row.Contract || row.Contract == "" || len(row.Code) == 0 || len(row.Code) > 24576 || crypto.Keccak256Hash(row.Code) != row.CodeHash {
			return nil, fmt.Errorf("invalid timing runtime %s", row.Address)
		}
		delete(want, row.Address)
		old, exists := testnetBootstrapAccountAt(row.Address)
		if exists {
			if row.Mode != "replace" || row.PreviousCodeHash == nil || *row.PreviousCodeHash != crypto.Keccak256Hash(old.code) || len(row.NewStorage) != 0 {
				return nil, fmt.Errorf("invalid replacement %s", row.Address)
			}
		} else if row.Mode != "create" || row.PreviousCodeHash != nil {
			return nil, fmt.Errorf("invalid fresh runtime %s", row.Address)
		}
		if row.Address == timingModuleAddress && len(row.NewStorage) != 0 {
			return nil, fmt.Errorf("timing module must start empty")
		}
		if row.Address == timingEscrowAddress && (len(row.NewStorage) != 1 || row.NewStorage[params.SlotInitializableStorage] != initializableDisabled) {
			return nil, fmt.Errorf("invalid escrow constructor storage")
		}
	}
	if len(want) != 0 {
		return nil, fmt.Errorf("missing timing runtime")
	}
	sort.Slice(bundle.Runtimes, func(i, j int) bool { return bundle.Runtimes[i].Address.Hex() < bundle.Runtimes[j].Address.Hex() })
	return bundle.Runtimes, nil
}
