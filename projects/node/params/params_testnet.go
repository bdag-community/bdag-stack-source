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

// testNetPowLimit is the highest proof of work value a block can
// have for the test network.
var testNetPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 242), common.Big1)

var maxNetPowLimit = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 0), common.Big1)

// target time per block unit second(s)
const testTargetTimePerBlock = 1

// Difficulty check interval is about 3*60 = 3 mins
const testWorkDiffWindowSize = 60

// TestNetParams defines the network parameters for the test network.
var TestNetParams = Params{
	Name:        "testnet",
	Net:         protocol.TestNet,
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
		Blake2bdPowLimitBits:          0x0, // compact from of testNetPowLimit 0
		X16rv3PowLimit:                maxNetPowLimit,
		X16rv3PowLimitBits:            0x0, // compact from of testNetPowLimit 0
		X8r16PowLimit:                 maxNetPowLimit,
		X8r16PowLimitBits:             0x0, // compact from of testNetPowLimit 0
		BlockdagKeccak256PowLimit:     maxNetPowLimit,
		BlockdagKeccak256PowLimitBits: 0x0, // compact from of testNetPowLimit 0
		BdagXKeccakV1PowLimit:         testNetPowLimit,
		BdagXKeccakV1PowLimitBits:     0x1f0198f2, // compact from of testNetPowLimit (2^242-1)

		Blake2sPowLimit:     testNetPowLimit,
		Blake2sPowLimitBits: 0x207fffff,

		ScryptPowLimit:     testNetPowLimit,
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
		AdjustmentStartMainHeight: 365 * 1440 * 60 / testTargetTimePerBlock,
	},
	ReduceMinDifficulty:      true,
	MinDiffReductionTime:     time.Minute * 20, // Does not apply since ReduceMinDifficulty false
	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       testWorkDiffWindowSize,
	WorkDiffWindows:          20,
	MaximumBlockSizes:        []int{1310720},
	MaxTxSize:                1000000,
	TargetTimePerBlock:       time.Second * testTargetTimePerBlock,
	TargetTimespan:           time.Second * testTargetTimePerBlock * testWorkDiffWindowSize, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 2,                                                             // equal to 2 hour vs. 4

	UpgradeActivationDelayBlocks: 0,
	P2PProtocol46ActivationLayer: 0,

	// Subsidy parameters.
	// Monthly decay schedule (mapped to per-block with 1s block time)
	BaseSubsidy:              140 * 1e8, // atomic units: ~9.70000000 BDAG per block (week 1)
	MulSubsidy:               9958,      // multiplier numerator -> 9958/10000 = 0.9958 (week factor = 1 - k)
	DivSubsidy:               10000,     // multiplier denominator
	SubsidyReductionInterval: 1030000,   // ~12 months at 1s block time (365*1440*60/1)
	WorkRewardProportion:     100,       // percentage share to miners (adjust if needed)
	StakeRewardProportion:    0,

	BlockTaxProportion: 0,

	StakingActivationEpoch: 0,
	// Preserve RC2's block-based financial clock for historical tallies and
	// operator locks. Post-bootstrap reward voting has its own 60-second clock.

	// Checkpoints ordered from oldest to newest.
}
