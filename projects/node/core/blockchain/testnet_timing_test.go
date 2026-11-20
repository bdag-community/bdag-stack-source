package blockchain

import (
	"math/big"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow/difficultymanager"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

type timingDifficultyBlock struct {
	model.Block
	id   uint
	hash common.Hash
}

func (b timingDifficultyBlock) GetID() uint           { return b.id }
func (b timingDifficultyBlock) GetHash() *common.Hash { return &b.hash }
func (b timingDifficultyBlock) GetOrder() uint        { return b.id }
func (b timingDifficultyBlock) GetHeight() uint       { return b.id }
func (b timingDifficultyBlock) GetLayer() uint        { return b.id }
func (b timingDifficultyBlock) HasParents() bool      { return b.id > 0 }
func (b timingDifficultyBlock) GetMainParent() uint   { return b.id - 1 }

type timingDifficultyChain struct {
	model.BlockChain
	headers []*types.BlockHeader
	blocks  []model.Block
}

func (b *timingDifficultyChain) GetBlockHeader(block model.Block) *types.BlockHeader {
	return b.headers[block.GetID()]
}
func (b *timingDifficultyChain) GetBlockById(id uint) model.Block {
	if id >= uint(len(b.blocks)) {
		return nil
	}
	return b.blocks[id]
}

type timingDifficultyConsensus struct {
	model.Consensus
	chain *timingDifficultyChain
}

func (c timingDifficultyConsensus) BlockChain() model.BlockChain { return c.chain }

func TestTimingDifficultyRetargetsWithoutChangingHistoricalTargets(t *testing.T) {
	base := params.TestNetParams
	changed := base
	changed.TargetTimePerBlock = 2 * time.Second
	changed.TargetTimespan = 120 * time.Second
	chain := &timingDifficultyChain{}
	const bits uint32 = 0x1e010000
	for i := uint(0); i <= 1260; i++ {
		p := pow.GetInstance(pow.SCRYPT, 0, 0, []byte{})
		p.SetParams(base.PowConfig)
		p.SetMainHeight(pow.MainHeight(i))
		chain.headers = append(chain.headers, &types.BlockHeader{Timestamp: time.Unix(100000+int64(i), 0), Difficulty: bits, Pow: p})
		chain.blocks = append(chain.blocks, timingDifficultyBlock{id: i, hash: common.BigToHash(new(big.Int).SetUint64(uint64(i) + 1))})
	}
	con := timingDifficultyConsensus{chain: chain}
	old := difficultymanager.NewDiffManager(con, &base)
	next := difficultymanager.NewDiffManager(con, &changed)
	last := chain.blocks[len(chain.blocks)-1]
	p := chain.headers[len(chain.headers)-1].Pow
	oldBits, err := old.RequiredDifficulty(last, time.Unix(101261, 0), p)
	require.NoError(t, err)
	newBits, err := next.RequiredDifficulty(last, time.Unix(101261, 0), p)
	require.NoError(t, err)
	require.Equal(t, bits, oldBits)
	require.Equal(t, pow.BigToCompact(new(big.Int).Rsh(pow.CompactToBig(bits), 1)), newBits, "two-second target requires twice the work on a one-second history")
	backward, err := next.RequiredDifficulty(last, time.Unix(101259, 0), p)
	require.NoError(t, err)
	require.Equal(t, newBits, backward)
	again, err := old.RequiredDifficulty(last, time.Unix(101261, 0), p)
	require.NoError(t, err)
	require.Equal(t, oldBits, again)
	// This network has no native PoW checkpoints. Do not claim qualification of
	// the inherited CalcEasiestDifficulty checkpoint path: its zero-share input
	// panics for Scrypt and must be repaired/reviewed before adding checkpoints.
	// EVM state-root checkpoint validation is separate and remains enforced.
	require.Empty(t, base.Checkpoints)
}

func TestTimingDifficultyIsHistoryAndNoticeGated(t *testing.T) {
	p := &ethparams.BDAGTestnetTimingForkConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"), SignalStartLayer: 1000, ActivationLayer: 4999, ReleaseDistributedAt: 100000, NoticePublishedAt: 100000, NotBeforeTimestamp: 186400, EpochSeconds: 1800, TargetBlockSeconds: 2}
	d := &upgrades.UpgradeDefinition{Name: upgrades.TestnetTimingV1Name, BitMask: upgrades.TestnetTimingV1BitMask, StartLayer: 1000, TimeoutLayer: 3000, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000, LockedIn: true, AnchorSet: true, ActivateAnchor: 4999}
	base := params.TestNetParams
	for _, name := range []string{"unscheduled", "no result", "no lockin", "before layer", "before notice", "mainnet", "at boundary", "after boundary", "bad anchor", "bad definition", "bad policy"} {
		t.Run(name, func(t *testing.T) {
			b, policy, definition := base, *p, *d
			pp, dd := &policy, &definition
			layer, median := uint64(4999), int64(186400)
			switch name {
			case "unscheduled":
				pp = nil
			case "no result":
				dd = nil
			case "no lockin":
				definition.LockedIn = false
				definition.AnchorSet = false
				definition.ActivateAnchor = 0
			case "before layer":
				layer--
			case "before notice":
				median--
			case "mainnet":
				b = params.MainNetParams
			case "after boundary":
				layer += 10000
				median += 10000
			case "bad anchor":
				definition.ActivateAnchor++
			case "bad definition":
				definition.Threshold--
			case "bad policy":
				policy.TargetBlockSeconds = 1
			}
			got, err := testnetTimingDifficultyParams(&b, pp, dd, layer, median)
			if name == "bad anchor" || name == "bad definition" || name == "bad policy" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if name == "at boundary" || name == "after boundary" {
				require.NotSame(t, &b, got)
				require.Equal(t, 2*time.Second, got.TargetTimePerBlock)
				require.Equal(t, 120*time.Second, got.TargetTimespan)
				require.Equal(t, b.MinDiffReductionTime, got.MinDiffReductionTime)
				require.Equal(t, time.Second, b.TargetTimePerBlock, "historical configuration must not be mutated")
			} else {
				require.Same(t, &b, got)
			}
		})
	}
}
