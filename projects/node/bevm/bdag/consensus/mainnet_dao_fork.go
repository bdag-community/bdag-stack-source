package consensus

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethconsensus "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// A preparation build has no production program. Final release sealing replaces
// null with an independently reproduced bundle and its compiled profile digest.
// No process flag, wallet, environment variable or RPC can supply these bytes.
//
//go:embed bytecode/MainnetDAOStakingV2.bundle.json
var mainnetDAOArtifactJSON []byte

var mainnetDAOAppliedSlot = crypto.Keccak256Hash([]byte("bdag.mainnet.dao.staking.v2.activation"))
var mainnetDAOCommitmentSlot = crypto.Keccak256Hash([]byte("bdag.mainnet.dao.staking.v2.manifest"))

var mainnetDAORoles = []string{"reader", "legacyImplementation", "stakingV2Implementation", "stakingV2",
	"miningCredential", "stakingCredential", "governor", "miningTimelock", "stakingTimelock", "timing", "policy", "hub",
	"developers", "bondVault", "treasury", "factory", "escrow", "miningRegistry", "legacyEscrow"}

type mainnetDAOAccount struct {
	Role     string                      `json:"role"`
	Address  common.Address              `json:"address"`
	Code     hexutil.Bytes               `json:"code"`
	CodeHash common.Hash                 `json:"codeHash"`
	Nonce    uint64                      `json:"nonce"`
	Storage  map[common.Hash]common.Hash `json:"storage,omitempty"`
}

// Only fresh accounts accept an initialization map. Existing account writes
// below are a fixed small whitelist, never a caller-provided storage program.
type mainnetDAOBundle struct {
	Version                      uint32              `json:"version"`
	ChainID                      uint64              `json:"chainId"`
	LegacyGenesisTime            uint64              `json:"legacyGenesisTime"`
	GenesisHash                  common.Hash         `json:"genesisHash"`
	GenesisStateRoot             common.Hash         `json:"genesisStateRoot"`
	BaselineNumber               uint64              `json:"baselineNumber"`
	BaselineHash                 common.Hash         `json:"baselineHash"`
	BaselineStateRoot            common.Hash         `json:"baselineStateRoot"`
	LegacyProxyCodeHash          common.Hash         `json:"legacyProxyCodeHash"`
	LegacyImplementation         common.Address      `json:"legacyImplementation"`
	LegacyImplementationCodeHash common.Hash         `json:"legacyImplementationCodeHash"`
	LegacyOwner                  common.Address      `json:"legacyOwner"`
	BlocklistCodeHash            common.Hash         `json:"blocklistCodeHash"`
	BlocklistOwner               common.Address      `json:"blocklistOwner"`
	SelectiveCodeHash            common.Hash         `json:"selectiveCodeHash"`
	SelectiveOwner               common.Address      `json:"selectiveOwner"`
	GovernanceProxyCodeHash      common.Hash         `json:"governanceProxyCodeHash"`
	GovernanceProxyRuntime       hexutil.Bytes       `json:"governanceProxyRuntime"`
	Accounts                     []mainnetDAOAccount `json:"accounts"`
}

