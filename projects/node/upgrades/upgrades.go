package upgrades

import (
	"fmt"
	"math/bits"
	"sync"
	"sync/atomic"
)

// WalletBlocklistName is the canonical name of the wallet-blocking consensus
// upgrade. It is defined here (rather than in consensus/upgrades) so that the
// enforcement sites in core/blockchain and bevm/bdag can reference it without
// importing the definition package.
const WalletBlocklistName = "WalletBlocklist"

// SelectiveBlocklistName is the canonical name of the selective-blocking
// consensus upgrade. It gates enforcement of the per-sender to-address
// whitelist stored in the SelectiveBlocklist contract.
const SelectiveBlocklistName = "SelectiveBlocklist"

// AuthorizedMiningName is the canonical name of the retired
// mining-authorization consensus upgrade. Some mainnet databases contain
// persisted state for this accidentally accepted upgrade, so the name remains
// registered for compatibility even though no rules are enforced from it.
const AuthorizedMiningName = "AuthorizedMining"

// CanonicalEVMStateV1Name is the canonical name of the retired native-header
// EVM state commitment upgrade. Some databases contain persisted vote and
// lock-in state for this upgrade, so the name remains registered for
// compatibility. Runtime consensus must not signal or enforce it: the header
// commitment proved unstable when a native DAG block changed order.
const CanonicalEVMStateV1Name = "CanonicalEVMStateV1"

// CanonicalEVMStateV1BitMask is the historical version bit for
// CanonicalEVMStateV1.
const CanonicalEVMStateV1BitMask uint32 = 0x8

// RewardGovernanceV3Name identifies the Guardian-free reward-voting release.
// It is registered only by a separately approved release manifest so this
// source change cannot silently choose a production signalling start layer.
const RewardGovernanceV3Name = "RewardGovernanceV3"
const RewardGovernanceV3BitMask uint32 = 0x10

// TestnetTimingV1 is the separately approved, one-time Testnet timing correction.
// It is never in the default/Mainnet definitions and never reuses V3's votes.
const TestnetTimingV1Name = "TestnetTimingV1"
const TestnetTimingV1BitMask uint32 = 0x20

const SeparateStakingV1Name = "SeparateStakingV1"
const SeparateStakingV1BitMask uint32 = 0x40

// MainnetDAOStakingV2 has a new identity: never reinterpret the persisted
// Testnet/V3 windows. No default network registers this review definition.
const MainnetDAOStakingV2Name = "MainnetDAOStakingV2"
const MainnetDAOStakingV2BitMask uint32 = 0x80

// UpgradeDefinition defines an individual consensus upgrade controlled by
// version bits signalling.
type UpgradeDefinition struct {
	Name    string
	BitMask uint32
	// WindowSize defines how many consecutive layers compose a voting
	// window when tallying version-bit signals for the upgrade.
	WindowSize uint64
	// Threshold is the minimum number of signalling headers within a
	// window required to lock in the upgrade.
	Threshold uint64
	// ActivationDelayBlocks specifies how many additional layers must pass
	// after a window reaches the threshold before the rules activate.
	ActivationDelayBlocks uint64
	// StartLayer is the first layer height at which votes for the upgrade
	// are counted.
	StartLayer uint64
	// TimeoutLayer, when non-zero, disables the upgrade if the threshold
	// has not been reached before the window that starts at this layer.
	TimeoutLayer   uint64
	Active         bool
	LockedIn       bool
	AnchorSet      bool
	ActivateAnchor uint64
	ThresholdAt    uint64
}

// defaultActivationDelayBlocks defines how many layers after a threshold is
// reached the new rules become active when an upgrade definition does not
// specify its own delay. The value can be overridden by network parameters or
// CLI flags during initialization.
var defaultActivationDelayBlocks atomic.Uint64

func init() {
	defaultActivationDelayBlocks.Store(0)
}

// DefaultActivationDelayBlocks returns the default activation delay, measured
// in layers, that is applied to upgrades which do not specify an explicit
// ActivationDelayBlocks value.
func DefaultActivationDelayBlocks() uint64 {
	return defaultActivationDelayBlocks.Load()
}

// SetDefaultActivationDelayBlocks updates the default activation delay that is
// used when registering upgrades without an explicit delay.
func SetDefaultActivationDelayBlocks(delay uint64) {
	defaultActivationDelayBlocks.Store(delay)
}

var (
	upgradeMu sync.RWMutex
	// upgrades lists all defined consensus upgrades.
	upgrades = map[string]*UpgradeDefinition{}

	// persistState, when set, is called whenever an upgrade's state changes
	// and may return an error which will be propagated to the caller.
	persistState func(string, *UpgradeDefinition) error
)

// UpgradeRLock acquires a read lock on the upgrades state.
func UpgradeRLock() {
	upgradeMu.RLock()
}

// UpgradeRUnlock releases a previously acquired read lock.
func UpgradeRUnlock() {
	upgradeMu.RUnlock()
}

// UpgradeLock acquires a write lock on the upgrades state.
func UpgradeLock() {
	upgradeMu.Lock()
}

