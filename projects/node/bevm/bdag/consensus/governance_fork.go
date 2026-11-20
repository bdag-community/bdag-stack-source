package consensus

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
)

// These are separate artifacts: the historical fork-53 bytecode is immutable.
// Creation executes in a reverted sandbox at the final address so constructor
// arguments and UUPS __self immutables are reproduced deterministically.
//
//go:embed bytecode/StakingContractV2Governance.creation.hex
var governanceStakingCreationHex string

//go:embed bytecode/ChainTallyReader.creation.hex
var governanceReaderCreationHex string

//go:embed bytecode/GovernanceHub.creation.hex
var governanceHubCreationHex string

//go:embed bytecode/GovernanceProxyLegacy.creation.hex
var governanceProxyLegacyCreationHex string

//go:embed bytecode/GovernanceProxyLegacy.runtime.hex
var governanceProxyLegacyRuntimeHex string

// testnetGovernanceBootstrapJSON is the reproducible clean-testnet allocation.
// Its digest is pinned independently so an artifact edit cannot silently alter
// consensus state.
//
//go:embed bytecode/TestnetGovernanceBootstrap.alloc.json
var testnetGovernanceBootstrapJSON []byte

type testnetBootstrapStorage struct {
	key, value common.Hash
}

type testnetBootstrapAccount struct {
	address common.Address
	nonce   uint64
	code    []byte
	storage []testnetBootstrapStorage
}

var (
	governanceStakingCreationCode      = mustDecodeHex(governanceStakingCreationHex)
	governanceReaderCreationCode       = mustDecodeHex(governanceReaderCreationHex)
	governanceHubCreationCode          = mustDecodeHex(governanceHubCreationHex)
	governanceProxyLegacyCreationCode  = mustDecodeHex(governanceProxyLegacyCreationHex)
	governanceProxyLegacyRuntimeCode   = mustDecodeHex(governanceProxyLegacyRuntimeHex)
	governanceHubV2Addr                = common.HexToAddress("0x1000000000000000000000000000000000000016")
	governanceAppliedSlot              = crypto.Keccak256Hash([]byte("bdag.governance.v3.activation.manifest"))
	testnetGovernanceBootstrapSHA256   = mustSHA256("304aeee52c451e383f1960bdc32d0415d1ad9bc3e0fe19dff95af197bc82d199")
	testnetGovernanceManifestHash      = common.HexToHash("0x2974bfdce5e574db5df0a2ca49c4fd8d5f8f78adb9845ba3f741125a0d0c7529")
	testnetGovernanceBootstrapAccounts = mustParseTestnetGovernanceBootstrap(testnetGovernanceBootstrapJSON)
)

func mustSHA256(value string) [sha256.Size]byte {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		panic("invalid pinned testnet governance bootstrap digest")
	}
	var result [sha256.Size]byte
	copy(result[:], raw)
	return result
}

func mustParseTestnetGovernanceBootstrap(raw []byte) []testnetBootstrapAccount {
	if sha256.Sum256(raw) != testnetGovernanceBootstrapSHA256 {
		panic("testnet governance bootstrap digest mismatch")
	}
	type encodedAccount struct {
		Balance string            `json:"balance"`
		Nonce   uint64            `json:"nonce"`
		Code    string            `json:"code"`
		Storage map[string]string `json:"storage"`
	}
	var encoded map[string]encodedAccount
	if err := json.Unmarshal(raw, &encoded); err != nil {
		panic(fmt.Sprintf("invalid testnet governance bootstrap allocation: %v", err))
	}
	if len(encoded) != 19 {
		panic(fmt.Sprintf("testnet governance bootstrap has %d accounts, want 19", len(encoded)))
	}
	seen := make(map[common.Address]bool, len(encoded))
	accounts := make([]testnetBootstrapAccount, 0, len(encoded))
	for textAddress, encodedAccount := range encoded {
		if !common.IsHexAddress(textAddress) || encodedAccount.Balance != "0" {
			panic(fmt.Sprintf("invalid testnet governance bootstrap account %q", textAddress))
		}
		address := common.HexToAddress(textAddress)
		if seen[address] {
			panic(fmt.Sprintf("duplicate testnet governance bootstrap account %s", address))
		}
		seen[address] = true
		code, err := hex.DecodeString(strings.TrimPrefix(encodedAccount.Code, "0x"))
		if err != nil || len(code) == 0 || !strings.HasPrefix(encodedAccount.Code, "0x") {
			panic(fmt.Sprintf("invalid testnet governance bootstrap code at %s", address))
		}
		account := testnetBootstrapAccount{address: address, nonce: encodedAccount.Nonce, code: code}
		for textKey, textValue := range encodedAccount.Storage {
			if len(textKey) != 66 || len(textValue) != 66 || !strings.HasPrefix(textKey, "0x") || !strings.HasPrefix(textValue, "0x") {
				panic(fmt.Sprintf("invalid testnet governance bootstrap storage at %s", address))
			}
			if _, err := hex.DecodeString(textKey[2:]); err != nil {
				panic(fmt.Sprintf("invalid testnet governance bootstrap storage key at %s", address))
			}
			if _, err := hex.DecodeString(textValue[2:]); err != nil {
				panic(fmt.Sprintf("invalid testnet governance bootstrap storage value at %s", address))
			}
			account.storage = append(account.storage, testnetBootstrapStorage{key: common.HexToHash(textKey), value: common.HexToHash(textValue)})
		}
		sort.Slice(account.storage, func(i, j int) bool { return bytes.Compare(account.storage[i].key[:], account.storage[j].key[:]) < 0 })
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return bytes.Compare(accounts[i].address[:], accounts[j].address[:]) < 0 })
	return accounts
}