func loadMainnetDAOBundle(raw []byte) (*mainnetDAOBundle, map[string]mainnetDAOAccount, error) {
	var b *mainnetDAOBundle
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return nil, nil, err
	}
	if err := d.Decode(new(interface{})); err != io.EOF {
		return nil, nil, fmt.Errorf("Mainnet artifact requires exactly one object")
	}
	if b == nil || b.Version != 1 || b.ChainID != 1404 || b.LegacyGenesisTime == 0 || b.BaselineNumber == 0 ||
		len(b.Accounts) != len(mainnetDAORoles) || len(b.GovernanceProxyRuntime) == 0 || len(b.GovernanceProxyRuntime) > 24576 {
		return nil, nil, fmt.Errorf("unsealed or invalid Mainnet migration artifact")
	}
	for _, hash := range []common.Hash{b.GenesisHash, b.GenesisStateRoot, b.BaselineHash, b.BaselineStateRoot, b.LegacyProxyCodeHash,
		b.LegacyImplementationCodeHash, b.BlocklistCodeHash, b.SelectiveCodeHash, b.GovernanceProxyCodeHash} {
		if hash == (common.Hash{}) {
			return nil, nil, fmt.Errorf("missing Mainnet pre-state identity")
		}
	}
	for _, address := range []common.Address{b.LegacyOwner, b.BlocklistOwner, b.SelectiveOwner, b.LegacyImplementation} {
		if address == (common.Address{}) {
			return nil, nil, fmt.Errorf("missing retired authority or implementation")
		}
	}
	roles := make(map[string]mainnetDAOAccount, len(b.Accounts))
	seen := make(map[common.Address]bool, len(b.Accounts))
	allowed := make(map[string]bool, len(mainnetDAORoles))
	for _, role := range mainnetDAORoles {
		allowed[role] = true
	}
	for _, a := range b.Accounts {
		if !allowed[a.Role] || a.Address == (common.Address{}) || seen[a.Address] || len(a.Code) == 0 || len(a.Code) > 24576 ||
			crypto.Keccak256Hash(a.Code) != a.CodeHash || (a.Role == "factory" && a.Nonce != 2) || (a.Role != "factory" && a.Nonce != 1) {
			return nil, nil, fmt.Errorf("invalid Mainnet account %s", a.Role)
		}
		if _, ok := roles[a.Role]; ok {
			return nil, nil, fmt.Errorf("duplicate Mainnet role %s", a.Role)
		}
		if (a.Role == "reader" && (a.Address != mparams.ChainTallyAddr || len(a.Storage) != 0)) ||
			(a.Role != "reader" && a.Address == mparams.ChainTallyAddr) ||
			(a.Role == "stakingV2" && (a.Address != mparams.SeparateStakingV2Addr || len(a.Storage) != 0)) ||
			(a.Role != "stakingV2" && a.Address == mparams.SeparateStakingV2Addr) {
			return nil, nil, fmt.Errorf("invalid native staking/reader binding")
		}
		for _, old := range []common.Address{mparams.StakingContractAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, governanceProxyAddr, b.LegacyImplementation, b.LegacyOwner, b.BlocklistOwner, b.SelectiveOwner} {
			if a.Address == old {
				return nil, nil, fmt.Errorf("Mainnet role overlaps legacy target/authority")
			}
		}
		roles[a.Role] = a
		seen[a.Address] = true
	}
	if roles["escrow"].Address != crypto.CreateAddress(roles["factory"].Address, 1) {
		return nil, nil, fmt.Errorf("Mainnet constructor-created escrow address mismatch")
	}
	return b, roles, nil
}

func mainnetDAOCommitment(chain ethconsensus.ChainHeaderReader) common.Hash {
	raw, _ := json.Marshal(chain.Config().BdagMainnetDAO)
	return crypto.Keccak256Hash(raw)
}

// Fail at process configuration time, before opening live databases, if a
// release profile and its compiled artifact do not form a complete sealed pair.
func ValidateCompiledMainnetDAOArtifacts() error {
	g := mparams.BdagMainnetDAO
	if g == nil {
		return nil
	}
	if err := g.Validate(big.NewInt(1404)); err != nil {
		return err
	}
	if common.Hash(sha256.Sum256(mainnetDAOArtifactJSON)) != g.ArtifactSHA256 {
		return fmt.Errorf("compiled Mainnet artifact/profile digest mismatch")
	}
	b, _, err := loadMainnetDAOBundle(mainnetDAOArtifactJSON)
	if err != nil {
		return err
	}
	if b.LegacyGenesisTime != g.LegacyGenesisTime || b.BaselineNumber >= g.EarliestEVMBlock {
		return fmt.Errorf("compiled Mainnet artifact/profile pre-state mismatch")
	}
	return nil
}

// ApplyMainnetDAOFork is used by the shared build/import pre-transaction hook.
// It validates a compiled manifest, canonical history, persisted native result
// and complete destination set before performing any state write.
func ApplyMainnetDAOFork(chain ethconsensus.ChainHeaderReader, h *types.Header, s *state.StateDB) error {
	return applyMainnetDAOFork(chain, h, s, mainnetDAOArtifactJSON)
}

