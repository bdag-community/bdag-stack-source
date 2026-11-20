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

// privNetPowLimit is the highest proof of work value a block can
// have for the private test network. It is the value 2^255 - 1.
var privNetPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 255), common.Big1)

// target time per block unit second(s)
const privTargetTimePerBlock = 1

// PirvNetParams defines the network parameters for the private test network.
// This network is similar to the normal test network except it is
// intended for private use within a group of individuals doing simulation
// testing.  The functionality is intended to differ in that the only nodes
// which are specifically specified are used to create the network rather than
// following normal discovery rules.  This is important as otherwise it would
// just turn into another public testnet.
var PrivNetParams = Params{
	Name:        "privnet",
	Net:         protocol.PrivNet,
	DefaultPort: "38130",
	Bootstrap:   []string{},

	// Chain parameters
	GenesisBlock: types.NewBlock(&privNetGenesisBlock),
	GenesisHash:  &privNetGenesisHash,
	// LedgerParams: ledger.LedgerParams{
	// 	GenesisAmountUnit: 1000 * 1e8,
	// 	MaxLockHeight:     10 * 365 * 5,
	// },
	PowConfig: &pow.PowConfig{
		Blake2bdPowLimit:              privNetPowLimit,
		Blake2bdPowLimitBits:          0x207fffff,
		X8r16PowLimit:                 privNetPowLimit,
		X8r16PowLimitBits:             0x207fffff,
		X16rv3PowLimit:                privNetPowLimit,
		X16rv3PowLimitBits:            0x207fffff,
		BlockdagKeccak256PowLimit:     privNetPowLimit,
		BlockdagKeccak256PowLimitBits: 0x207fffff,
		BdagXKeccakV1PowLimit:         privNetPowLimit,
		BdagXKeccakV1PowLimitBits:     0x207fffff,

		Blake2sPowLimit:     privNetPowLimit,
		Blake2sPowLimitBits: 0x1C003000,

		ScryptPowLimit:     privNetPowLimit,
		ScryptPowLimitBits: 0x207fffff, // 0x207fffff
		//hash ffffffffffffffff000000000000000000000000000000000000000000000000 corresponding difficulty is 48 for edge bits 24
		// Uniform field type uint64 value is 48 . bigToCompact the uint32 value
		// 24 edge_bits only need hash 1 times use for privnet if GPS is 2. need 50 /2 = 25s find once
		CuckarooMinDifficulty:  0x1300000,
		CuckatooMinDifficulty:  0x1300000,
		CuckaroomMinDifficulty: 0x1300000,

		Percent: map[pow.MainHeight]pow.PercentItem{
			pow.MainHeight(0): {
				pow.BLAKE2BD:      0,
				pow.CUCKAROO:      0,
				pow.CUCKATOO:      0,
				pow.CUCKAROOM:     0,
				pow.X16RV3:        0,
				pow.X8R16:         0,
				pow.BDAGXKECCAKV1: 0,
				pow.BLAKE2S:       0,
				pow.SCRYPT:        100,
			},
			pow.MainHeight(50): {
				pow.BLAKE2BD:          0,
				pow.CUCKAROO:          0,
				pow.CUCKATOO:          0,
				pow.CUCKAROOM:         0,
				pow.X16RV3:            0,
				pow.X8R16:             0,
				pow.BlockdagKECCAK256: 0,
				pow.BDAGXKECCAKV1:     0,
				pow.BLAKE2S:           0,
				pow.SCRYPT:            100,
			},
			pow.MainHeight(100): {
				pow.BLAKE2BD:      0,
				pow.CUCKAROO:      0,
				pow.CUCKATOO:      0,
				pow.CUCKAROOM:     0,
				pow.X16RV3:        0,
				pow.X8R16:         0,
				pow.BDAGXKECCAKV1: 0,
				pow.BLAKE2S:       0,
				pow.SCRYPT:        100,
			},
		},
		// after this height the big graph will be the main pow graph
		AdjustmentStartMainHeight: 45 * 1440 * 60 / privTargetTimePerBlock,
	},
	ReduceMinDifficulty:      false,
	MinDiffReductionTime:     0, // Does not apply since ReduceMinDifficulty false
	MaximumBlockSizes:        []int{1000000, 1310720},
	MaxTxSize:                1000000,
	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       1600,
	WorkDiffWindows:          20,
	TargetTimePerBlock:       time.Second * privTargetTimePerBlock,
	TargetTimespan:           time.Second * privTargetTimePerBlock * 1600, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 2,

	UpgradeActivationDelayBlocks: 0,
	P2PProtocol46ActivationLayer: 0,

	// Subsidy parameters.
	BaseSubsidy:              120000000000,
	MulSubsidy:               100,
	DivSubsidy:               200,
	SubsidyReductionInterval: 1000,
	WorkRewardProportion:     10,
	StakeRewardProportion:    0,

	BlockTaxProportion: 0,

	StakingActivationEpoch: 0,

	// Checkpoints ordered from oldest to newest.
	Checkpoints: []Checkpoint{},
}
