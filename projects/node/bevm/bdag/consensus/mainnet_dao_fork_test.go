package consensus

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"testing"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	cu "github.com/BlockdagNetworkLabs/bdag/consensus/upgrades"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	ethconsensus "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

type mainnetDAOTestChain struct {
	ethconsensus.ChainHeaderReader
	c                         *ethparams.ChainConfig
	parent, baseline, genesis *types.Header
}

func (c *mainnetDAOTestChain) Config() *ethparams.ChainConfig { return c.c }
func (c *mainnetDAOTestChain) GetHeaderByHash(hash common.Hash) *types.Header {
	if c.parent.Hash() == hash {
		return c.parent
	}
	return nil
}
func (c *mainnetDAOTestChain) GetHeaderByNumber(n uint64) *types.Header {
	if n == 0 {
		return c.genesis
	}
	if c.baseline.Number.Uint64() == n {
		return c.baseline
	}
	return nil
}

// This deliberately tiny program tests the native write envelope independently
// of runtime semantics. The exported real-runtime fixture is exercised separately.
func mainnetDAOTestFixture(t *testing.T) (*mainnetDAOTestChain, *types.Header, *state.StateDB, []byte) {
	t.Helper()
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	t.Cleanup(func() { qparams.ActiveNetParams = previousNet })
	s, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	require.NoError(t, err)
	c := *mparams.BdagMainnetChainConfig
	g := &ethparams.BDAGMainnetDAOConfig{Version: 1, SignalStartLayer: 30_000_000, ActivationDelayLayers: 2000, EarliestEVMBlock: 10,
		ReleaseDistributedAt: 1800000000, NoticePublishedAt: 1803024000, NotBeforeTimestamp: 1803628800, LegacyGenesisTime: 1770760980}
	c.BdagMainnetDAO = g
	code := []byte{0x60, 0, 0x00}
	codeHash := crypto.Keccak256Hash(code)
	owner := common.HexToAddress("0xaaaa")
	oldImpl := common.HexToAddress("0xbbbb")
	for _, address := range []common.Address{mparams.StakingContractAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, governanceProxyAddr, oldImpl} {
		s.SetCode(address, code)
		s.SetState(address, common.Hash{}, common.BytesToHash(owner.Bytes()))
	}
	s.SetState(mparams.StakingContractAddr, erc1967ImplementationSlot, common.BytesToHash(oldImpl.Bytes()))
	s.SetState(mparams.StakingContractAddr, mparams.SlotGenesisTime, common.BigToHash(new(big.Int).SetUint64(g.LegacyGenesisTime)))
	s.SetState(mparams.StakingContractAddr, mparams.SlotMinerSplit, common.BigToHash(big.NewInt(70)))
	s.SetState(mparams.StakingContractAddr, mparams.SlotStakerSplit, common.BigToHash(big.NewInt(30)))
	s.SetBalance(mparams.StakingContractAddr, uint256.NewInt(987654321), tracing.BalanceChangeUnspecified)
	genesis := &types.Header{Number: big.NewInt(0), Root: common.HexToHash("0x1404")}
	baseline := &types.Header{Number: big.NewInt(5), Root: common.HexToHash("0x1234")}
	b := mainnetDAOBundle{Version: 1, ChainID: 1404, LegacyGenesisTime: g.LegacyGenesisTime,
		GenesisHash: genesis.Hash(), GenesisStateRoot: genesis.Root, BaselineNumber: 5, BaselineHash: baseline.Hash(), BaselineStateRoot: baseline.Root,
		LegacyProxyCodeHash: codeHash, LegacyImplementation: oldImpl, LegacyImplementationCodeHash: codeHash, LegacyOwner: owner,
		BlocklistCodeHash: codeHash, BlocklistOwner: owner, SelectiveCodeHash: codeHash, SelectiveOwner: owner,
		GovernanceProxyCodeHash: codeHash, GovernanceProxyRuntime: code}
	for i, role := range mainnetDAORoles {
		address := common.BigToAddress(big.NewInt(int64(1000 + i)))
		if role == "reader" {
			address = mparams.ChainTallyAddr
		}
		if role == "stakingV2" {
			address = mparams.SeparateStakingV2Addr
		}
		nonce := uint64(1)
		if role == "factory" {
			nonce = 2
		}
		b.Accounts = append(b.Accounts, mainnetDAOAccount{Role: role, Address: address, Code: code, CodeHash: codeHash, Nonce: nonce})
	}
	for i := range b.Accounts {
		if b.Accounts[i].Role == "escrow" {
			for _, a := range b.Accounts {
				if a.Role == "factory" {
					b.Accounts[i].Address = crypto.CreateAddress(a.Address, 1)
				}
			}
		}
	}
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	g.ArtifactSHA256 = common.Hash(sha256.Sum256(raw))
	time := g.LegacyGenesisTime + ((g.NotBeforeTimestamp-g.LegacyGenesisTime)/604800+1)*604800
	parent := &types.Header{Number: big.NewInt(9), Time: time - 1}
	h := &types.Header{Number: big.NewInt(10), Time: time, ParentHash: parent.Hash(), Extra: EncodeRewardExtraWithLayer(1, g.SignalStartLayer+99999+g.ActivationDelayLayers)}
	d, err := cu.MainnetDAOStakingV2Definition(g.SignalStartLayer, g.ActivationDelayLayers)
	require.NoError(t, err)
	d.LockedIn = true
	d.AnchorSet = true
	d.ActivateAnchor = g.SignalStartLayer + 99999 + g.ActivationDelayLayers
	previous := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{d.Name: d})
	t.Cleanup(func() { upgrades.ReplaceUpgrades(previous) })
	return &mainnetDAOTestChain{c: &c, parent: parent, baseline: baseline, genesis: genesis}, h, s, raw
}

