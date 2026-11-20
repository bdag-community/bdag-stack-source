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

// devNetPowLimit is the highest proof of work value a block can
// have for the test network.
var devNetPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 242), common.Big1)

// var maxNetPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 0), common.Big1)

// target time per block unit second(s)
const devTargetTimePerBlock = 1

// Difficulty check interval is about 3*60 = 3 mins
const devWorkDiffWindowSize = 60

// DevNetParams defines the network parameters for the devnet network.
var DevNetParams = Params{
	Name:        "devnet",
	Net:         protocol.DevNet,
	DefaultPort: "18150",
	Bootstrap:   []string{},
	// LedgerParams: ledger.LedgerParams{ // lock tx release rule in genesis
	// 	GenesisAmountUnit: 1000 * 1e8, // 1000 BDAG every utxo
	// 	MaxLockHeight:     2880 * 365 * 5,
	// },
	// Chain parameters
	GenesisBlock: types.NewBlock(&testNetGenesisBlock),
	GenesisHash:  &testNetGenesisHash,
	PowConfig: &pow.PowConfig{
		Blake2bdPowLimit:              maxNetPowLimit,
		Blake2bdPowLimitBits:          0x0, // compact from of devNetPowLimit 0
		X16rv3PowLimit:                maxNetPowLimit,
		X16rv3PowLimitBits:            0x0, // compact from of devNetPowLimit 0
		X8r16PowLimit:                 maxNetPowLimit,
		X8r16PowLimitBits:             0x0, // compact from of devNetPowLimit 0
		BlockdagKeccak256PowLimit:     maxNetPowLimit,
		BlockdagKeccak256PowLimitBits: 0x0, // compact from of devNetPowLimit 0
		BdagXKeccakV1PowLimit:         devNetPowLimit,
		BdagXKeccakV1PowLimitBits:     0x1f0198f2, // compact from of devNetPowLimit (2^242-1)

		Blake2sPowLimit:     devNetPowLimit,
		Blake2sPowLimitBits: 0x207fffff,

		ScryptPowLimit:     devNetPowLimit,
		ScryptPowLimitBits: 0x1f0198f2,
		//hash ffffffffffffffff000000000000000000000000000000000000000000000000 corresponding difficulty is 48 for edge bits 24
		// Uniform field type uint64 value is 48 . bigToCompact the uint32 value
		// 24 edge_bits only need hash 1*4 times use for privnet if GPS is 2. need 50 /2 * 4 = 1min find once
		CuckarooMinDifficulty:  0x87fffff, // diff: max int64
		CuckatooMinDifficulty:  0x87fffff, // diff: max int64
		CuckaroomMinDifficulty: 0x87fffff, // diff: max int64

		Percent: map[pow.MainHeight]pow.PercentItem{
			pow.MainHeight(0): {
				// pow.BDAGXKECCAKV1: 100,
				// pow.BLAKE2S: 100,
				pow.SCRYPT: 100,
			},
		},
		// after this height the big graph will be the main pow graph
		AdjustmentStartMainHeight: 365 * 1440 * 60 / devTargetTimePerBlock,
	},
	ReduceMinDifficulty:      true,
	MinDiffReductionTime:     time.Minute * 20, // Does not apply since ReduceMinDifficulty false
	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       devWorkDiffWindowSize,
	WorkDiffWindows:          20,
	MaximumBlockSizes:        []int{1310720},
	MaxTxSize:                1000000,
	TargetTimePerBlock:       time.Second * devTargetTimePerBlock,
	TargetTimespan:           time.Second * devTargetTimePerBlock * devWorkDiffWindowSize, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 2,                                                           // equal to 2 hour vs. 4

	UpgradeActivationDelayBlocks: 0,
	P2PProtocol46ActivationLayer: 0,

	// Subsidy parameters match mainnet for reward testing.
	BaseSubsidy:              260 * 1e8, // POW reward: 260 BDAG per block
	MulSubsidy:               9969,      // subsidy reduces by 0.9969 every block (~1% a year)
	DivSubsidy:               10000,
	SubsidyReductionInterval: 604800,
	WorkRewardProportion:     10,
	StakeRewardProportion:    0,
	BlockTaxProportion:       0,
	StakingActivationEpoch:   3,
	// Devnet accelerates staking epochs without changing the subsidy schedule.
	StakingEpochDurationSeconds: 60,
	// Checkpoints ordered from oldest to newest.
	Checkpoints: []Checkpoint{},
}
