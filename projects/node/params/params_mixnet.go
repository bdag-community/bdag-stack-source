// Copyright (c) 2014-2016 The btcsuite developers
// Copyright (c) 2015-2017 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package params

import (
	"math/big"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/types"

	"github.com/BlockdagNetworkLabs/bdag/common"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
)

// testMixNetPowLimit is the highest proof of work value a block can
// have for the test network. It is the value 2^224 - 1.
// target 0x0000000000ffffffffffffffffffffffffffffffffffffffffffffffffffffff
var testMixNetPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 242), common.Big1)

// target time per block unit second(s)
const mixTargetTimePerBlock = 1

// The DAA should take the median of 60 blocks, so in order to do that we need 60 window size.
const mixWorkDiffWindowSize = 60

// testPowNetParams defines the network parameters for the test network.
var MixNetParams = Params{
	Name:        "mixnet",
	Net:         protocol.MixNet,
	DefaultPort: "28130",
	Bootstrap:   []string{},

	// Chain parameters
	GenesisBlock:         types.NewBlock(&mixNetGenesisBlock),
	GenesisHash:          &mixNetGenesisHash,
	ReduceMinDifficulty:  false,
	MinDiffReductionTime: 0, // Does not apply since ReduceMinDifficulty false
	// LedgerParams: ledger.LedgerParams{
	// 	GenesisAmountUnit: 1000 * 1e8,                              // 1000 BDAG every utxo
	// 	MaxLockHeight:     86400 / mixTargetTimePerBlock * 365 * 5, // max lock height
	// },
	PowConfig: &pow.PowConfig{
		Blake2bdPowLimit:              testMixNetPowLimit,
		Blake2bdPowLimitBits:          0x2003ffff,
		X16rv3PowLimit:                testMixNetPowLimit,
		X16rv3PowLimitBits:            0x2003ffff,
		X8r16PowLimit:                 testMixNetPowLimit,
		X8r16PowLimitBits:             0x2003ffff,
		BlockdagKeccak256PowLimit:     testMixNetPowLimit,
		BlockdagKeccak256PowLimitBits: 0x2003ffff,
		CryptoNightPowLimit:           testMixNetPowLimit,
		CryptoNightPowLimitBits:       0x2003ffff,
		BdagXKeccakV1PowLimit:         testMixNetPowLimit,
		BdagXKeccakV1PowLimitBits:     0x1f0198f2,
		//hash ffffffffffffffff000000000000000000000000000000000000000000000000 corresponding difficulty is 48 for edge bits 24
		// Uniform field type uint64 value is 48 . bigToCompact the uint32 value
		// 24 edge_bits only need hash 1*4 times use for privnet if GPS is 2. need 50 /2 * 2 ≈ 1min find once
		CuckarooMinDifficulty:  0x1600000, // 96
		CuckatooMinDifficulty:  0x1600000, // 96
		CuckaroomMinDifficulty: 0x1600000, // 96

		Percent: map[pow.MainHeight]pow.PercentItem{
			pow.MainHeight(0): {
				pow.BDAGXKECCAKV1: 100,
			},
		},
		// after this height the big graph will be the main pow graph
		AdjustmentStartMainHeight: 1440 * 15 / mixTargetTimePerBlock,
		DifficultyMode:            pow.DIFFICULTY_MODE_BDAG,
	},

	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       mixWorkDiffWindowSize,
	WorkDiffWindows:          20,
	MaximumBlockSizes:        []int{1310720},
	MaxTxSize:                1000000,
	TargetTimePerBlock:       time.Second * mixTargetTimePerBlock,
	TargetTimespan:           time.Second * mixTargetTimePerBlock * mixWorkDiffWindowSize, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 2,

	UpgradeActivationDelayBlocks: 0,
	P2PProtocol46ActivationLayer: 0,
	// Subsidy parameters.
	BaseSubsidy:              10 * 1e8, // 10 Coin, stay same with testnet
	MulSubsidy:               100,
	DivSubsidy:               10000000000000, // Coin-base reward reduce to zero at 1540677 blocks created
	SubsidyReductionInterval: 1669066,        // 120 * 1669066 (blocks) *= 200287911 (200M) -> 579 ~ 289 days
	WorkRewardProportion:     10,
	StakeRewardProportion:    0,

	BlockTaxProportion: 0,

	StakingActivationEpoch: 0,

	// Checkpoints ordered from oldest to newest.
}