// UpgradeUnlock releases a previously acquired write lock.
func UpgradeUnlock() {
	upgradeMu.Unlock()
}

// GetUpgrades returns a copy of all registered upgrades.
// It acquires a read lock while copying the data to ensure consistency.
func GetUpgrades() map[string]*UpgradeDefinition {
	upgradeMu.RLock()
	defer upgradeMu.RUnlock()
	m := make(map[string]*UpgradeDefinition, len(upgrades))
	for k, v := range upgrades {
		cp := *v   // copy value
		m[k] = &cp // store pointer to copy (prevents external mutation)
	}
	return m
}

// ForEachUpgrade executes fn for every registered upgrade while holding
// a read lock. The function should not attempt to modify the upgrades map.
func ForEachUpgrade(fn func(string, *UpgradeDefinition)) {
	upgradeMu.RLock()
	defer upgradeMu.RUnlock()
	for name, u := range upgrades {
		fn(name, u)
	}
}

// ReplaceUpgrades swaps the current upgrades map with the provided one and
// returns the previous map. It acquires a write lock while replacing the map.
// This function is intended for tests that need to manipulate the global
// upgrades state.
func ReplaceUpgrades(newUpgrades map[string]*UpgradeDefinition) map[string]*UpgradeDefinition {
	upgradeMu.Lock()
	defer upgradeMu.Unlock()
	old := upgrades
	upgrades = newUpgrades
	return old
}

// UpdateUpgrade finds the named upgrade and applies fn to it while holding a
// write lock. If the upgrade does not exist, fn is not called.
func UpdateUpgrade(name string, fn func(*UpgradeDefinition)) {
	upgradeMu.Lock()
	defer upgradeMu.Unlock()
	if u, ok := upgrades[name]; ok {
		fn(u)
	}
}

// RebuiltState is the consensus state derived from a complete header scan.
type RebuiltState struct {
	Active         bool
	LockedIn       bool
	AnchorSet      bool
	ActivateAnchor uint64
	ThresholdAt    uint64
}

// PublishRebuiltStates makes persistence and publication one logical update.
// persist must atomically commit all states and the corresponding checkpoint;
// an error leaves the visible upgrade states unchanged.
func PublishRebuiltStates(states map[string]RebuiltState, persist func() error) error {
	upgradeMu.Lock()
	defer upgradeMu.Unlock()
	if len(states) != len(upgrades) {
		return fmt.Errorf("rebuilt upgrade count %d does not match definitions %d", len(states), len(upgrades))
	}
	for name := range states {
		if _, ok := upgrades[name]; !ok {
			return fmt.Errorf("unknown rebuilt upgrade %s", name)
		}
	}
	if err := persist(); err != nil {
		return err
	}
	for name, state := range states {
		u := upgrades[name]
		u.Active, u.LockedIn, u.AnchorSet = state.Active, state.LockedIn, state.AnchorSet
		u.ActivateAnchor, u.ThresholdAt = state.ActivateAnchor, state.ThresholdAt
	}
	return nil
}

// RegisterUpgrade adds a new upgrade definition after validating that the
// name is unique and the bit mask does not overlap with existing upgrades.
func RegisterUpgrade(def *UpgradeDefinition) error {
	upgradeMu.Lock()
	defer upgradeMu.Unlock()
	if bits.OnesCount32(def.BitMask) != 1 {
		return fmt.Errorf("upgrade %s bitmask must have exactly one bit set", def.Name)
	}
	if def.WindowSize == 0 {
		return fmt.Errorf("upgrade %s window size must be > 0", def.Name)
	}
	if def.Threshold == 0 || def.Threshold > def.WindowSize {
		return fmt.Errorf("upgrade %s threshold must be between 1 and the window size", def.Name)
	}
	if def.TimeoutLayer != 0 && def.StartLayer >= def.TimeoutLayer {
		return fmt.Errorf("upgrade %s start layer must be before timeout", def.Name)
	}

	if def.ActivationDelayBlocks == 0 {
		def.ActivationDelayBlocks = defaultActivationDelayBlocks.Load()
	}

	if _, exists := upgrades[def.Name]; exists {
		return fmt.Errorf("upgrade %s already registered", def.Name)
	}
	for name, existing := range upgrades {
		if existing.BitMask&def.BitMask != 0 {
			return fmt.Errorf("upgrade %s bitmask overlaps with %s", def.Name, name)
		}
	}
	upgrades[def.Name] = def
	return nil
}

// RegisterPersist registers a callback to persist upgrade state changes.
func RegisterPersist(cb func(string, *UpgradeDefinition) error) {
	upgradeMu.Lock()
	persistState = cb
	upgradeMu.Unlock()
}

