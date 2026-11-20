package blockchain

import (
	"fmt"
	"math/big"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	ethparams "github.com/ethereum/go-ethereum/params"
)

func testnetTimingDifficultyParams(base *params.Params, policy *ethparams.BDAGTestnetTimingForkConfig, definition *upgrades.UpgradeDefinition, parentLayer uint64, parentMedianTime int64) (*params.Params, error) {
	if base == nil || base.Net != params.TestNetParams.Net || policy == nil {
		return base, nil
	}
	if err := policy.Validate(big.NewInt(1043)); err != nil {
		return nil, err
	}
	if definition == nil {
		return base, nil
	}
	if definition.Name != upgrades.TestnetTimingV1Name || definition.BitMask != upgrades.TestnetTimingV1BitMask ||
		definition.StartLayer != policy.SignalStartLayer || definition.TimeoutLayer != policy.SignalStartLayer+2000 ||
		definition.WindowSize != 2000 || definition.Threshold != 1500 || definition.ActivationDelayBlocks != 2000 {
		return nil, fmt.Errorf("difficulty timing definition conflicts with immutable release")
	}
	if !definition.LockedIn && !definition.AnchorSet && !definition.Active && definition.ActivateAnchor == 0 {
		return base, nil
	}
	if !definition.LockedIn || !definition.AnchorSet || definition.ActivateAnchor != policy.ActivationLayer {
		return nil, fmt.Errorf("difficulty timing lock-in or anchor is inconsistent")
	}
	// Parent median time, not the proposed timestamp, prevents timestamp
	// backtracking from switching the retarget rule off after activation.
	// The native anchor and notice both gate this testnet-only change. Historical
	// replay therefore keeps its original rules even with a persisted lock-in.
	if parentLayer < policy.ActivationLayer || parentMedianTime < 0 || uint64(parentMedianTime) < policy.NotBeforeTimestamp {
		return base, nil
	}
	adjusted := *base
	adjusted.TargetTimePerBlock = time.Duration(policy.TargetBlockSeconds) * time.Second
	adjusted.TargetTimespan = time.Duration(adjusted.WorkDiffWindowSize) * adjusted.TargetTimePerBlock
	return &adjusted, nil
}
