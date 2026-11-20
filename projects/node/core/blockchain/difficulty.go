// Copyright (c) 2013-2016 The btcsuite developers
// Copyright (c) 2015-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"
	"math/big"
	"time"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow/difficultymanager"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

// CalcEasiestDifficulty calculates the easiest possible difficulty that a block
// can have given starting difficulty bits and a duration.  It is mainly used to
// verify that claimed proof of work by a block is sane as compared to a
// known good checkpoint.
func (m *BlockChain) calcEasiestDifficulty(bits uint32, duration time.Duration, powInstance pow.IPow) uint32 {
	return m.difficultyManager.CalcEasiestDifficulty(bits, duration, powInstance)
}

func (m *BlockChain) calcNextRequiredDifficulty(block model.Block, newBlockTime time.Time, powInstance pow.IPow) (uint32, error) {
	manager := m.difficultyManager
	if block != nil && m.params.Net == params.TestNetParams.Net && evmparams.BdagTestnetTimingFork != nil {
		parent := m.bd.GetBlockById(block.GetID())
		if parent == nil || m.GetBlockHeader(parent) == nil {
			return 0, fmt.Errorf("timing difficulty requires the selected parent header")
		}
		adjusted, err := testnetTimingDifficultyParams(m.params, evmparams.BdagTestnetTimingFork,
			upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name], uint64(block.GetLayer()), m.CalcPastMedianTime(parent).Unix())
		if err != nil {
			return 0, err
		}
		if adjusted != m.params {
			manager = difficultymanager.NewDiffManager(m.Consensus(), adjusted)
		}
	}
	return manager.RequiredDifficulty(block, newBlockTime, powInstance)
}

// CalcNextRequiredDifficulty calculates the required difficulty for the block
// after the end of the current best chain based on the difficulty retarget
// rules.
//
// This function is safe for concurrent access.
func (b *BlockChain) CalcNextRequiredDifficulty(timestamp time.Time, powType pow.PowType) (uint32, error) {
	b.ChainRLock()
	block := b.bd.GetMainChainTip()
	instance := pow.GetInstance(powType, 0, 0, []byte{})
	instance.SetParams(b.params.PowConfig)
	instance.SetMainHeight(pow.MainHeight(block.GetHeight() + 1))
	difficulty, err := b.calcNextRequiredDifficulty(block, timestamp, instance)
	b.ChainRUnlock()
	return difficulty, err
}

// find block node by pow type
func (b *BlockChain) GetCurrentPowDiff(ib model.Block, powType pow.PowType) *big.Int {
	return b.difficultyManager.GetCurrentPowDiff(ib, powType)
}