func applyMainnetDAOFork(chain ethconsensus.ChainHeaderReader, h *types.Header, s *state.StateDB, raw []byte) error {
	if chain == nil || chain.Config() == nil || chain.Config().BdagMainnetDAO == nil {
		if s != nil && s.GetState(mparams.ChainTallyAddr, mainnetDAOAppliedSlot) != (common.Hash{}) {
			return fmt.Errorf("persisted Mainnet DAO state requires its immutable profile")
		}
		return nil
	}
	c := chain.Config()
	g := c.BdagMainnetDAO
	if err := g.Validate(c.ChainID); err != nil {
		return err
	}
	if qparams.ActiveNetParams == nil || qparams.ActiveNetParams.Net != qparams.MainNetParams.Net ||
		c.BdagGovernanceFork != nil || c.BdagGovernanceForkBlock != nil || c.BdagTestnetTiming != nil || c.BdagTestnetTimingBlock != nil ||
		c.BdagSeparateStaking != nil || c.BdagSeparateStakingBlock != nil {
		return fmt.Errorf("Mainnet DAO cannot use a rehearsal network or predecessor")
	}
	if s == nil || h == nil || h.Number == nil || !h.Number.IsUint64() || h.Number.Sign() <= 0 {
		return fmt.Errorf("Mainnet DAO requires bounded block identity and state")
	}
	commitment := mainnetDAOCommitment(chain)
	marker := s.GetState(mparams.ChainTallyAddr, mainnetDAOAppliedSlot)
	if marker != (common.Hash{}) {
		if !marker.Big().IsUint64() || marker.Big().Cmp(h.Number) > 0 || marker.Big().Uint64() < g.EarliestEVMBlock ||
			s.GetState(mparams.ChainTallyAddr, mainnetDAOCommitmentSlot) != commitment ||
			s.GetState(mparams.ChainTallyAddr, separateStakingActiveAddressSlot) != common.BytesToHash(mparams.SeparateStakingV2Addr.Bytes()) ||
			s.GetState(mparams.ChainTallyAddr, separateStakingCutoverEpochPlusSlot) == (common.Hash{}) ||
			common.Hash(sha256.Sum256(raw)) != g.ArtifactSHA256 {
			return fmt.Errorf("persisted Mainnet migration marker/profile mismatch")
		}
		return nil
	}
	if h.Number.Uint64() < g.EarliestEVMBlock {
		return nil
	}
	if common.Hash(sha256.Sum256(raw)) != g.ArtifactSHA256 {
		return fmt.Errorf("Mainnet artifact digest mismatch")
	}
	b, roles, err := loadMainnetDAOBundle(raw)
	if err != nil {
		return err
	}
	if b.LegacyGenesisTime != g.LegacyGenesisTime || b.BaselineNumber >= g.EarliestEVMBlock {
		return fmt.Errorf("Mainnet clock/checkpoint profile mismatch")
	}
	for _, pin := range []struct {
		number     uint64
		hash, root common.Hash
	}{{0, b.GenesisHash, b.GenesisStateRoot}, {b.BaselineNumber, b.BaselineHash, b.BaselineStateRoot}} {
		got := chain.GetHeaderByNumber(pin.number)
		if got == nil || got.Hash() != pin.hash || got.Root != pin.root {
			return fmt.Errorf("Mainnet canonical history mismatch at %d", pin.number)
		}
	}
	d := upgrades.GetUpgrades()[upgrades.MainnetDAOStakingV2Name]
	if d == nil {
		return fmt.Errorf("Mainnet native definition missing")
	}
	if d.Name != upgrades.MainnetDAOStakingV2Name || d.BitMask != upgrades.MainnetDAOStakingV2BitMask || d.StartLayer != g.SignalStartLayer ||
		d.WindowSize != 100000 || d.Threshold != 75000 || d.ActivationDelayBlocks != g.ActivationDelayLayers || d.TimeoutLayer != 0 {
		return fmt.Errorf("Mainnet native definition differs from release policy")
	}
	if !d.LockedIn && !d.AnchorSet && !d.Active && d.ActivateAnchor == 0 {
		return nil
	}
	minimum := g.SignalStartLayer + 99999 + g.ActivationDelayLayers
	if !d.LockedIn || !d.AnchorSet || d.ActivateAnchor < minimum || (d.ActivateAnchor-minimum)%100000 != 0 {
		return fmt.Errorf("invalid persisted Mainnet native anchor")
	}
	// Pre-anchor headers retain the historical reward-only encoding.
	if len(h.Extra) == RewardExtraLength {
		return nil
	}
	_, layer, err := DecodeRewardExtraWithLayer(h.Extra)
	if err != nil {
		return err
	}
	if layer < d.ActivateAnchor || h.Time < g.NotBeforeTimestamp {
		return nil
	}
	if !upgrades.IsEnforcedAtLayer(d.Name, layer) {
		return fmt.Errorf("Mainnet native result is not enforced")
	}
	parent := chain.GetHeaderByHash(h.ParentHash)
	if parent == nil || parent.Number == nil || !parent.Number.IsUint64() || parent.Number.Uint64()+1 != h.Number.Uint64() || parent.Time < g.LegacyGenesisTime {
		return fmt.Errorf("Mainnet migration requires the canonical parent")
	}
	if h.Time < g.LegacyGenesisTime {
		return fmt.Errorf("Mainnet header predates original staking clock")
	}
	epoch := (h.Time - g.LegacyGenesisTime) / 604800
	parentEpoch := (parent.Time - g.LegacyGenesisTime) / 604800
	if epoch <= parentEpoch || g.LegacyGenesisTime+epoch*604800 < g.NotBeforeTimestamp {
		return nil
	}
	if epoch == math.MaxUint64 {
		return fmt.Errorf("Mainnet epoch overflow")
	}
	for _, pin := range []struct {
		address common.Address
		hash    common.Hash
	}{{mparams.StakingContractAddr, b.LegacyProxyCodeHash},
		{b.LegacyImplementation, b.LegacyImplementationCodeHash}, {mparams.BlocklistContractAddr, b.BlocklistCodeHash},
		{mparams.SelectiveBlocklistContractAddr, b.SelectiveCodeHash}, {governanceProxyAddr, b.GovernanceProxyCodeHash}} {
		if s.GetCodeHash(pin.address) != pin.hash {
			return fmt.Errorf("unexpected Mainnet legacy code at %s", pin.address)
		}
	}
	for _, pin := range []struct {
		address common.Address
		owner   common.Address
	}{{mparams.StakingContractAddr, b.LegacyOwner},
		{mparams.BlocklistContractAddr, b.BlocklistOwner}, {mparams.SelectiveBlocklistContractAddr, b.SelectiveOwner}} {
		if s.GetState(pin.address, common.Hash{}) != common.BytesToHash(pin.owner.Bytes()) {
			return fmt.Errorf("unexpected Mainnet legacy owner")
		}
	}
	if s.GetState(mparams.StakingContractAddr, erc1967ImplementationSlot) != common.BytesToHash(b.LegacyImplementation.Bytes()) ||
		s.GetState(mparams.StakingContractAddr, mparams.SlotGenesisTime) != common.BigToHash(new(big.Int).SetUint64(g.LegacyGenesisTime)) ||
		s.GetState(mparams.StakingContractAddr, mparams.SlotMinerSplit) != common.BigToHash(big.NewInt(70)) ||
		s.GetState(mparams.StakingContractAddr, mparams.SlotStakerSplit) != common.BigToHash(big.NewInt(30)) {
		return fmt.Errorf("Mainnet V1 implementation/clock/subsidy pre-state mismatch")
	}
	for _, slot := range []common.Hash{mainnetDAOCommitmentSlot, governanceAppliedSlot, testnetTimingAppliedSlot, separateStakingAppliedSlot,
		separateStakingActiveAddressSlot, separateStakingCutoverEpochPlusSlot, mparams.SlotRewardVotingEpoch, mparams.SlotRewardVotingBootstrapEpochPlus,
		mparams.SlotRewardVotingFirstEpochPlus, mparams.SlotRewardEpochScheduleLength, mparams.SlotGovernanceTimeFloor} {
		if s.GetState(mparams.ChainTallyAddr, slot) != (common.Hash{}) {
			return fmt.Errorf("partial or conflicting Mainnet governance pre-state")
		}
	}
	// Inspect a copy so a storage collision also fails when an earlier hook has
	// left uncommitted storage. Never finalize or clear the caller's live journal.
	inspection := s.Copy()
	inspection.IntermediateRoot(false)
	if err := inspection.Error(); err != nil {
		return fmt.Errorf("Mainnet pre-state read failed: %w", err)
	}
	for _, a := range b.Accounts {
		if a.Role == "reader" {
			if len(s.GetCode(a.Address)) != 0 {
				return fmt.Errorf("occupied Mainnet reader")
			}
			continue
		}
		// Preserve unsolicited balances; reject code, nonce or storage collisions.
		root := inspection.GetStorageRoot(a.Address)
		if len(s.GetCode(a.Address)) != 0 || s.GetNonce(a.Address) != 0 || (root != (common.Hash{}) && root != types.EmptyRootHash) {
			return fmt.Errorf("occupied Mainnet destination %s", a.Role)
		}
	}
	for i := int64(0); i < 3; i++ {
		if s.GetState(mparams.ChainTallyAddr, rewardScheduleWordSlot(i)) != (common.Hash{}) {
			return fmt.Errorf("occupied Mainnet clock segment")
		}
	}
	if err := s.Error(); err != nil {
		return fmt.Errorf("Mainnet migration pre-state unavailable: %w", err)
	}

	// No fallible operations below. Existing balances, nonces, financial slots,
	// legacy tallies and every restriction mapping are deliberately not written.
	for _, a := range b.Accounts {
		s.SetCode(a.Address, a.Code)
		if a.Role != "reader" || s.GetNonce(a.Address) == 0 {
			s.SetNonce(a.Address, a.Nonce)
		}
		for slot, value := range a.Storage {
			s.SetState(a.Address, slot, value)
		}
	}
	word := func(n uint64) common.Hash { return common.BigToHash(new(big.Int).SetUint64(n)) }
	addressWord := func(role string) common.Hash { return common.BytesToHash(roles[role].Address.Bytes()) }
	for _, role := range []string{"legacyImplementation", "stakingV2Implementation"} {
		s.SetState(roles[role].Address, mparams.SlotInitializableStorage, initializableDisabled)
	}
	s.SetState(mparams.StakingContractAddr, erc1967ImplementationSlot, addressWord("legacyImplementation"))
	s.SetState(mparams.StakingContractAddr, common.Hash{}, addressWord("stakingTimelock"))
	for _, a := range []common.Address{mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, governanceProxyAddr} {
		s.SetState(a, common.Hash{}, addressWord("miningTimelock"))
	}
	s.SetCode(governanceProxyAddr, b.GovernanceProxyRuntime)
	s.SetState(governanceProxyAddr, erc1967ImplementationSlot, addressWord("hub"))
	s.SetState(governanceProxyAddr, erc1967AdminSlot, addressWord("miningTimelock"))
	for slot, value := range map[common.Hash]common.Hash{common.Hash{}: addressWord("stakingTimelock"), erc1967ImplementationSlot: addressWord("stakingV2Implementation"),
		mparams.SlotGenesisTime: word(g.LegacyGenesisTime), mparams.SlotMinerSplit: word(70), mparams.SlotStakerSplit: word(30),
		mparams.SlotInitializableStorage: word(2), mparams.SlotReentrancyGuardState: word(1), common.BigToHash(big.NewInt(18)): word(h.Time), common.BigToHash(big.NewInt(32)): word(256)} {
		s.SetState(mparams.SeparateStakingV2Addr, slot, value)
	}
	for slot, value := range map[common.Hash]common.Hash{mparams.SlotRewardVotingEpoch: word(epoch), mparams.SlotRewardVotingBootstrapEpochPlus: word(epoch + 1),
		mparams.SlotRewardEpochScheduleLength: word(1), mparams.SlotGovernanceTimeFloor: word(h.Time), separateStakingActiveAddressSlot: addressWord("stakingV2"),
		separateStakingCutoverEpochPlusSlot: word(epoch + 1), rewardScheduleWordSlot(0): word(0), rewardScheduleWordSlot(1): word(g.LegacyGenesisTime), rewardScheduleWordSlot(2): word(604800),
		mainnetDAOCommitmentSlot: commitment, mainnetDAOAppliedSlot: common.BigToHash(h.Number)} {
		s.SetState(mparams.ChainTallyAddr, slot, value)
	}
	return nil
}
