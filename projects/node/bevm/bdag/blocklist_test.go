package bdag

import (
	"errors"
	"math/big"
	"testing"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

func newBlocklistTestState(t *testing.T) *state.StateDB {
	t.Helper()
	db := rawdb.NewMemoryDatabase()
	sdb := state.NewDatabase(triedb.NewDatabase(db, nil), nil)
	statedb, err := state.New(types.EmptyRootHash, sdb)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	return statedb
}

func TestBlockedSlotMatchesSolidityLayout(t *testing.T) {
	addr := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	// Solidity mapping slot: keccak256(abi.encode(key, baseSlot)) with
	// blocked at slot 1.
	want := crypto.Keccak256Hash(
		common.LeftPadBytes(addr.Bytes(), 32),
		common.BigToHash(big.NewInt(1)).Bytes(),
	)
	if got := blockedSlot(addr); got != want {
		t.Fatalf("blockedSlot mismatch: got %s want %s", got.Hex(), want.Hex())
	}
}

func TestRestrictedSlotMatchesSolidityLayout(t *testing.T) {
	addr := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	// SelectiveBlocklist.restricted at slot 1: keccak256(abi.encode(addr, 1))
	want := crypto.Keccak256Hash(
		common.LeftPadBytes(addr.Bytes(), 32),
		common.BigToHash(big.NewInt(1)).Bytes(),
	)
	if got := restrictedSlot(addr); got != want {
		t.Fatalf("restrictedSlot mismatch: got %s want %s", got.Hex(), want.Hex())
	}
}

func TestAllowedToSlotMatchesSolidityLayout(t *testing.T) {
	from := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	// SelectiveBlocklist.allowedTo at slot 2 (nested mapping):
	//   level1 = keccak256(abi.encode(from, 2))
	//   slot   = keccak256(abi.encode(to, level1))
	level1 := crypto.Keccak256Hash(
		common.LeftPadBytes(from.Bytes(), 32),
		common.BigToHash(big.NewInt(2)).Bytes(),
	)
	want := crypto.Keccak256Hash(
		common.LeftPadBytes(to.Bytes(), 32),
		level1.Bytes(),
	)
	if got := allowedToSlot(from, to); got != want {
		t.Fatalf("allowedToSlot mismatch: got %s want %s", got.Hex(), want.Hex())
	}
}

func TestIsAddressBlocked(t *testing.T) {
	statedb := newBlocklistTestState(t)
	addr := common.HexToAddress("0x000000000000000000000000000000000000dEaD")

	if IsAddressBlocked(statedb, addr) {
		t.Fatal("address must not be blocked in empty state")
	}

	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(addr), common.BigToHash(common.Big1))
	if !IsAddressBlocked(statedb, addr) {
		t.Fatal("address must be blocked after setting the mapping slot")
	}

	other := common.HexToAddress("0x1111111111111111111111111111111111111111")
	if IsAddressBlocked(statedb, other) {
		t.Fatal("unrelated address must not be blocked")
	}
}

func TestIsAddressRestricted(t *testing.T) {
	statedb := newBlocklistTestState(t)
	addr := common.HexToAddress("0x000000000000000000000000000000000000dEaD")

	if IsAddressRestricted(statedb, addr) {
		t.Fatal("address must not be restricted in empty state")
	}

	statedb.SetState(mparams.SelectiveBlocklistContractAddr, restrictedSlot(addr), common.BigToHash(common.Big1))
	if !IsAddressRestricted(statedb, addr) {
		t.Fatal("address must be restricted after setting the mapping slot")
	}

	other := common.HexToAddress("0x1111111111111111111111111111111111111111")
	if IsAddressRestricted(statedb, other) {
		t.Fatal("unrelated address must not be restricted")
	}
}

func TestIsAllowedTo(t *testing.T) {
	statedb := newBlocklistTestState(t)
	from := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")

	if IsAllowedTo(statedb, from, to) {
		t.Fatal("pair must not be allowed in empty state")
	}

	statedb.SetState(mparams.SelectiveBlocklistContractAddr, allowedToSlot(from, to), common.BigToHash(common.Big1))
	if !IsAllowedTo(statedb, from, to) {
		t.Fatal("pair must be allowed after setting the mapping slot")
	}

	other := common.HexToAddress("0x2222222222222222222222222222222222222222")
	if IsAllowedTo(statedb, from, other) {
		t.Fatal("non-whitelisted destination must not be allowed")
	}
}

