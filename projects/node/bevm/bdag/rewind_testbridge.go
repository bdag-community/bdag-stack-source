//go:build bdag_recovery_test

package bdag

import (
	"fmt"
	"sync"
)

// TestEVMRewindChain is the existing narrow rewind operation surface. It is
// exported only from this explicitly tagged test bridge.
type TestEVMRewindChain = evmRewindChain

var testEVMRewindProviderMu sync.Mutex

// BindTestEVMRewindChain installs a real test chain for one exact BdagChain
// pointer and returns an idempotent restore function. Other targets continue
// to use the previous provider, so the bridge cannot grant shared authority.
func BindTestEVMRewindChain(target *BdagChain, backend TestEVMRewindChain) func() {
	if target == nil {
		panic(fmt.Sprintf("nil EVM rewind test target"))
	}
	if backend == nil {
		panic(fmt.Sprintf("nil EVM rewind test backend"))
	}
	testEVMRewindProviderMu.Lock()
	previous := evmRewindChainProvider
	evmRewindChainProvider = func(candidate *BdagChain) evmRewindChain {
		if candidate == target {
			return backend
		}
		return previous(candidate)
	}
	var restoreOnce sync.Once
	return func() {
		restoreOnce.Do(func() {
			evmRewindChainProvider = previous
			testEVMRewindProviderMu.Unlock()
		})
	}
}

// IsTestUnsafeEVMRewindError exposes only the existing typed classifier to
// tagged caller tests; it does not expose rewind authority or implementation.
func IsTestUnsafeEVMRewindError(err error) bool { return isUnsafeEVMRewindError(err) }