func mutateMainnetDAODefinition(change func(*upgrades.UpgradeDefinition)) {
	defs := upgrades.GetUpgrades()
	change(defs[upgrades.MainnetDAOStakingV2Name])
	upgrades.ReplaceUpgrades(defs)
}

func TestMainnetDAOAtomicEnvelopePreservesEveryUnrelatedLegacyAndRestrictionSlot(t *testing.T) {
	c, h, s, raw := mainnetDAOTestFixture(t)
	slots := make(map[common.Address]map[common.Hash]common.Hash)
	for _, a := range []common.Address{mparams.StakingContractAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, mparams.ChainTallyAddr} {
		slots[a] = make(map[common.Hash]common.Hash)
		for i := 0; i < 256; i++ {
			k := crypto.Keccak256Hash([]byte{byte(i), a[19]})
			v := crypto.Keccak256Hash(k.Bytes())
			s.SetState(a, k, v)
			slots[a][k] = v
		}
	}
	old := s.Copy()
	require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
	for a, rows := range slots {
		for k, v := range rows {
			require.Equal(t, v, s.GetState(a, k), "unknown storage %s/%s", a, k)
		}
	}
	require.Equal(t, old.GetBalance(mparams.StakingContractAddr), s.GetBalance(mparams.StakingContractAddr))
	require.Equal(t, old.GetState(mparams.StakingContractAddr, mparams.SlotGenesisTime), s.GetState(mparams.StakingContractAddr, mparams.SlotGenesisTime))
	require.Equal(t, mparams.SeparateStakingV2Addr, activeStakingAddress(h, s))
	require.Zero(t, s.GetBalance(mparams.SeparateStakingV2Addr).Sign())
	require.Equal(t, common.BigToHash(h.Number), s.GetState(mparams.ChainTallyAddr, mainnetDAOAppliedSlot))
	require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
	replay := old.Copy()
	require.NoError(t, applyMainnetDAOFork(c, h, replay, raw))
	require.Equal(t, s.IntermediateRoot(false), replay.IntermediateRoot(false))
	changed := *c.c.BdagMainnetDAO
	changed.NotBeforeTimestamp++
	c.c.BdagMainnetDAO = &changed
	require.Error(t, applyMainnetDAOFork(c, h, s, raw))
}