func TestCheckBlocklist(t *testing.T) {
	statedb := newBlocklistTestState(t)
	config := ethparams.TestChainConfig
	admissibility := GovernanceAdmissibility{walletBlocklist: true}

	key, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(key.PublicKey)
	recipient := common.HexToAddress("0x2222222222222222222222222222222222222222")
	signer := types.LatestSigner(config)

	mkTx := func(to *common.Address) *types.Transaction {
		tx := types.NewTx(&types.LegacyTx{
			Nonce:    0,
			To:       to,
			Value:    big.NewInt(1),
			Gas:      21000,
			GasPrice: big.NewInt(1),
		})
		signed, err := types.SignTx(tx, signer, key)
		if err != nil {
			t.Fatalf("sign tx: %v", err)
		}
		return signed
	}

	// Clean state: tx passes.
	if err := admissibility.CheckTx(statedb, config, mkTx(&recipient)); err != nil {
		t.Fatalf("clean tx must pass, got %v", err)
	}

	// Blocked recipient.
	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(recipient), common.BigToHash(common.Big1))
	if err := admissibility.CheckTx(statedb, config, mkTx(&recipient)); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("blocked recipient must be rejected, got %v", err)
	}

	// Blocked sender (send to a clean address).
	clean := common.HexToAddress("0x3333333333333333333333333333333333333333")
	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(sender), common.BigToHash(common.Big1))
	if err := admissibility.CheckTx(statedb, config, mkTx(&clean)); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("blocked sender must be rejected, got %v", err)
	}

	// Blocked sender is rejected even for zero-value transactions because it
	// must spend native BDAG for gas.
	zeroValueTx := types.NewTx(&types.LegacyTx{
		Nonce:    0,
		To:       &clean,
		Value:    big.NewInt(0),
		Gas:      21000,
		GasPrice: big.NewInt(1),
	})
	signedZeroValueTx, err := types.SignTx(zeroValueTx, signer, key)
	if err != nil {
		t.Fatalf("sign zero-value tx: %v", err)
	}
	if err := admissibility.CheckTx(statedb, config, signedZeroValueTx); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("blocked sender zero-value tx must be rejected, got %v", err)
	}
}

func TestBlockedRecipientRejectedEvenWithoutValue(t *testing.T) {
	statedb := newBlocklistTestState(t)
	config := ethparams.TestChainConfig
	admissibility := GovernanceAdmissibility{walletBlocklist: true}

	key, _ := crypto.GenerateKey()
	recipient := common.HexToAddress("0x2222222222222222222222222222222222222222")
	signer := types.LatestSigner(config)
	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(recipient), common.BigToHash(common.Big1))

	mkTx := func(value *big.Int) *types.Transaction {
		tx := types.NewTx(&types.LegacyTx{
			Nonce:    0,
			To:       &recipient,
			Value:    value,
			Gas:      21000,
			GasPrice: big.NewInt(1),
		})
		signed, err := types.SignTx(tx, signer, key)
		if err != nil {
			t.Fatalf("sign tx: %v", err)
		}
		return signed
	}

	if err := admissibility.CheckTx(statedb, config, mkTx(big.NewInt(0))); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("zero-value tx to blocked recipient must be rejected, got %v", err)
	}
	if err := admissibility.CheckTx(statedb, config, mkTx(big.NewInt(1))); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("value tx to blocked recipient must be rejected, got %v", err)
	}
}

func TestConsensusAdmissibilityDelaysNativeTransfersUntilPatchLayer(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	defer upgrades.ReplaceUpgrades(oldUpgrades)

	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{
		Name:       upgrades.WalletBlocklistName,
		BitMask:    0x1,
		Threshold:  1,
		WindowSize: 1,
	}); err != nil {
		t.Fatalf("register wallet blocklist upgrade: %v", err)
	}
	if err := upgrades.SetUpgradeLockedIn(upgrades.WalletBlocklistName, 1); err != nil {
		t.Fatalf("lock wallet blocklist upgrade: %v", err)
	}

	before := ConsensusGovernanceAdmissibilityAtLayer(mparams.NativeBlocklistEnforcementLayer - 1)
	after := ConsensusGovernanceAdmissibilityAtLayer(mparams.NativeBlocklistEnforcementLayer)
	if !before.walletBlocklist || before.nativeWalletBlocklist {
		t.Fatalf("before patch layer got wallet=%v nativeWallet=%v", before.walletBlocklist, before.nativeWalletBlocklist)
	}
	if !after.walletBlocklist || !after.nativeWalletBlocklist {
		t.Fatalf("at patch layer got wallet=%v nativeWallet=%v", after.walletBlocklist, after.nativeWalletBlocklist)
	}

	statedb := newBlocklistTestState(t)
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(to), common.BigToHash(common.Big1))

	if err := before.CheckTransaction(statedb, from, &to, big.NewInt(1)); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("direct top-level value tx must still be rejected before patch layer, got %v", err)
	}
	if err := before.CheckNativeTransfer(statedb, from, to, uint256.NewInt(1), vm.NativeTransferReasonCallValue); err != nil {
		t.Fatalf("internal native transfer must not be consensus-rejected before patch layer, got %v", err)
	}
	if err := after.CheckNativeTransfer(statedb, from, to, uint256.NewInt(1), vm.NativeTransferReasonCallValue); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("internal native transfer must be rejected at patch layer, got %v", err)
	}
}

