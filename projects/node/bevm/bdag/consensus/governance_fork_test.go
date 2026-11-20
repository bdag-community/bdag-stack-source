package consensus

import (
	"crypto/sha256"
	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	ethconsensus "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

func testnetBootstrapFixture(t *testing.T) (*ethparams.ChainConfig, *types.Header, *state.StateDB) {
	t.Helper()
	db, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	require.NoError(t, err)
	c := *mparams.BdagTestnetChainConfig
	c.BdagGovernanceForkBlock = big.NewInt(1)
	c.BdagGovernanceFork = mparams.TestnetGovernanceArtifactTemplate()
	c.BdagGovernanceFork.SignalWindowStartLayer = 1_000
	c.BdagGovernanceFork.ActivationLayer = 4_999
	c.BdagGovernanceFork.InitialReleaseDistributedAt = 100
	c.BdagGovernanceFork.NoticePublishedAt = 3_024_100
	c.BdagGovernanceFork.NotBeforeTimestamp = 3_628_900
	h := &types.Header{
		Number:     big.NewInt(1),
		Time:       c.BdagGovernanceFork.NotBeforeTimestamp,
		GasLimit:   45_000_000,
		Difficulty: big.NewInt(1),
		BaseFee:    big.NewInt(0),
		Extra:      EncodeRewardExtraWithLayer(1, c.BdagGovernanceFork.ActivationLayer),
	}
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		upgrades.RewardGovernanceV3Name: {
			Name: upgrades.RewardGovernanceV3Name, BitMask: upgrades.RewardGovernanceV3BitMask,
			Threshold: 1_500, WindowSize: 2_000, LockedIn: true, AnchorSet: true,
			ActivateAnchor: c.BdagGovernanceFork.ActivationLayer,
		},
	})
	t.Cleanup(func() { upgrades.ReplaceUpgrades(oldUpgrades) })
	return &c, h, db
}

func TestTestnetGovernanceBootstrapAllocationHashIsPinned(t *testing.T) {
	got := sha256.Sum256(testnetGovernanceBootstrapJSON)
	require.Equal(t, testnetGovernanceBootstrapSHA256, got)
	require.Len(t, testnetGovernanceBootstrapAccounts, 19)
}

func TestTestnetGovernanceBootstrapAppliesToEmptyState(t *testing.T) {
	c, h, s := testnetBootstrapFixture(t)
	require.NoError(t, ApplyGovernanceFork(c, h, s))
	for _, account := range testnetGovernanceBootstrapAccounts {
		require.Equal(t, account.code, s.GetCode(account.address), "code %s", account.address)
		require.Equal(t, account.nonce, s.GetNonce(account.address), "nonce %s", account.address)
		for _, slot := range account.storage {
			require.Equal(t, slot.value, s.GetState(account.address, slot.key), "storage %s %s", account.address, slot.key)
		}
	}
	require.Equal(t, common.BigToHash(h.Number), s.GetState(mparams.ChainTallyAddr, governanceAppliedSlot))
	require.Equal(t, c.BdagGovernanceFork.StakingTimelockCodeHash, s.GetCodeHash(c.BdagGovernanceFork.StakingTimelock))
	require.Equal(t, c.BdagGovernanceFork.MiningTimelockCodeHash, s.GetCodeHash(c.BdagGovernanceFork.MiningTimelock))
	require.Equal(t, c.BdagGovernanceFork.GovernorCodeHash, s.GetCodeHash(c.BdagGovernanceFork.Governor))
}

func TestTestnetGovernanceBootstrapPreservesProtocolStakingPrestate(t *testing.T) {
	c, h, s := testnetBootstrapFixture(t)
	wantBalance := uint256.NewInt(213_486)
	pool := common.HexToAddress("0xA6ea67E189868FcEd08E1cBc99A821CAe0FfFe72")
	poolAssetsSlot := calculateMappingSlot(pool, mparams.SlotPoolTotalAssetsBase)
	wantPoolAssets := common.BigToHash(big.NewInt(71_162))
	s.SetBalance(mparams.StakingContractAddr, wantBalance, tracing.BalanceIncreaseRewardMineBlock)
	s.SetState(mparams.StakingContractAddr, poolAssetsSlot, wantPoolAssets)

	require.NoError(t, ApplyGovernanceFork(c, h, s))
	require.Equal(t, wantBalance, s.GetBalance(mparams.StakingContractAddr))
	require.Equal(t, wantPoolAssets, s.GetState(mparams.StakingContractAddr, poolAssetsSlot))
	require.Equal(t, testnetBootstrapAccountCode(t, mparams.StakingContractAddr), s.GetCode(mparams.StakingContractAddr))
}

