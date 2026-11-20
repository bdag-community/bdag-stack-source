package bdag

import (
	"fmt"
	"math/big"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// GovernanceAdmissibility decides whether a transaction is allowed under
// consensus governance state.
type GovernanceAdmissibility struct {
	walletBlocklist          bool
	selectiveBlocklist       bool
	nativeWalletBlocklist    bool
	nativeSelectiveBlocklist bool
	nativeLayer              uint64
	rewardGovernance         bool
}

// ConsensusGovernanceAdmissibilityAtLayer returns the consensus-authoritative
// governance rules for a block at layer.
func ConsensusGovernanceAdmissibilityAtLayer(layer uint64) GovernanceAdmissibility {
	rewardGovernance := upgrades.RewardLayerBindingEnforced(layer)
	walletBlocklist := upgrades.IsEnforcedAtLayer(upgrades.WalletBlocklistName, layer)
	selectiveBlocklist := upgrades.IsEnforcedAtLayer(upgrades.SelectiveBlocklistName, layer)
	nativeBlocklist := NativeBlocklistEnforcedAtLayer(layer)
	return GovernanceAdmissibility{
		walletBlocklist:          walletBlocklist,
		selectiveBlocklist:       selectiveBlocklist,
		nativeWalletBlocklist:    walletBlocklist && nativeBlocklist,
		nativeSelectiveBlocklist: selectiveBlocklist && nativeBlocklist,
		nativeLayer:              layer,
		rewardGovernance:         rewardGovernance,
	}
}

// TemplateGovernanceAdmissibility returns best-effort governance rules for
// local block template construction.
func TemplateGovernanceAdmissibility() GovernanceAdmissibility {
	walletBlocklist := upgrades.IsActive(upgrades.WalletBlocklistName)
	selectiveBlocklist := upgrades.IsActive(upgrades.SelectiveBlocklistName)
	return GovernanceAdmissibility{
		walletBlocklist:          walletBlocklist,
		selectiveBlocklist:       selectiveBlocklist,
		nativeWalletBlocklist:    walletBlocklist,
		nativeSelectiveBlocklist: selectiveBlocklist,
	}
}

// NoGovernanceAdmissibility disables governance checks for already-validated
// historical rebuilds.
func NoGovernanceAdmissibility() GovernanceAdmissibility {
	return GovernanceAdmissibility{}
}

// ReplayGovernanceAdmissibilityAtLayer preserves consensus header encoding
// while rebuilding an already-validated historical block. Transaction and
// transfer policy is not re-applied during replay, but the reward header must
// still reproduce the original activation-era format exactly.
func ReplayGovernanceAdmissibilityAtLayer(layer uint64) GovernanceAdmissibility {
	return GovernanceAdmissibility{
		nativeLayer:      layer,
		rewardGovernance: upgrades.RewardLayerBindingEnforced(layer),
	}
}

func NativeBlocklistEnforcedAtLayer(layer uint64) bool {
	return layer >= mparams.NativeBlocklistEnforcementLayer
}

func (a GovernanceAdmissibility) enabled() bool {
	return a.txEnabled() || a.nativeEnabled()
}

func (a GovernanceAdmissibility) txEnabled() bool {
	return a.walletBlocklist || a.selectiveBlocklist
}

func (a GovernanceAdmissibility) nativeEnabled() bool {
	return a.nativeWalletBlocklist || a.nativeSelectiveBlocklist
}

func (a GovernanceAdmissibility) enforceFullBlocklist() bool {
	// Full blocking takes precedence over selective restrictions. If selective
	// governance is active, a fully blocked address is still inadmissible.
	return a.walletBlocklist || a.selectiveBlocklist
}

func (a GovernanceAdmissibility) enforceNativeFullBlocklist() bool {
	return a.nativeWalletBlocklist || a.nativeSelectiveBlocklist
}

// CheckTx validates tx against the active governance rules.
func (a GovernanceAdmissibility) CheckTx(statedb blocklistStateReader, config *ethparams.ChainConfig, tx *types.Transaction) error {
	return a.checkTxWithSigner(statedb, types.LatestSigner(config), tx)
}

// CheckTxAt validates governance restrictions using the signing domain for the
// exact candidate block. This matters at the BDAG replay-protection boundary.
func (a GovernanceAdmissibility) CheckTxAt(statedb blocklistStateReader, config *ethparams.ChainConfig, header *types.Header, tx *types.Transaction) error {
	if header == nil || header.Number == nil {
		return fmt.Errorf("missing candidate header for transaction signing domain")
	}
	return a.checkTxWithSigner(statedb, types.MakeSigner(config, header.Number, header.Time), tx)
}

func (a GovernanceAdmissibility) checkTxWithSigner(statedb blocklistStateReader, signer types.Signer, tx *types.Transaction) error {
	if !a.txEnabled() {
		return nil
	}
	from, err := types.Sender(signer, tx)
	if err != nil {
		return err
	}
	return a.CheckTransaction(statedb, from, tx.To(), tx.Value())
}

// CheckTransaction validates a top-level transaction envelope against active
// governance rules. Top-level sender, recipient, and selective-address policy
// checks are envelope checks, so they do not depend on the transaction value.
func (a GovernanceAdmissibility) CheckTransaction(statedb blocklistStateReader, from common.Address, to *common.Address, value *big.Int) error {
	if !a.txEnabled() {
		return nil
	}
	if a.enforceFullBlocklist() {
		if IsAddressBlocked(statedb, from) {
			return fmt.Errorf("%w: sender %s", ErrBlockedAddress, from.Hex())
		}
		if to != nil && IsAddressBlocked(statedb, *to) {
			return fmt.Errorf("%w: recipient %s", ErrBlockedAddress, to.Hex())
		}
	}
	if a.selectiveBlocklist && IsAddressRestricted(statedb, from) {
		if to == nil {
			return fmt.Errorf("%w: sender %s destination contract creation", ErrRestrictedAddress, from.Hex())
		}
		if !IsAllowedTo(statedb, from, *to) {
			return fmt.Errorf("%w: sender %s destination %s", ErrRestrictedAddress, from.Hex(), to.Hex())
		}
	}
	return nil
}

// CheckNativeTransfer validates a native BDAG movement. Full blocklist prevents
// native value moving out of or into blocked addresses. Selective blocklist
// restricts only outbound native value from restricted addresses.
func (a GovernanceAdmissibility) CheckNativeTransfer(statedb blocklistStateReader, from, to common.Address, amount *uint256.Int, reason vm.NativeTransferReason) error {
	if !a.nativeEnabled() || amount == nil || amount.IsZero() {
		return nil
	}
	switch reason {
	case vm.NativeTransferReasonGasBuy:
		if a.enforceNativeFullBlocklist() && IsAddressBlocked(statedb, from) {
			return fmt.Errorf("%w: gas payer %s", ErrBlockedAddress, from.Hex())
		}
		return nil
	case vm.NativeTransferReasonTxFee, vm.NativeTransferReasonReward:
		return a.CheckNativeCredit(statedb, to, amount, reason)
	}
	if a.enforceNativeFullBlocklist() {
		if IsAddressBlocked(statedb, from) {
			return fmt.Errorf("%w: sender %s", ErrBlockedAddress, from.Hex())
		}
		if IsAddressBlocked(statedb, to) {
			return fmt.Errorf("%w: recipient %s", ErrBlockedAddress, to.Hex())
		}
	}
	if a.nativeSelectiveBlocklist && IsAddressRestricted(statedb, from) && !IsAllowedTo(statedb, from, to) {
		return fmt.Errorf("%w: sender %s destination %s", ErrRestrictedAddress, from.Hex(), to.Hex())
	}
	return nil
}

// CheckNativeCredit validates a protocol/native credit where there is no normal
// user-controlled sender, such as transaction fees paid to coinbase.
func (a GovernanceAdmissibility) CheckNativeCredit(statedb blocklistStateReader, to common.Address, amount *uint256.Int, reason vm.NativeTransferReason) error {
	if !a.nativeEnabled() || amount == nil || amount.IsZero() {
		return nil
	}
	if a.enforceNativeFullBlocklist() && IsAddressBlocked(statedb, to) {
		return fmt.Errorf("%w: %s recipient %s", ErrBlockedAddress, reason, to.Hex())
	}
	return nil
}

// NativeTransferAuthorizer adapts the governance checks to the forked EVM
// native-transfer policy hook.
func (a GovernanceAdmissibility) NativeTransferAuthorizer() vm.NativeTransferAuthorizerFunc {
	if !a.nativeEnabled() {
		return nil
	}
	return func(statedb vm.StateDB, from, to common.Address, amount *uint256.Int, reason vm.NativeTransferReason) error {
		if err := a.CheckNativeTransfer(statedb, from, to, amount, reason); err != nil {
			return fmt.Errorf("%w: %w", vm.ErrNativeTransferBlocked, err)
		}
		return nil
	}
}

func (a GovernanceAdmissibility) applyToVMConfig(cfg vm.Config) vm.Config {
	cfg.NativeTransferAuthorizer = a.NativeTransferAuthorizer()
	return cfg
}