func TestConsensusAdmissibilityCarriesRewardGovernanceLayer(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		upgrades.RewardGovernanceV3Name: {
			Name:           upgrades.RewardGovernanceV3Name,
			BitMask:        upgrades.RewardGovernanceV3BitMask,
			Threshold:      1,
			WindowSize:     1,
			LockedIn:       true,
			AnchorSet:      true,
			ActivateAnchor: 100,
		},
	})
	defer upgrades.ReplaceUpgrades(oldUpgrades)

	before := ConsensusGovernanceAdmissibilityAtLayer(99)
	if before.nativeLayer != 99 || before.rewardGovernance {
		t.Fatalf("before activation got layer=%d rewardGovernance=%v", before.nativeLayer, before.rewardGovernance)
	}
	at := ConsensusGovernanceAdmissibilityAtLayer(100)
	if at.nativeLayer != 100 || !at.rewardGovernance {
		t.Fatalf("at activation got layer=%d rewardGovernance=%v", at.nativeLayer, at.rewardGovernance)
	}
}
func TestCheckSelectiveBlocklist(t *testing.T) {
	statedb := newBlocklistTestState(t)
	config := ethparams.TestChainConfig
	disabled := NoGovernanceAdmissibility()
	selective := GovernanceAdmissibility{selectiveBlocklist: true}

	key, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(key.PublicKey)
	allowed := common.HexToAddress("0x4444444444444444444444444444444444444444")
	notAllowed := common.HexToAddress("0x5555555555555555555555555555555555555555")
	signer := types.LatestSigner(config)

	mkTxWithValue := func(to *common.Address, value *big.Int) *types.Transaction {
		tx := types.NewTx(&types.LegacyTx{
			Nonce:    0,
			To:       to,
			Value:    value,
			Gas:      21000,
			GasPrice: big.NewInt(1),
		})
		signed, err := types.SignTx(tx, signer, key)
		if err != nil {
			t.Fatalf("sign tx: %v", err)
		}
		return signed
	}
	mkTx := func(to *common.Address) *types.Transaction {
		return mkTxWithValue(to, big.NewInt(1))
	}

	// Mark sender as restricted with one approved destination.
	statedb.SetState(mparams.SelectiveBlocklistContractAddr, restrictedSlot(sender), common.BigToHash(common.Big1))
	statedb.SetState(mparams.SelectiveBlocklistContractAddr, allowedToSlot(sender, allowed), common.BigToHash(common.Big1))

	// disabled: restriction is ignored.
	if err := disabled.CheckTx(statedb, config, mkTx(&notAllowed)); err != nil {
		t.Fatalf("selective block disabled: tx must pass, got %v", err)
	}

	// selective enabled: sending to approved destination passes.
	if err := selective.CheckTx(statedb, config, mkTx(&allowed)); err != nil {
		t.Fatalf("restricted sender to allowed destination must pass, got %v", err)
	}

	// selective enabled: sending to non-whitelisted destination fails.
	if err := selective.CheckTx(statedb, config, mkTx(&notAllowed)); !errors.Is(err, ErrRestrictedAddress) {
		t.Fatalf("restricted sender to non-whitelisted destination must be rejected, got %v", err)
	}
	if err := selective.CheckTx(statedb, config, mkTxWithValue(&notAllowed, big.NewInt(0))); !errors.Is(err, ErrRestrictedAddress) {
		t.Fatalf("restricted sender zero-value tx to non-whitelisted destination must be rejected, got %v", err)
	}

	// selective enabled: contract creation (nil to) fails for restricted sender.
	if err := selective.CheckTx(statedb, config, mkTx(nil)); !errors.Is(err, ErrRestrictedAddress) {
		t.Fatalf("restricted sender contract creation must be rejected, got %v", err)
	}
	if err := selective.CheckTx(statedb, config, mkTxWithValue(nil, big.NewInt(0))); !errors.Is(err, ErrRestrictedAddress) {
		t.Fatalf("restricted sender zero-value contract creation must be rejected, got %v", err)
	}

	// Full block takes precedence: even if destination is whitelisted, a fully
	// blocked sender is rejected with ErrBlockedAddress not ErrRestrictedAddress.
	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(sender), common.BigToHash(common.Big1))
	if err := selective.CheckTx(statedb, config, mkTx(&allowed)); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("full block must take precedence over selective block, got %v", err)
	}
}

