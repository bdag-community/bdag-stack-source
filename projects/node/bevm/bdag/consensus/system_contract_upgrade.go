package consensus

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	ethparams "github.com/ethereum/go-ethereum/params"
)

// StorageWrite is one storage slot assignment applied with a SystemContractUpgrade.
type StorageWrite struct {
	Account common.Address
	Slot    common.Hash
	Value   common.Hash
}

// SystemContractUpgrade describes a fork-height replacement of a system contract's
// code, plus optional storage writes, that every node applies identically to the
// pre-transaction state of block ForkBlock (see consensus.PreBlockStateChanger).
type SystemContractUpgrade struct {
	Name string
	// ForkBlock selects the activation block from the chain config; nil = never.
	ForkBlock func(*ethparams.ChainConfig) *big.Int
	// Address receives the runtime code.
	Address common.Address
	// CreationCode, when set, is executed at Address in a sandboxed EVM to derive
	// the runtime. This binds constructor-time immutables (UUPS __self) correctly.
	CreationCode []byte
	// RuntimeCode is installed verbatim when CreationCode is empty.
	RuntimeCode []byte
	// Storage writes applied after the code, e.g. flipping a proxy's ERC-1967 slot.
	Storage []StorageWrite
}

// systemContractUpgrades is the registry consulted at every block.
var systemContractUpgrades = []SystemContractUpgrade{stakingV2Upgrade}

// ApplySystemContractUpgrades applies every registered upgrade whose fork block
// equals header.Number. Re-executing block N from the same parent state yields
// the same result, so builders, importers and replays agree.
func ApplySystemContractUpgrades(config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB) {
	if config == nil || header == nil || header.Number == nil || statedb == nil {
		return
	}
	for _, u := range systemContractUpgrades {
		fork := u.ForkBlock(config)
		if fork == nil || fork.Cmp(header.Number) != 0 {
			continue
		}
		u.apply(config, header, statedb)
	}
}

func (u SystemContractUpgrade) apply(config *ethparams.ChainConfig, header *types.Header, statedb *state.StateDB) {
	runtime := u.RuntimeCode
	if len(u.CreationCode) > 0 {
		derived, err := runtimeFromCreationCode(config, header, statedb, u.Address, u.CreationCode)
		if err != nil || len(derived) == 0 {
			// Skipping on one node would split consensus; refuse to continue.
			log.Crit("System contract upgrade: cannot derive runtime", "upgrade", u.Name, "address", u.Address, "err", err)
		}
		runtime = derived
	}
	if len(runtime) == 0 {
		log.Crit("System contract upgrade: empty runtime", "upgrade", u.Name, "address", u.Address)
	}
	if !bytes.Equal(statedb.GetCode(u.Address), runtime) {
		statedb.SetCode(u.Address, runtime)
	}
	if statedb.GetNonce(u.Address) == 0 {
		// CREATE semantics; also keeps the account out of EIP-161 empty-account clearing.
		statedb.SetNonce(u.Address, 1)
	}
	for _, w := range u.Storage {
		statedb.SetState(w.Account, w.Slot, w.Value)
	}
	log.Info("Applied system contract upgrade", "upgrade", u.Name, "block", header.Number, "address", u.Address, "codeHash", crypto.Keccak256Hash(runtime))
}

// mustDecodeHex decodes an embedded bytecode file ("0x" prefix and whitespace allowed).
func mustDecodeHex(s string) []byte {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(fmt.Sprintf("embedded bytecode is not hex: %v", err))
	}
	return b
}
