// Copyright (c) 2014-2016 The btcsuite developers
// Copyright (c) 2015-2017 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package params

import (
	"math/big"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
)

var mainPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 242), common.Big1)

// target time per block unit second(s)
const mainTargetTimePerBlock = 1

const (
	// Temporarily disabled while the fleet rolls through the EVM anchoring
	// transition. A zero value leaves the guard inactive.
	mainEVMStateCommitmentStartOrder = 0

	// Approximately 2026-07-21 23:32 SAST / 21:32 UTC, two weeks out from
	// 2026-07-07 and computed using one target layer per second.
	mainP2PProtocol46ActivationLayer = 11_926_166
)

// Difficulty check interval is about 60*30 = 30 mins
const mainWorkDiffWindowSize = 60

// MainNetParams defines the network parameters for the main network.
var MainNetParams = Params{
	Name:        "mainnet",
	Net:         protocol.MainNet,
	DefaultPort: "8150",
	Bootstrap:   []string{},
	// LedgerParams: ledger.LedgerParams{
	// 	GenesisAmountUnit: 1000 * 1e8,                               // 1000 BDAG every utxo
	// 	MaxLockHeight:     86400 / mainTargetTimePerBlock * 365 * 5, // max lock height
	// },
	// Chain parameters
	GenesisBlock: types.NewBlock(&genesisBlock),
	GenesisHash:  &genesisHash,
	PowConfig: &pow.PowConfig{
		Blake2bdPowLimit:              mainPowLimit,
		Blake2bdPowLimitBits:          0x1f0198f2,
		X16rv3PowLimit:                mainPowLimit,
		X16rv3PowLimitBits:            0x1f0198f2,
		X8r16PowLimit:                 mainPowLimit,
		X8r16PowLimitBits:             0x1f0198f2,
		BlockdagKeccak256PowLimit:     mainPowLimit,
		BlockdagKeccak256PowLimitBits: 0x1f0198f2,
		//hash ffffffffffffffff000000000000000000000000000000000000000000000000 corresponding difficulty is 48 for edge bits 24
		// Uniform field type uint64 value is 48 . bigToCompact the uint32 value
		// 24 edge_bits only need hash 1*4 times use for privnet if GPS is 2. need 50 /2 * 4 find once
		CuckarooMinDifficulty:     0x1300000 * 4,
		CuckaroomMinDifficulty:    0x1300000 * 4,
		CuckatooMinDifficulty:     0x1300000 * 4,
		BdagXKeccakV1PowLimit:     mainPowLimit,
		BdagXKeccakV1PowLimitBits: 0x1f0198f2,
		ScryptPowLimit:            mainPowLimit,
		ScryptPowLimitBits:        0x1f0198f2,
		Percent: map[pow.MainHeight]pow.PercentItem{
			pow.MainHeight(0): {
				pow.SCRYPT: 100,
			},
		},
		// after this height the big graph will be the main pow graph
		AdjustmentStartMainHeight: 45 * 1440 * 60 / mainTargetTimePerBlock,
	},
	ReduceMinDifficulty:      false,
	MinDiffReductionTime:     0, // Does not apply since ReduceMinDifficulty false
	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       mainWorkDiffWindowSize,
	WorkDiffWindows:          20, //
	MaximumBlockSizes:        []int{1310720},
	MaxTxSize:                1000000,
	TargetTimePerBlock:       time.Second * mainTargetTimePerBlock,
	TargetTimespan:           time.Second * mainTargetTimePerBlock * mainWorkDiffWindowSize, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 2,

	UpgradeActivationDelayBlocks: 0,
	EVMStateCommitmentStartOrder: mainEVMStateCommitmentStartOrder,
	P2PProtocol46ActivationLayer: mainP2PProtocol46ActivationLayer,

	// Subsidy parameters.
	BaseSubsidy:                 260 * 1e8, // POW reward: 260 BDAG per block
	MulSubsidy:                  9969,      // subsidy reduces by 0.9969 every block (~1% a year)
	DivSubsidy:                  10000,
	SubsidyReductionInterval:    604800,
	StakingEpochDurationSeconds: 604800,
	WorkRewardProportion:        10,
	StakeRewardProportion:       0,

	BlockTaxProportion: 0,

	StakingActivationEpoch: 3,

	// Checkpoints ordered from oldest to newest.
}
