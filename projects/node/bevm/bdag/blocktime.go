package bdag

import (
	"math/big"
)

// ResolveEVMBlockTimestamp maps a native DAG block timestamp to the EVM
// header.Time used when building the paired EVM block.
//
// Historical mainnet blocks use the native header timestamp verbatim, including
// occasional equal or decreasing values. Changing this mapping rewrites the EVM
// block hash and was the direct cause of the July 2026 execution-state split.
// The chain ID remains in the signature so every block-construction path shares
// one API and no network can silently introduce a different mapping.
func ResolveEVMBlockTimestamp(parentTime uint64, requestedUnix int64, chainID *big.Int) uint64 {
	_ = chainID
	if requestedUnix <= 0 {
		return parentTime + 1
	}
	return uint64(requestedUnix)
}