func TestTestnetGovernanceBootstrapPreservesHistoricalChainTallyPrestate(t *testing.T) {
	c, h, s := testnetBootstrapFixture(t)
	miner := common.HexToAddress("0xA6ea67E189868FcEd08E1cBc99A821CAe0FfFe72")
	currentBase := crypto.Keccak256Hash(nil)
	tallySlot := calculateMappingSlot(miner, currentBase)
	wantTally := common.BigToHash(big.NewInt(71_162))
	s.SetNonce(mparams.ChainTallyAddr, 1)
	s.SetState(mparams.ChainTallyAddr, mparams.SlotCurrentTallyBase, currentBase)
	s.SetState(mparams.ChainTallyAddr, tallySlot, wantTally)

	require.NoError(t, ApplyGovernanceFork(c, h, s))
	require.Equal(t, uint64(1), s.GetNonce(mparams.ChainTallyAddr))
	require.Equal(t, currentBase, s.GetState(mparams.ChainTallyAddr, mparams.SlotCurrentTallyBase))
	require.Equal(t, wantTally, s.GetState(mparams.ChainTallyAddr, tallySlot))
	require.Equal(t, testnetBootstrapAccountCode(t, mparams.ChainTallyAddr), s.GetCode(mparams.ChainTallyAddr))
}

func testnetBootstrapAccountCode(t *testing.T, address common.Address) []byte {
	t.Helper()
	account, ok := testnetBootstrapAccountAt(address)
	require.True(t, ok)
	return account.code
}