// SetUpgradeLockedIn marks the upgrade as locked in with the provided
// activation time.
// Returns an error if the upgrade name does not exist.
func SetUpgradeLockedIn(name string, activateAnchor uint64) error {
	upgradeMu.Lock()

	u, ok := upgrades[name]
	if !ok {
		upgradeMu.Unlock()
		return fmt.Errorf("unknown upgrade %s", name)
	}

	u.LockedIn = true
	u.AnchorSet = true
	u.ActivateAnchor = activateAnchor
	if u.ActivationDelayBlocks > 0 && activateAnchor >= u.ActivationDelayBlocks {
		u.ThresholdAt = activateAnchor - u.ActivationDelayBlocks
	} else {
		u.ThresholdAt = activateAnchor
	}
	// Create a copy to pass to the persistence callback outside the lock.
	copied := *u
	cb := persistState
	upgradeMu.Unlock()

	if cb != nil {
		if err := cb(name, &copied); err != nil {
			return err
		}
	}

	return nil
}

// IsUpgradeActive returns true if the given upgrade is active for the
// provided timestamp.
func IsUpgradeActive(name string, layer uint64) bool {
	upgradeMu.RLock()
	defer upgradeMu.RUnlock()
	u, ok := upgrades[name]
	if !ok {
		return false
	}
	if !u.Active {
		return false
	}
	return layer >= u.ActivateAnchor
}

// IsEnforcedAtLayer reports whether the named upgrade's rules must be enforced
// for a block at the given layer. Unlike IsUpgradeActive, this does not depend
// on the mutable Active flag (which only flips once the tip passes the anchor
// and can therefore be observed at different blocks depending on how blocks are
// batched during sync). It keys solely off the locked-in anchor, which is set
// ActivationDelayBlocks layers before enforcement begins and is a deterministic
// function of chain headers. This is the consensus-authoritative gate.
func IsEnforcedAtLayer(name string, layer uint64) bool {
	upgradeMu.RLock()
	defer upgradeMu.RUnlock()
	u, ok := upgrades[name]
	if !ok {
		return false
	}
	if !u.AnchorSet {
		return false
	}
	return layer >= u.ActivateAnchor
}

// RewardLayerBindingEnforced preserves each historical definition while also
// enabling the independently named production transition's native-layer header.
func RewardLayerBindingEnforced(layer uint64) bool {
	return IsEnforcedAtLayer(RewardGovernanceV3Name, layer) || IsEnforcedAtLayer(MainnetDAOStakingV2Name, layer)
}

// IsActive reports whether the named upgrade has been activated on the current
// chain, independent of any specific layer. Once an upgrade activates it stays
// active, so this is suitable for best-effort, non-authoritative gates such as
// the mempool/template builder. Consensus-authoritative checks should use
// IsEnforcedAtLayer with the layer of the block being validated.
func IsActive(name string) bool {
	upgradeMu.RLock()
	defer upgradeMu.RUnlock()
	u, ok := upgrades[name]
	if !ok {
		return false
	}
	return u.Active
}

// SetUpgradeActivated marks an upgrade as active at the given timestamp.
// Returns an error if the upgrade name does not exist.
func SetUpgradeActivated(name string, layer uint64) error {
	upgradeMu.Lock()

	u, ok := upgrades[name]
	if !ok {
		upgradeMu.Unlock()
		fmt.Printf("SetUpgradeActivated: unknown upgrade %s\n", name)
		return fmt.Errorf("unknown upgrade %s", name)
	}

	u.Active = true
	u.LockedIn = true
	u.AnchorSet = true
	u.ActivateAnchor = layer
	if u.ThresholdAt == 0 {
		if u.ActivationDelayBlocks > 0 && layer >= u.ActivationDelayBlocks {
			u.ThresholdAt = layer - u.ActivationDelayBlocks
		} else {
			u.ThresholdAt = layer
		}
	}
	copied := *u
	cb := persistState
	upgradeMu.Unlock()

	fmt.Printf("SetUpgradeActivated: upgrade %s activated at layer %d\n", name, layer)
	fmt.Printf("SetUpgradeActivated: upgrade %s state: Active=%v, LockedIn=%v, AnchorSet=%v, ActivateAnchor=%d, ThresholdAt=%d\n",
		name, u.Active, u.LockedIn, u.AnchorSet, u.ActivateAnchor, u.ThresholdAt)

	if cb != nil {
		if err := cb(name, &copied); err != nil {
			fmt.Printf("SetUpgradeActivated: persist callback error for upgrade %s: %v\n", name, err)
			return err
		}
	}
	return nil
}

// GetSignalingBits returns the bit mask that should be signalled in new block
// versions for all known upgrades which are not yet locked in or active. The
// caller supplies the current layer to evaluate start and timeout windows.
func GetSignalingBits(nowLayer uint64) uint32 {
	upgradeMu.RLock()
	defer upgradeMu.RUnlock()
	if len(upgrades) == 0 {
		return 0
	}

	var bits uint32
	for _, u := range upgrades {
		if u.Active {
			continue
		}
		if u.AnchorSet && nowLayer >= u.ThresholdAt {
			// Locked in: no further signalling is necessary while
			// waiting for activation.
			continue
		}
		if nowLayer < u.StartLayer {
			continue
		}
		if u.TimeoutLayer != 0 && nowLayer >= u.TimeoutLayer {
			continue
		}
		bits |= u.BitMask
	}
	return bits
}
