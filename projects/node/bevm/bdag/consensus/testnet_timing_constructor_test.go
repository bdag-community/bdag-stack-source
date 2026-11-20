package consensus

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

// Release qualification must supply the constructor export from the matching
// pinned contract source/toolchain. It is deliberately not shipped as executable
// creation code in the native migration or used to mutate any live account.
func TestTimingConstructorSandboxEquivalence(t *testing.T) {
	path := os.Getenv("BDAG_TIMING_CONSTRUCTORS")
	if path == "" {
		t.Skip("requires matching isolated constructor export")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var constructors []struct {
		Address    common.Address `json:"address"`
		Contract   string         `json:"contract"`
		InitCode   hexutil.Bytes  `json:"initCode"`
		Relocation []struct {
			Start  int `json:"start"`
			Length int `json:"length"`
		} `json:"factoryImplementationRelocation"`
	}
	require.NoError(t, json.Unmarshal(raw, &constructors))
	require.Len(t, constructors, 10)
	c, h, s, _ := timingForkFixture(t)
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	rows, err := timingRuntimes()
	require.NoError(t, err)
	byAddress := make(map[common.Address]timingRuntime)
	for _, row := range rows {
		byAddress[row.Address] = row
	}
	root := s.IntermediateRoot(false)
	for _, input := range constructors {
		t.Run(input.Contract+input.Address.Hex(), func(t *testing.T) {
			row, ok := byAddress[input.Address]
			require.True(t, ok)
			delete(byAddress, input.Address)
			require.Equal(t, row.Contract, input.Contract)
			// Constructor side effects occur only on this disposable copy. Existing
			// migrated storage is never used as a blank deployment or written back.
			sandbox := s.Copy()
			sandbox.SetCode(input.Address, nil)
			sandbox.SetStorage(input.Address, map[common.Hash]common.Hash{})
			evm := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer,
				GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: h.Number, Time: h.Time, Difficulty: new(big.Int), BaseFee: new(big.Int), GasLimit: 30_000_000}, vm.TxContext{}, sandbox, c, vm.Config{NoBaseFee: true})
			contract := vm.NewContract(vm.AccountRef(common.Address{}), vm.AccountRef(input.Address), uint256.NewInt(0), 30_000_000)
			contract.IsDeployment = true
			contract.SetCallCode(&input.Address, crypto.Keccak256Hash(input.InitCode), input.InitCode)
			runtime, err := evm.Interpreter().Run(contract, nil, false)
			require.NoErrorf(t, err, "constructor return 0x%x", runtime)
			if row.Contract == "VersionedProjectEscrowFactory" {
				require.NotEmpty(t, input.Relocation)
				var child common.Address
				for _, ref := range input.Relocation {
					require.Equal(t, 32, ref.Length)
					require.GreaterOrEqual(t, ref.Start, 0)
					require.LessOrEqual(t, ref.Start+32, len(runtime))
					word := runtime[ref.Start : ref.Start+32]
					actual := common.BytesToAddress(word)
					require.Equal(t, common.LeftPadBytes(actual.Bytes(), 32), []byte(word))
					if child == (common.Address{}) {
						child = actual
					} else {
						require.Equal(t, child, actual)
					}
					copy(word, common.LeftPadBytes(timingEscrowAddress.Bytes(), 32))
				}
				require.NotEqual(t, timingEscrowAddress, child)
				require.Equal(t, s.GetCode(timingEscrowAddress), sandbox.GetCode(child), "only reviewed implementation-address relocation is allowed")
				for _, expected := range rows {
					if expected.Address == timingEscrowAddress {
						for slot, value := range expected.NewStorage {
							require.Equal(t, value, sandbox.GetState(child, slot))
						}
					}
				}
			} else {
				require.Empty(t, input.Relocation)
			}
			require.Equal(t, []byte(row.Code), runtime, "canonical constructor must reproduce every immutable byte")
			for slot, value := range row.NewStorage {
				require.Equal(t, value, sandbox.GetState(row.Address, slot))
			}
			require.Equal(t, root, s.IntermediateRoot(false), "sandbox must not modify candidate state")
		})
	}
	require.Empty(t, byAddress)
}
