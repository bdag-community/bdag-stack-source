package Blockdag

import (
	"container/list"
	"io"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

type cacheTestConsensus struct {
	mainTip IBlock
}

func (c *cacheTestConsensus) GetName() string                          { return "cache-test" }
func (c *cacheTestConsensus) Init(*BDAG) bool                          { return true }
func (c *cacheTestConsensus) AddBlock(IBlock) (*list.List, *list.List) { return nil, nil }
func (c *cacheTestConsensus) CreateBlock(b *Block) IBlock              { return b }
func (c *cacheTestConsensus) GetTipsList() []IBlock                    { return nil }
func (c *cacheTestConsensus) isOnMainChain(uint) bool                  { return false }
func (c *cacheTestConsensus) GetMainChainTip() IBlock                  { return c.mainTip }
func (c *cacheTestConsensus) GetMainChainTipId() uint                  { return c.mainTip.GetID() }
func (c *cacheTestConsensus) GetMainParent(*IdSet) IBlock              { return nil }
func (c *cacheTestConsensus) Encode(io.Writer) error                   { return nil }
func (c *cacheTestConsensus) Decode(io.Reader) error                   { return nil }
func (c *cacheTestConsensus) Load() error                              { return nil }
func (c *cacheTestConsensus) IsDAG([]IBlock) bool                      { return true }
func (c *cacheTestConsensus) GetMainParentConcurrency(IBlock) int      { return 0 }
func (c *cacheTestConsensus) GetBlues(*IdSet) uint                     { return 0 }
func (c *cacheTestConsensus) IsBlue(uint) bool                         { return false }
func (c *cacheTestConsensus) getMaxParents() int                       { return 0 }

func TestUpdateBlockCacheDoesNotSpendBudgetOnIneligibleBlocks(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		mainTip := &Block{id: 1000, height: 1000, hash: common.HexToHash("0x1000")}
		eligible := &Block{id: 1, height: 1, hash: common.HexToHash("0x1")}
		bd := &BDAG{
			blocks:       map[uint]IBlock{eligible.GetID(): eligible},
			tips:         NewIdSet(),
			commitBlock:  NewIdSet(),
			instance:     &cacheTestConsensus{mainTip: mainTip},
			minCacheSize: 64,
		}
		for id := uint(2); id <= 65; id++ {
			bd.blocks[id] = &Block{
				id:     id,
				height: 990,
				hash:   common.BigToHash(new(big.Int).SetUint64(uint64(id))),
			}
		}

		bd.updateBlockCache()

		if _, ok := bd.blocks[eligible.GetID()]; ok {
			t.Fatal("eligible block was not evicted")
		}
	}
}
