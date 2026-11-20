package forks

import (
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

const (
	TestnetRewardForkTargetTotalSubsidy int64 = 75000000000 * 1e8
	TestnetRewardForkBaseSubsidy        int64 = 2812000000
	TestnetRewardForkMulSubsidy               = 9996146
	TestnetRewardForkDivSubsidy               = 10000000
)

var (
	TestnetRewardForkHeight                   int64 = 1 * params.TestNetParams.SubsidyReductionInterval
	TestnetRewardForkSubsidyReductionInterval       = params.TestNetParams.SubsidyReductionInterval
)

func IsTestnetRewardForkHeight(height int64) bool {
	if params.ActiveNetParams.Net != protocol.TestNet {
		return false
	}
	return height >= TestnetRewardForkHeight
}

func TestnetRewardForkIteration(height int64) uint64 {
	if height < TestnetRewardForkHeight {
		return 0
	}
	realHeight := height - TestnetRewardForkHeight
	if realHeight < 0 {
		realHeight = 0
	}
	return uint64(realHeight) / uint64(TestnetRewardForkSubsidyReductionInterval)
}