func TestSelectiveBlocklistNativeTransferPolicy(t *testing.T) {
	statedb := newBlocklistTestState(t)
	selective := GovernanceAdmissibility{selectiveBlocklist: true, nativeSelectiveBlocklist: true}
	amount := uint256.NewInt(1)

	staker := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	staking := mparams.StakingContractAddr

	statedb.SetState(mparams.SelectiveBlocklistContractAddr, restrictedSlot(staking), common.BigToHash(common.Big1))
	statedb.SetState(mparams.SelectiveBlocklistContractAddr, allowedToSlot(staking, staker), common.BigToHash(common.Big1))

	if err := selective.CheckNativeTransfer(statedb, staker, staking, amount, vm.NativeTransferReasonCallValue); err != nil {
		t.Fatalf("inbound stake deposit must pass, got %v", err)
	}
	if err := selective.CheckNativeTransfer(statedb, staking, staker, amount, vm.NativeTransferReasonCallValue); err != nil {
		t.Fatalf("staking payout to original staker must pass, got %v", err)
	}
	if err := selective.CheckNativeTransfer(statedb, staking, owner, amount, vm.NativeTransferReasonCallValue); !errors.Is(err, ErrRestrictedAddress) {
		t.Fatalf("staking payout to unapproved destination must be rejected, got %v", err)
	}
	if err := selective.CheckNativeTransfer(statedb, staking, owner, uint256.NewInt(0), vm.NativeTransferReasonCallValue); err != nil {
		t.Fatalf("zero-value call from restricted staking address must pass, got %v", err)
	}
}

func TestNativeTransferAuthorizerWrapsPolicyErrors(t *testing.T) {
	statedb := newBlocklistTestState(t)
	selective := GovernanceAdmissibility{selectiveBlocklist: true, nativeSelectiveBlocklist: true}

	staker := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	staking := mparams.StakingContractAddr

	statedb.SetState(mparams.SelectiveBlocklistContractAddr, restrictedSlot(staking), common.BigToHash(common.Big1))
	statedb.SetState(mparams.SelectiveBlocklistContractAddr, allowedToSlot(staking, staker), common.BigToHash(common.Big1))

	authorizer := selective.NativeTransferAuthorizer()
	err := authorizer(statedb, staking, owner, uint256.NewInt(1), vm.NativeTransferReasonCallValue)
	if !errors.Is(err, vm.ErrNativeTransferBlocked) {
		t.Fatalf("authorizer error must wrap ErrNativeTransferBlocked, got %v", err)
	}
	if !errors.Is(err, ErrRestrictedAddress) {
		t.Fatalf("authorizer error must preserve restricted-address cause, got %v", err)
	}
}

func TestBdagGovernanceAdmissionHookRejectsBlockedSender(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	defer upgrades.ReplaceUpgrades(oldUpgrades)
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{
		Name:       upgrades.WalletBlocklistName,
		BitMask:    0x1,
		Threshold:  1,
		WindowSize: 1,
	}); err != nil {
		t.Fatalf("register wallet blocklist upgrade: %v", err)
	}
	if err := upgrades.SetUpgradeActivated(upgrades.WalletBlocklistName, 1); err != nil {
		t.Fatalf("activate wallet blocklist upgrade: %v", err)
	}

	statedb := newBlocklistTestState(t)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sender := crypto.PubkeyToAddress(key.PublicKey)
	statedb.SetState(mparams.BlocklistContractAddr, blockedSlot(sender), common.BigToHash(common.Big1))

	signer := types.LatestSigner(ethparams.TestChainConfig)
	tx, err := types.SignTx(types.NewTx(&types.LegacyTx{
		Nonce:    0,
		To:       &common.Address{},
		Value:    big.NewInt(1),
		Gas:      21000,
		GasPrice: big.NewInt(1),
	}), signer, key)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}

	if err := bdagGovernanceAdmissionHook(statedb, signer, tx); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("admission hook error = %v, want ErrBlockedAddress", err)
	}
}
