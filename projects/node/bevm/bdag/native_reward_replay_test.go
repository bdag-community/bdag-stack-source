package bdag

import (
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	nativestate "github.com/BlockdagNetworkLabs/bdag/core/state"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

type nativeRewardTestBlock struct {
	id, order uint
	hash      common.Hash
	state     *nativestate.BlockState
}

func (b *nativeRewardTestBlock) GetID() uint                { return b.id }
func (b *nativeRewardTestBlock) GetHash() *common.Hash      { return &b.hash }
func (b *nativeRewardTestBlock) GetState() model.BlockState { return b.state }
func (b *nativeRewardTestBlock) GetOrder() uint             { return b.order }
func (*nativeRewardTestBlock) HasParents() bool             { return true }
func (*nativeRewardTestBlock) GetMainParent() uint          { return 0 }
func (*nativeRewardTestBlock) GetHeight() uint              { return 1 }
func (*nativeRewardTestBlock) GetLayer() uint               { return 1 }

type nativeRewardTestChain struct {
	mu                sync.RWMutex
	current, previous *nativeRewardTestBlock
	header            *native.BlockHeader
	observations      int
	changeAfterFirst  bool
}

func (c *nativeRewardTestChain) ChainRLock() { c.mu.RLock() }
func (c *nativeRewardTestChain) ChainRUnlock() {
	c.mu.RUnlock()
	c.observations++
	if c.changeAfterFirst && c.observations == 1 {
		c.current.state.Invalid()
	}
}
func (c *nativeRewardTestChain) GetBlockOrderByHash(hash *common.Hash) (uint, error) {
	if hash == nil || *hash != c.current.hash {
		return 0, errors.New("unknown hash")
	}
	return c.current.order, nil
}
func (c *nativeRewardTestChain) GetBlockByOrder(order uint64) model.Block {
	switch order {
	case uint64(c.current.order):
		return c.current
	case uint64(c.previous.order):
		return c.previous
	default:
		return nil
	}
}
func (c *nativeRewardTestChain) GetBlockHeader(block model.Block) *native.BlockHeader {
	if block.GetID() != c.current.id {
		return nil
	}
	return c.header
}

func newNativeRewardFixture(t *testing.T) (*nativeRewardTestChain, *core.BlockChain, *types.Block, common.Address) {
	t.Helper()
	evm, block, wallet := newRewardReplayFixture(t, 0, 0)
	parent := evm.GetBlockByHash(block.ParentHash())
	require.NotNil(t, parent)
	previousState := nativestate.NewBlockState(7)
	previousState.SetOrder(10)
	previousState.SetEVM(parent.Header())
	currentState := nativestate.NewBlockState(8)
	currentState.SetOrder(11)
	currentState.SetEVM(block.Header())
	header := &native.BlockHeader{Timestamp: time.Unix(int64(block.Time()), 0), CoinbaseAddress: wallet, Reward: 1000, Pow: pow.GetInstance(pow.SCRYPT, 0, 0, nil)}
	current := &nativeRewardTestBlock{id: 8, order: 11, hash: header.BlockHash(), state: currentState}
	previous := &nativeRewardTestBlock{id: 7, order: 10, hash: common.HexToHash("0x1234"), state: previousState}
	return &nativeRewardTestChain{current: current, previous: previous, header: header}, evm, block, wallet
}

func TestNativeRewardReplayBindsFullNativeBlock(t *testing.T) {
	nativeChain, evm, block, wallet := newNativeRewardFixture(t)
	evidence, err := replayNativeReward(nativeChain, evm, nativeChain.current.hash, wallet)
	require.NoError(t, err)
	require.Equal(t, nativeChain.current.hash, evidence.Native.BlockHash)
	require.EqualValues(t, 11, evidence.Native.Order)
	require.Equal(t, block.Hash(), evidence.EVM.BlockHash)
	require.Equal(t, big.NewInt(7_000_000_000_000), evidence.EVM.SubsidyWei)
	require.EqualValues(t, 2, nativeChain.observations)
}

func TestNativeRewardReplayRejectsAmbiguousAttribution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*nativeRewardTestChain, *core.BlockChain, *types.Block, common.Address)
	}{
		{"invalid native block", func(c *nativeRewardTestChain, _ *core.BlockChain, _ *types.Block, _ common.Address) {
			c.current.state.Invalid()
		}},
		{"native order changed", func(c *nativeRewardTestChain, _ *core.BlockChain, _ *types.Block, _ common.Address) {
			c.current.state.SetOrder(12)
		}},
		{"no EVM advance", func(c *nativeRewardTestChain, evm *core.BlockChain, b *types.Block, _ common.Address) {
			c.current.state.SetEVM(evm.GetBlockByHash(b.ParentHash()).Header())
		}},
		{"wrong EVM root", func(c *nativeRewardTestChain, _ *core.BlockChain, b *types.Block, _ common.Address) {
			h := b.Header()
			h.Root = common.HexToHash("0x88")
			c.current.state.SetEVM(h)
		}},
		{"wrong previous root", func(c *nativeRewardTestChain, evm *core.BlockChain, b *types.Block, _ common.Address) {
			h := evm.GetBlockByHash(b.ParentHash()).Header()
			h.Root = common.HexToHash("0x99")
			c.previous.state.SetEVM(h)
		}},
		{"wrong native wallet", func(c *nativeRewardTestChain, _ *core.BlockChain, _ *types.Block, _ common.Address) {
			c.header.CoinbaseAddress = common.HexToAddress("0x1234")
			c.current.hash = c.header.BlockHash()
		}},
		{"wrong native reward", func(c *nativeRewardTestChain, _ *core.BlockChain, _ *types.Block, _ common.Address) {
			c.header.Reward++
			c.current.hash = c.header.BlockHash()
		}},
		{"wrong native time", func(c *nativeRewardTestChain, _ *core.BlockChain, _ *types.Block, _ common.Address) {
			c.header.Timestamp = c.header.Timestamp.Add(time.Second)
			c.current.hash = c.header.BlockHash()
		}},
		{"native changed during replay", func(c *nativeRewardTestChain, _ *core.BlockChain, _ *types.Block, _ common.Address) {
			c.changeAfterFirst = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain, evm, block, wallet := newNativeRewardFixture(t)
			tc.mutate(chain, evm, block, wallet)
			evidence, err := replayNativeReward(chain, evm, chain.current.hash, wallet)
			require.Error(t, err)
			require.Nil(t, evidence)
		})
	}
}