func TestTestnetGovernanceBootstrapRejectsMixedStateAtomically(t *testing.T) {
	for _, name := range []string{"balance", "code", "nonce", "storage"} {
		t.Run(name, func(t *testing.T) {
			c, h, s := testnetBootstrapFixture(t)
			account := testnetGovernanceBootstrapAccounts[0]
			switch name {
			case "balance":
				s.SetBalance(account.address, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
			case "code":
				s.SetCode(account.address, []byte{0xfe})
			case "nonce":
				s.SetNonce(account.address, 7)
			case "storage":
				for _, candidate := range testnetGovernanceBootstrapAccounts {
					if len(candidate.storage) != 0 {
						account = candidate
						break
					}
				}
				s.SetState(account.address, account.storage[0].key, common.HexToHash("0x01"))
			}
			before := s.IntermediateRoot(false)
			err := ApplyGovernanceFork(c, h, s)
			require.ErrorContains(t, err, "bootstrap target is not empty")
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}

func TestTestnetGovernanceBootstrapRejectsInvalidProtocolPrestateAtomically(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*state.StateDB)
	}{
		{
			name: "staking nonce",
			mutate: func(s *state.StateDB) {
				s.SetNonce(mparams.StakingContractAddr, 1)
			},
		},
		{
			name: "staking manifest slot",
			mutate: func(s *state.StateDB) {
				account, ok := testnetBootstrapAccountAt(mparams.StakingContractAddr)
				require.True(t, ok)
				require.NotEmpty(t, account.storage)
				s.SetState(mparams.StakingContractAddr, account.storage[0].key, common.HexToHash("0x01"))
			},
		},
		{
			name: "chain tally balance",
			mutate: func(s *state.StateDB) {
				s.SetBalance(mparams.ChainTallyAddr, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
			},
		},
		{
			name: "chain tally unexpected nonce",
			mutate: func(s *state.StateDB) {
				s.SetNonce(mparams.ChainTallyAddr, 2)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, h, s := testnetBootstrapFixture(t)
			test.mutate(s)
			before := s.IntermediateRoot(false)
			err := ApplyGovernanceFork(c, h, s)
			require.ErrorContains(t, err, "bootstrap target is not empty")
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}

func governanceFixture(t *testing.T) (*ethparams.ChainConfig, *types.Header, *state.StateDB) {
	t.Helper()
	db, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	require.NoError(t, err)
	c := *mparams.BdagDevnetChainConfig
	c.BdagStakingV2Block = nil
	c.BdagGovernanceForkBlock = big.NewInt(20)
	g := &ethparams.BDAGGovernanceForkConfig{
		StakingTimelock: common.HexToAddress("0x2002"), MiningTimelock: common.HexToAddress("0x2003"), Governor: common.HexToAddress("0x2004"),
		SignalWindowStartLayer: 1000, SignalWindowLayers: 2000, SignalThresholdLayers: 1500,
		ActivationDelayLayers: 2000, ActivationLayer: 5000, InitialReleaseDistributedAt: 100,
		NoticePublishedAt: 3_024_100, NotBeforeTimestamp: 3_628_900,
	}
	h := crypto.Keccak256Hash([]byte{0})
	g.ManifestHash = h
	g.StakingTimelockCodeHash = h
	g.MiningTimelockCodeHash = h
	g.GovernorCodeHash = h
	for _, a := range []common.Address{g.StakingTimelock, g.MiningTimelock, g.Governor} {
		db.SetCode(a, []byte{0})
	}
	g.StakingCreationHash = common.HexToHash("0x6b78064e1567a759eb6689707e73be8c3fcf98ed2f700067886b82b8d4862c20")
	g.StakingRuntimeHash = common.HexToHash("0xfe999a6161d4aaa7ab5184754b27631b6fbabd2a2d077220951e1ea906febd52")
	g.ReaderCreationHash = common.HexToHash("0x36f1fba4037676480c2f3f0c456354906000a7e78dc355fd70a954fa24abdfb6")
	g.ReaderRuntimeHash = common.HexToHash("0x7e8995c8a0a5c440b11024be018680b23d8faea686cd52bdbbd8e1d998ae4786")
	g.HubCreationHash = common.HexToHash("0xd246cd17a9f0ed2c01044219574cd9d825436847d93343fa877aaa2d4ca35627")
	g.HubRuntimeHash = common.HexToHash("0x692924ee9b79e97eb39fa84d8ed3d4c724f12aba981436558a7716415b67e738")
	g.LegacyProxyCreationHash = common.HexToHash("0xe26ae18ca3524ca816bba4a77eb909af5b1566bfcf8b4c0f0a663512f8a86399")
	g.LegacyProxyRuntimeHash = common.HexToHash("0x75498dc0fa24902bbd116019b098f2e3c8474c5c1f26213d56594a6457e3a7ed")
	c.BdagGovernanceFork = g
	db.SetCode(governanceProxyAddr, governanceProxyLegacyCreationCode)
	db.SetCode(mparams.StakingContractAddr, []byte{0})
	db.SetCode(mparams.BlocklistContractAddr, []byte{0})
	db.SetCode(mparams.SelectiveBlocklistContractAddr, []byte{0})
	return &c, &types.Header{Number: big.NewInt(20), Time: 3_628_900, GasLimit: 45000000, Difficulty: big.NewInt(1), BaseFee: big.NewInt(0)}, db
}

func TestTestnetGovernanceForkRequiresNativeSignalAnchor(t *testing.T) {
	for _, test := range []struct {
		name        string
		extra       func(uint64) []byte
		anchor      uint64
		wantApplied bool
		wantError   bool
	}{
		{name: "legacy header before activation", extra: func(uint64) []byte { return EncodeRewardExtra(1) }},
		{name: "unlocked extended header", extra: func(layer uint64) []byte { return EncodeRewardExtraWithLayer(1, layer) }, anchor: 0, wantError: true},
		{name: "wrong manifest anchor", extra: func(layer uint64) []byte { return EncodeRewardExtraWithLayer(1, layer) }, anchor: 5_000, wantError: true},
		{name: "signalled activation", extra: func(layer uint64) []byte { return EncodeRewardExtraWithLayer(1, layer) }, anchor: 4_999, wantApplied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, h, s := testnetBootstrapFixture(t)
			h.Extra = test.extra(c.BdagGovernanceFork.ActivationLayer)
			oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
			defer upgrades.ReplaceUpgrades(oldUpgrades)
			if test.anchor != 0 {
				upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
					upgrades.RewardGovernanceV3Name: {
						Name:           upgrades.RewardGovernanceV3Name,
						BitMask:        upgrades.RewardGovernanceV3BitMask,
						Threshold:      1,
						WindowSize:     1,
						LockedIn:       true,
						AnchorSet:      true,
						ActivateAnchor: test.anchor,
					},
				})
			}

			before := s.IntermediateRoot(false)
			err := ApplyGovernanceFork(c, h, s)
			if test.wantError {
				require.Error(t, err)
				require.Equal(t, before, s.IntermediateRoot(false))
				return
			}
			require.NoError(t, err)
			applied := s.GetState(mparams.ChainTallyAddr, governanceAppliedSlot) != (common.Hash{})
			require.Equal(t, test.wantApplied, applied)
		})
	}
}
func TestGovernanceForkManifestTiming(t *testing.T) {
	for _, name := range []string{"before-height", "before-notice", "disabled"} {
		t.Run(name, func(t *testing.T) {
			c, h, s := governanceFixture(t)
			switch name {
			case "before-height":
				h.Number = big.NewInt(19)
			case "before-notice":
				h.Time = c.BdagGovernanceFork.NotBeforeTimestamp - 1
			case "disabled":
				c.BdagGovernanceForkBlock = nil
			}
			before := s.IntermediateRoot(false)
			require.NoError(t, ApplyGovernanceFork(c, h, s))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}
func TestGovernanceForkAtomicPreservesStateAndReplays(t *testing.T) {
	c, h, s := governanceFixture(t)
	type item struct {
		a    common.Address
		k, v common.Hash
	}
	var saved []item
	for _, a := range []common.Address{mparams.StakingContractAddr, mparams.ChainTallyAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, stakingProxyAddr, stakingImplAddr} {
		for i := int64(1); i <= 18; i++ {
			k := common.BigToHash(big.NewInt(i))
			v := common.BigToHash(big.NewInt(i + 77))
			s.SetState(a, k, v)
			saved = append(saved, item{a, k, v})
		}
	}
	tally0 := common.HexToHash("0x321")
	s.SetState(mparams.ChainTallyAddr, common.Hash{}, tally0)
	s.SetCode(stakingProxyAddr, []byte{0xfe})
	s.SetCode(stakingImplAddr, []byte{0xfe})
	original := s.Copy()
	builder := s.Copy()
	importer := s.Copy()
	chain := governanceTestChain{c}
	ApplyPreBlockStateChanges(New(rawdb.NewMemoryDatabase()), chain, h, builder)
	var hook ethconsensus.PreBlockStateChanger = New(rawdb.NewMemoryDatabase())
	hook.ApplyPreBlockStateChange(chain, h, importer)
	require.Equal(t, builder.IntermediateRoot(false), importer.IntermediateRoot(false), "builder/importer hook roots differ")
	require.NoError(t, ApplyGovernanceFork(c, h, s))
	require.Equal(t, builder.IntermediateRoot(false), s.IntermediateRoot(false))
	require.NotEqual(t, original.IntermediateRoot(false), s.IntermediateRoot(false))
	for _, x := range saved {
		require.Equal(t, x.v, s.GetState(x.a, x.k), "preserve %s %s", x.a, x.k)
	}
	require.Equal(t, tally0, s.GetState(mparams.ChainTallyAddr, common.Hash{}))
	require.Equal(t, []byte{0xfe}, s.GetCode(stakingProxyAddr))
	require.Equal(t, []byte{0xfe}, s.GetCode(stakingImplAddr))
	g := c.BdagGovernanceFork
	require.Equal(t, g.StakingRuntimeHash, s.GetCodeHash(mparams.StakingV2ImplAddr))
	require.Equal(t, g.ReaderRuntimeHash, s.GetCodeHash(mparams.ChainTallyAddr))
	require.Equal(t, g.HubRuntimeHash, s.GetCodeHash(governanceHubV2Addr))
	require.Equal(t, g.LegacyProxyRuntimeHash, s.GetCodeHash(governanceProxyAddr))
	require.Equal(t, common.BytesToHash(g.StakingTimelock.Bytes()), s.GetState(mparams.StakingContractAddr, common.Hash{}))
	for _, a := range []common.Address{governanceProxyAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr} {
		require.Equal(t, common.BytesToHash(g.MiningTimelock.Bytes()), s.GetState(a, common.Hash{}))
	}
	require.Equal(t, common.BigToHash(big.NewInt(20)), s.GetState(mparams.ChainTallyAddr, governanceAppliedSlot))
	require.Equal(t, common.Hash{}, s.GetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(22))))
	require.Equal(t, common.Hash{}, s.GetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(23))))
	require.Equal(t, int64(0x100), new(big.Int).And(s.GetState(mparams.StakingContractAddr, common.BigToHash(big.NewInt(32))).Big(), big.NewInt(0x100)).Int64())
	root := s.IntermediateRoot(false)
	h.Number = big.NewInt(21)
	require.NoError(t, ApplyGovernanceFork(c, h, s))
	require.Equal(t, root, s.IntermediateRoot(false))
	// A fresh execution from the same parent state gives the same root.
	h.Number = big.NewInt(20)
	require.NoError(t, ApplyGovernanceFork(c, h, original))
	require.Equal(t, root, original.IntermediateRoot(false))
}
func TestGovernanceForkFailsBeforeAnyMutation(t *testing.T) {
	for _, name := range []string{"authority", "creation", "runtime", "unexpected-proxy", "mainnet"} {
		t.Run(name, func(t *testing.T) {
			c, h, s := governanceFixture(t)
			switch name {
			case "authority":
				s.SetCode(c.BdagGovernanceFork.MiningTimelock, nil)
			case "creation":
				c.BdagGovernanceFork.HubCreationHash = common.HexToHash("0x01")
			case "runtime":
				c.BdagGovernanceFork.HubRuntimeHash = common.HexToHash("0x01")
			case "unexpected-proxy":
				s.SetCode(governanceProxyAddr, []byte{0xfe})
			case "mainnet":
				c.ChainID = big.NewInt(1404)
			}
			before := s.IntermediateRoot(false)
			require.Error(t, ApplyGovernanceFork(c, h, s))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}

type governanceTestChain struct{ c *ethparams.ChainConfig }

func (c governanceTestChain) Config() *ethparams.ChainConfig            { return c.c }
func (governanceTestChain) CurrentHeader() *types.Header                { return nil }
func (governanceTestChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (governanceTestChain) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (governanceTestChain) GetHeaderByHash(common.Hash) *types.Header   { return nil }
func (governanceTestChain) GetTd(common.Hash, uint64) *big.Int          { return big.NewInt(0) }

// Exercise geth's actual importer StateProcessor in addition to the shared hook.
func TestGovernanceBuilderAndImporterStateProcessorRoots(t *testing.T) {
	config, header, parent := governanceFixture(t)
	header.Extra = EncodeRewardExtra(26000000000)
	database := rawdb.NewMemoryDatabase()
	genesis := &types.Header{Number: big.NewInt(0), Difficulty: big.NewInt(1), GasLimit: 45000000}
	rawdb.WriteHeader(database, genesis)
	rawdb.WriteCanonicalHash(database, genesis.Hash(), 0)
	engine := New(database)
	chain, err := core.NewHeaderChain(database, config, engine, func() bool { return false })
	require.NoError(t, err)
	builder, importer := parent.Copy(), parent.Copy()
	ApplyPreBlockStateChanges(engine, chain, header, builder)
	engine.Finalize(chain, header, builder, &types.Body{})
	block := types.NewBlockWithHeader(header)
	_, err = core.NewStateProcessor(config, chain).Process(block, importer, vm.Config{})
	require.NoError(t, err)
	require.Equal(t, builder.IntermediateRoot(true), importer.IntermediateRoot(true))
	require.Equal(t, common.BigToHash(big.NewInt(20)), importer.GetState(mparams.ChainTallyAddr, governanceAppliedSlot))
}

func TestGovernanceManifestRefusesOccupiedTargets(t *testing.T) {
	for _, name := range []string{"occupied-staking", "occupied-reader", "occupied-hub"} {
		t.Run(name, func(t *testing.T) {
			c, h, s := governanceFixture(t)
			switch name {
			case "occupied-staking":
				s.SetCode(mparams.StakingV2ImplAddr, []byte{0xfe})
			case "occupied-reader":
				s.SetCode(mparams.ChainTallyAddr, []byte{0xfe})
			case "occupied-hub":
				s.SetCode(governanceHubV2Addr, []byte{0xfe})
			}
			before := s.IntermediateRoot(false)
			require.Error(t, ApplyGovernanceFork(c, h, s))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}
