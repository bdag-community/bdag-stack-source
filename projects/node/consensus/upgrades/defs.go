package upgrades

import base "github.com/BlockdagNetworkLabs/bdag/upgrades"

// New votes only; earlier governance and timing histories remain unchanged.
func SeparateStakingV1Definition(startLayer uint64) *base.UpgradeDefinition {
	return &base.UpgradeDefinition{Name: base.SeparateStakingV1Name, BitMask: base.SeparateStakingV1BitMask,
		StartLayer: startLayer, TimeoutLayer: startLayer + 2000, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000}
}

// mainnetBlocklistStartLayer is the first DAG layer where the shipped
// blocklist voting windows may count version-bit signals. It is intentionally
// set just behind the current mainnet DAG layer for this release so upgraded
// miners start signalling the blocklist bits immediately after deployment.
const mainnetBlocklistStartLayer uint64 = 8692800

const (
	mainnetBlocklistWindowSize      uint64 = 2000
	mainnetBlocklistThreshold       uint64 = 1500
	mainnetBlocklistActivationDelay uint64 = 2000

	// Canonical EVM voting parameters as originally shipped. Retained only so
	// databases with persisted CanonicalEVMStateV1 state remain readable.
	mainnetCanonicalEVMStartLayer      uint64 = 11_020_000
	mainnetCanonicalEVMWindowSize      uint64 = 2_000
	mainnetCanonicalEVMThreshold       uint64 = 1_600
	mainnetCanonicalEVMActivationDelay uint64 = 4_000
)

// WalletBlocklist activates consensus-level rejection of transactions whose
// sender or recipient is present in the on-chain governance blocklist contract
// (BlocklistContractAddr in bevm/params/blocklist.go).
//
// Mainnet parameters: voting begins at StartLayer, tallied over fixed
// 2000-layer windows. Lock-in requires 1500 of 2000 (75%) main-chain blocks in
// a window to signal bit 0x1; enforcement starts ActivationDelayBlocks layers
// after the lock-in window ends. These values are consensus-critical and must
// never change once a release is shipped.
var walletBlocklist = &base.UpgradeDefinition{
	Name:                  base.WalletBlocklistName,
	BitMask:               0x1,
	WindowSize:            mainnetBlocklistWindowSize,
	Threshold:             mainnetBlocklistThreshold,
	ActivationDelayBlocks: mainnetBlocklistActivationDelay,
	StartLayer:            mainnetBlocklistStartLayer,
	TimeoutLayer:          0, // never times out
}

// selectiveBlocklist adds per-sender to-address whitelisting on top of the
// existing full-block mechanism. A "restricted" address may only send
// transactions to destinations approved in the SelectiveBlocklist contract.
// Full blocking (WalletBlocklist) takes precedence: a fully-blocked address is
// rejected regardless of any whitelist entry.
//
// SelectiveBlocklistContractAddr in bevm/params/blocklist.go is pinned for this
// release. Voting starts at the same layer as WalletBlocklist so both
// governance rule sets signal and lock in together.
var selectiveBlocklist = &base.UpgradeDefinition{
	Name:                  base.SelectiveBlocklistName,
	BitMask:               0x2,
	WindowSize:            mainnetBlocklistWindowSize,
	Threshold:             mainnetBlocklistThreshold,
	ActivationDelayBlocks: mainnetBlocklistActivationDelay,
	StartLayer:            mainnetBlocklistStartLayer,
	TimeoutLayer:          0, // never times out
}

// authorizedMining is a retired compatibility definition for an accidentally
// accepted mainnet upgrade. The historical definition must remain registered so
// nodes can load and rebuild persisted upgrade state containing
// "AuthorizedMining"; no current consensus rule path references this name.
var authorizedMining = &base.UpgradeDefinition{
	Name:                  base.AuthorizedMiningName,
	BitMask:               0x4,
	WindowSize:            mainnetBlocklistWindowSize,
	Threshold:             mainnetBlocklistThreshold,
	ActivationDelayBlocks: mainnetBlocklistActivationDelay,
	StartLayer:            mainnetBlocklistStartLayer,
	TimeoutLayer:          0, // never times out
}

// canonicalEVMStateV1 is retained with its shipped parameters so databases
// containing its persisted vote and lock-in state remain readable. Runtime
// consensus and template code intentionally no longer acts on this definition:
// its header commitment proved unstable when a native DAG block changed order.
var canonicalEVMStateV1 = &base.UpgradeDefinition{
	Name:                  base.CanonicalEVMStateV1Name,
	BitMask:               base.CanonicalEVMStateV1BitMask,
	WindowSize:            mainnetCanonicalEVMWindowSize,
	Threshold:             mainnetCanonicalEVMThreshold,
	ActivationDelayBlocks: mainnetCanonicalEVMActivationDelay,
	StartLayer:            mainnetCanonicalEVMStartLayer,
	TimeoutLayer:          0,
}

// RewardGovernanceV3Definition returns the immutable signalling rules for a
// reviewed release manifest. Callers must supply the publicly announced start
// layer; the default Defs set intentionally does not activate a candidate.
func RewardGovernanceV3Definition(startLayer uint64) *base.UpgradeDefinition {
	return &base.UpgradeDefinition{
		Name:                  base.RewardGovernanceV3Name,
		BitMask:               base.RewardGovernanceV3BitMask,
		WindowSize:            2_000,
		Threshold:             1_500,
		ActivationDelayBlocks: 2_000,
		StartLayer:            startLayer,
		TimeoutLayer:          0,
	}
}

// TestnetTimingV1Definition permits exactly the newly published voting window.
// The caller must validate start-layer overflow against the immutable manifest.
func TestnetTimingV1Definition(startLayer uint64) *base.UpgradeDefinition {
	return &base.UpgradeDefinition{
		Name: base.TestnetTimingV1Name, BitMask: base.TestnetTimingV1BitMask,
		WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000,
		StartLayer: startLayer, TimeoutLayer: startLayer + 2000,
	}
}

// Defs lists all default consensus upgrade definitions.
var Defs = []*base.UpgradeDefinition{
	walletBlocklist,
	selectiveBlocklist,
	authorizedMining,
	canonicalEVMStateV1,
}