func testnetBootstrapAccountAt(address common.Address) (testnetBootstrapAccount, bool) {
	i := sort.Search(len(testnetGovernanceBootstrapAccounts), func(i int) bool {
		return bytes.Compare(testnetGovernanceBootstrapAccounts[i].address[:], address[:]) >= 0
	})
	if i == len(testnetGovernanceBootstrapAccounts) || testnetGovernanceBootstrapAccounts[i].address != address {
		return testnetBootstrapAccount{}, false
	}
	return testnetGovernanceBootstrapAccounts[i], true
}

func validateTestnetBootstrapManifest(g *ethparams.BDAGGovernanceForkConfig) error {
	if g.ManifestHash != testnetGovernanceManifestHash {
		return fmt.Errorf("testnet governance manifest hash mismatch")
	}
	for _, expected := range []struct {
		address common.Address
		hash    common.Hash
	}{
		{g.StakingTimelock, g.StakingTimelockCodeHash},
		{g.MiningTimelock, g.MiningTimelockCodeHash},
		{g.Governor, g.GovernorCodeHash},
		{mparams.StakingV2ImplAddr, g.StakingRuntimeHash},
		{mparams.ChainTallyAddr, g.ReaderRuntimeHash},
		{governanceHubV2Addr, g.HubRuntimeHash},
		{governanceProxyAddr, g.LegacyProxyRuntimeHash},
	} {
		account, ok := testnetBootstrapAccountAt(expected.address)
		if !ok || crypto.Keccak256Hash(account.code) != expected.hash {
			return fmt.Errorf("testnet governance bootstrap artifact mismatch at %s", expected.address)
		}
	}
	return nil
}

func applyTestnetGovernanceBootstrap(g *ethparams.BDAGGovernanceForkConfig, header *types.Header, s *state.StateDB) error {
	if err := validateTestnetBootstrapManifest(g); err != nil {
		return err
	}
	// Validate the complete target set before the first write. This bootstrap is
	// only valid for a clean testnet and deliberately rejects mixed state. The
	// staking address is the one exception to a zero balance: consensus rewards
	// accrue there before the proxy code is installed. The ChainTally is also a
	// protocol-owned predeploy: reward accounting gives it nonce 1 and historical
	// storage before the reader code is installed. Preserve those engine-owned
	// fields, but still reject code, an unexpected nonce/balance, and collisions
	// with every manifest-owned slot before making any write.
	for _, account := range testnetGovernanceBootstrapAccounts {
		nonce := s.GetNonce(account.address)
		balanceIsZero := s.GetBalance(account.address).IsZero()
		validProtocolPrestate := false
		switch account.address {
		case mparams.StakingContractAddr:
			validProtocolPrestate = nonce == 0
		case mparams.ChainTallyAddr:
			validProtocolPrestate = balanceIsZero && (nonce == 0 || nonce == 1)
		default:
			validProtocolPrestate = balanceIsZero && nonce == 0
		}
		if len(s.GetCode(account.address)) != 0 || !validProtocolPrestate {
			return fmt.Errorf("testnet governance bootstrap target is not empty at %s", account.address)
		}
		for _, slot := range account.storage {
			if s.GetState(account.address, slot.key) != (common.Hash{}) {
				return fmt.Errorf("testnet governance bootstrap target is not empty at %s slot %s", account.address, slot.key)
			}
		}
	}
	for _, account := range testnetGovernanceBootstrapAccounts {
		s.SetCode(account.address, account.code)
		if account.nonce != 0 {
			s.SetNonce(account.address, account.nonce)
		}
		for _, slot := range account.storage {
			s.SetState(account.address, slot.key, slot.value)
		}
	}
	// This marker is always the final write and makes re-execution idempotent.
	s.SetState(mparams.ChainTallyAddr, governanceAppliedSlot, common.BigToHash(header.Number))
	return nil
}