func TestMainnetDAORefusesUnsafeInputsBeforeAnyStateWrite(t *testing.T) {
	for _, name := range []string{"artifact", "native threshold", "anchor", "wrong fork", "legacy owner", "legacy code", "original implementation", "clock", "occupied target", "occupied storage", "partial marker", "missing parent", "notice", "evm floor", "old profile"} {
		t.Run(name, func(t *testing.T) {
			c, h, s, raw := mainnetDAOTestFixture(t)
			switch name {
			case "artifact":
				raw = append(raw, ' ')
			case "native threshold":
				mutateMainnetDAODefinition(func(d *upgrades.UpgradeDefinition) { d.Threshold = 1500 })
			case "anchor":
				mutateMainnetDAODefinition(func(d *upgrades.UpgradeDefinition) { d.ActivateAnchor++ })
			case "wrong fork":
				c.baseline.Root = common.HexToHash("0xdead")
			case "legacy owner":
				s.SetState(mparams.StakingContractAddr, common.Hash{}, common.Hash{})
			case "legacy code":
				s.SetCode(mparams.StakingContractAddr, []byte{0x01})
			case "original implementation":
				s.SetState(mparams.StakingContractAddr, erc1967ImplementationSlot, common.Hash{})
			case "clock":
				s.SetState(mparams.StakingContractAddr, mparams.SlotGenesisTime, common.Hash{})
			case "occupied target":
				s.SetNonce(mparams.SeparateStakingV2Addr, 1)
			case "occupied storage":
				s.SetBalance(mparams.SeparateStakingV2Addr, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
				s.SetState(mparams.SeparateStakingV2Addr, common.HexToHash("0x777"), common.HexToHash("0x1"))
			case "partial marker":
				s.SetState(mparams.ChainTallyAddr, separateStakingActiveAddressSlot, common.HexToHash("0x01"))
			case "missing parent":
				h.ParentHash = common.Hash{}
			case "notice":
				c.c.BdagMainnetDAO.NotBeforeTimestamp = c.c.BdagMainnetDAO.NoticePublishedAt + 86400
			case "evm floor":
				c.c.BdagMainnetDAO.EarliestEVMBlock = 0
			case "old profile":
				c.c.BdagMainnetDAO = nil
				s.SetState(mparams.ChainTallyAddr, mainnetDAOAppliedSlot, common.BigToHash(h.Number))
			}
			before := s.Copy().IntermediateRoot(false)
			require.Error(t, applyMainnetDAOFork(c, h, s, raw))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}

func TestMainnetDAOWaitsForSignalNoticeAndFreshEpoch(t *testing.T) {
	for _, name := range []string{"below EVM floor", "not locked", "before native anchor", "before notice", "same epoch"} {
		t.Run(name, func(t *testing.T) {
			c, h, s, raw := mainnetDAOTestFixture(t)
			switch name {
			case "below EVM floor":
				c.c.BdagMainnetDAO.EarliestEVMBlock = 11
			case "not locked":
				mutateMainnetDAODefinition(func(d *upgrades.UpgradeDefinition) { d.LockedIn = false; d.AnchorSet = false; d.ActivateAnchor = 0 })
			case "before native anchor":
				h.Extra = EncodeRewardExtraWithLayer(1, c.c.BdagMainnetDAO.SignalStartLayer)
			case "before notice":
				h.Time = c.c.BdagMainnetDAO.NotBeforeTimestamp - 1
			case "same epoch":
				h.Time += 1
				c.parent.Time = h.Time
				h.ParentHash = c.parent.Hash()
			}
			before := s.Copy().IntermediateRoot(false)
			require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}

func TestMainnetDAOEpochClockAndVotingDoNotReinterpretLegacyRewards(t *testing.T) {
	c, h, s, raw := mainnetDAOTestFixture(t)
	require.False(t, rewardVotingEnabled(c, h, s))
	require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
	require.True(t, rewardVotingEnabled(c, h, s))
	epoch := (h.Time - c.c.BdagMainnetDAO.LegacyGenesisTime) / 604800
	require.False(t, rewardVotingEpochFinalized(s, epoch-1))
	oldRoot := s.Copy().IntermediateRoot(false)
	wrong := *c.c
	wrong.BdagMainnetDAO = nil
	require.Error(t, prepareVersionedRewardClock(&wrong, h, s))
	require.Equal(t, oldRoot, s.Copy().IntermediateRoot(false))
	// A timestamp regression cannot reopen the bootstrap epoch.
	later := *h
	later.Number = big.NewInt(11)
	later.Time += 604800
	require.NoError(t, prepareVersionedRewardClock(c.c, &later, s))
	require.Equal(t, epoch+1, rewardVotingEpochForHeader(&later, s))
	require.False(t, rewardVotingEpochFinalized(s, epoch))
	later.Number = big.NewInt(12)
	later.Time += 604800
	require.NoError(t, prepareVersionedRewardClock(c.c, &later, s))
	require.True(t, rewardVotingEpochFinalized(s, epoch+1))
	later.Time = h.Time
	require.NoError(t, prepareVersionedRewardClock(c.c, &later, s))
	require.Equal(t, epoch+2, rewardVotingEpochForHeader(&later, s))
	require.Equal(t, mparams.StakingContractAddr, stakingAddressForRewardEpoch(s, epoch-1))
	require.Equal(t, mparams.SeparateStakingV2Addr, stakingAddressForRewardEpoch(s, epoch))
}

func TestMainnetDAONativeFirstCreditPreservesLegacyAndExistingDates(t *testing.T) {
	c, h, s, raw := mainnetDAOTestFixture(t)
	require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
	activation := h.Time
	h.Coinbase = common.HexToAddress("0xabcd")
	slot := calculateMappingSlot(h.Coinbase, mparams.SlotUserFirstDepositTimestampBase)
	db := rawdb.NewMemoryDatabase()
	t.Cleanup(func() { db.Close() })
	engine := New(db)
	h.Time = activation - 60
	engine.updateAutoStake(s, h, big.NewInt(100))
	require.Equal(t, activation, s.GetState(mparams.SeparateStakingV2Addr, slot).Big().Uint64())
	require.Equal(t, common.Hash{}, s.GetState(mparams.StakingContractAddr, slot))
	h.Time = activation + 604800
	engine.updateAutoStake(s, h, big.NewInt(100))
	require.Equal(t, activation, s.GetState(mparams.SeparateStakingV2Addr, slot).Big().Uint64())
}