// ApplyGovernanceFork performs the manifest-pinned owner rotation.
// It is a pure function of persisted chain configuration, parent state and the
// incoming header. Every validation and constructor derivation completes before
// any migration write; the applied block marker is written last in the group.
func ApplyGovernanceFork(config *ethparams.ChainConfig, header *types.Header, s *state.StateDB) error {
	if config == nil || config.BdagGovernanceForkBlock == nil || header == nil || header.Number == nil || header.Number.Cmp(config.BdagGovernanceForkBlock) < 0 {
		return nil
	}
	if s == nil {
		return fmt.Errorf("governance fork requires state")
	}
	g := config.BdagGovernanceFork
	if err := g.Validate(); err != nil {
		return err
	}
	if config.ChainID == nil {
		return fmt.Errorf("governance fork requires chain ID")
	}
	testnetBootstrap := false
	switch {
	case config.ChainID.Cmp(mparams.BdagDevnetChainConfig.ChainID) == 0:
		if g.TestnetRehearsal {
			return fmt.Errorf("testnet governance rehearsal policy is restricted to chain 1043")
		}
		// Preserve the already-rehearsed private-devnet activation path.
	case config.ChainID.Cmp(mparams.BdagTestnetChainConfig.ChainID) == 0:
		if !g.TestnetRehearsal {
			return fmt.Errorf("testnet governance bootstrap requires rehearsal policy")
		}
		testnetBootstrap = true
		// Testnet activation is driven only by the native version-bit result.
		// A legacy reward-only header means the activation layer has not yet
		// been reached, so this pre-block hook remains a no-op.
		if len(header.Extra) == RewardExtraLength {
			return nil
		}
		_, nativeLayer, err := DecodeRewardExtraWithLayer(header.Extra)
		if err != nil {
			return fmt.Errorf("governance fork native-layer binding: %w", err)
		}
		definition, ok := upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name]
		if !ok || !definition.AnchorSet || !definition.LockedIn {
			return fmt.Errorf("governance fork native signal is not locked in")
		}
		if definition.ActivateAnchor != g.ActivationLayer {
			return fmt.Errorf("governance activation layer %d does not match native signal anchor %d", g.ActivationLayer, definition.ActivateAnchor)
		}
		if nativeLayer < g.ActivationLayer || !upgrades.IsEnforcedAtLayer(upgrades.RewardGovernanceV3Name, nativeLayer) {
			return fmt.Errorf("governance fork is not enforced at native layer %d", nativeLayer)
		}
	default:
		return fmt.Errorf("governance fork is restricted to private devnet chain 8008 and testnet chain 1043")
	}
	if s.GetState(mparams.ChainTallyAddr, governanceAppliedSlot) != (common.Hash{}) {
		return nil
	}
	// The release manifest is immutable in ChainConfig. It is produced only
	// after the 1500/2000 native signal threshold and its activation layer is at
	// least 2000 layers later. The timestamp independently enforces public notice.
	if header.Time < g.NotBeforeTimestamp {
		return nil
	}
	if header.Number.Sign() <= 0 || header.Number.BitLen() > 256 {
		return fmt.Errorf("invalid governance activation block")
	}
	if testnetBootstrap {
		return applyTestnetGovernanceBootstrap(g, header, s)
	}
	for _, a := range []struct {
		addr common.Address
		hash common.Hash
	}{{g.StakingTimelock, g.StakingTimelockCodeHash}, {g.MiningTimelock, g.MiningTimelockCodeHash}, {g.Governor, g.GovernorCodeHash}} {
		if len(s.GetCode(a.addr)) == 0 || s.GetCodeHash(a.addr) != a.hash {
			return fmt.Errorf("governance authority code hash mismatch at %s", a.addr)
		}
		for _, target := range []common.Address{mparams.StakingContractAddr, mparams.StakingV2ImplAddr, mparams.ChainTallyAddr, governanceHubV2Addr, governanceProxyAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr} {
			if a.addr == target {
				return fmt.Errorf("governance authority overlaps migration target %s", target)
			}
		}
	}
	if crypto.Keccak256Hash(governanceProxyLegacyCreationCode) != g.LegacyProxyCreationHash || crypto.Keccak256Hash(governanceProxyLegacyRuntimeCode) != g.LegacyProxyRuntimeHash {
		return fmt.Errorf("governance legacy proxy artifact hash mismatch")
	}
	// Only this audited inert proxy is repaired. Never scan for byte patterns or
	// touch the unused historical staking proxy/implementation placeholders.
	if s.GetCodeHash(governanceProxyAddr) != g.LegacyProxyCreationHash {
		return fmt.Errorf("unexpected governance proxy code before activation")
	}
	for _, a := range []common.Address{mparams.StakingContractAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr} {
		if len(s.GetCode(a)) == 0 {
			return fmt.Errorf("missing existing governance target at %s", a)
		}
	}
	rows := []struct {
		addr                      common.Address
		creation                  []byte
		creationHash, runtimeHash common.Hash
		runtime                   []byte
	}{
		{addr: mparams.StakingV2ImplAddr, creation: governanceStakingCreationCode, creationHash: g.StakingCreationHash, runtimeHash: g.StakingRuntimeHash},
		{addr: mparams.ChainTallyAddr, creation: governanceReaderCreationCode, creationHash: g.ReaderCreationHash, runtimeHash: g.ReaderRuntimeHash},
		{addr: governanceHubV2Addr, creation: governanceHubCreationCode, creationHash: g.HubCreationHash, runtimeHash: g.HubRuntimeHash},
	}
	for i := range rows {
		r := &rows[i]
		if len(s.GetCode(r.addr)) != 0 {
			return fmt.Errorf("governance destination already has code at %s", r.addr)
		}
		if crypto.Keccak256Hash(r.creation) != r.creationHash {
			return fmt.Errorf("governance creation artifact mismatch at %s", r.addr)
		}
		runtime, err := runtimeFromCreationCode(config, header, s, r.addr, r.creation)
		if err != nil {
			return fmt.Errorf("governance constructor at %s: %w", r.addr, err)
		}
		if len(runtime) == 0 || crypto.Keccak256Hash(runtime) != r.runtimeHash {
			return fmt.Errorf("governance runtime artifact mismatch at %s", r.addr)
		}
		r.runtime = runtime
	}
	// StateDB writes below cannot return errors. The pre-block hook is applied to
	// the candidate state; normal block validation commits or discards it as one.
	for _, r := range rows {
		s.SetCode(r.addr, r.runtime)
		if s.GetNonce(r.addr) == 0 {
			s.SetNonce(r.addr, 1)
		}
	}
	s.SetCode(governanceProxyAddr, governanceProxyLegacyRuntimeCode)
	s.SetState(mparams.StakingV2ImplAddr, mparams.SlotInitializableStorage, initializableDisabled)
	s.SetState(mparams.StakingContractAddr, erc1967ImplementationSlot, common.BytesToHash(mparams.StakingV2ImplAddr.Bytes()))
	s.SetState(mparams.StakingContractAddr, common.Hash{}, common.BytesToHash(g.StakingTimelock.Bytes()))
	// Retired emergency-key slots are explicitly cleared. daoInitialized is set
	// deterministically (offset 1 in slot 32); no post-fork bootstrap transaction
	// or principal seed is required for reward voting.
	s.SetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(22)), common.Hash{})
	s.SetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(23)), common.Hash{})
	daoFlags := s.GetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(32))).Big()
	daoFlags.Or(daoFlags, big.NewInt(0x100))
	s.SetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(32)), common.BigToHash(daoFlags))
	s.SetState(governanceProxyAddr, erc1967ImplementationSlot, common.BytesToHash(governanceHubV2Addr.Bytes()))
	// This legacy proxy is bare ERC1967. Its admin slot is metadata; the Hub's
	// immutable governor and executing timelock enforce UUPS authorization.
	s.SetState(governanceProxyAddr, erc1967AdminSlot, common.BytesToHash(g.MiningTimelock.Bytes()))
	for _, a := range []common.Address{governanceProxyAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr} {
		s.SetState(a, common.Hash{}, common.BytesToHash(g.MiningTimelock.Bytes()))
	}
	s.SetState(mparams.ChainTallyAddr, governanceAppliedSlot, common.BigToHash(header.Number))
	return nil
}
