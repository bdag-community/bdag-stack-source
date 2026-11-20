package Blockdag

import (
	"container/list"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type rollbackTestConsensus struct {
	mainTip IBlock
}

func (c *rollbackTestConsensus) GetName() string                          { return "rollback-test" }
func (c *rollbackTestConsensus) Init(*BDAG) bool                          { return true }
func (c *rollbackTestConsensus) AddBlock(IBlock) (*list.List, *list.List) { return nil, nil }
func (c *rollbackTestConsensus) CreateBlock(b *Block) IBlock              { return b }
func (c *rollbackTestConsensus) GetTipsList() []IBlock                    { return nil }
func (c *rollbackTestConsensus) isOnMainChain(uint) bool                  { return false }
func (c *rollbackTestConsensus) GetMainChainTip() IBlock                  { return c.mainTip }
func (c *rollbackTestConsensus) GetMainChainTipId() uint                  { return c.mainTip.GetID() }
func (c *rollbackTestConsensus) GetMainParent(*IdSet) IBlock              { return nil }
func (c *rollbackTestConsensus) Encode(io.Writer) error                   { return nil }
func (c *rollbackTestConsensus) Decode(io.Reader) error                   { return nil }
func (c *rollbackTestConsensus) Load() error                              { return nil }
func (c *rollbackTestConsensus) IsDAG([]IBlock) bool                      { return true }
func (c *rollbackTestConsensus) GetMainParentConcurrency(IBlock) int      { return 0 }
func (c *rollbackTestConsensus) GetBlues(*IdSet) uint                     { return 0 }
func (c *rollbackTestConsensus) IsBlue(uint) bool                         { return false }
func (c *rollbackTestConsensus) getMaxParents() int                       { return 0 }

func TestRollbackSerializesWithBlockLookups(t *testing.T) {
	parent := &Block{
		id:     0,
		hash:   common.HexToHash("0xparent"),
		height: 0,
	}
	candidate := &Block{
		id:         1,
		hash:       common.HexToHash("0xcandidate"),
		height:     1,
		mainParent: parent.GetID(),
	}
	candidate.parents = NewIdSet()
	candidate.parents.AddPair(parent.GetID(), parent)

	mainTip := parent
	bd := &BDAG{
		blocks: map[uint]IBlock{
			parent.GetID():    parent,
			candidate.GetID(): candidate,
		},
		tips:        NewIdSet(),
		commitBlock: NewIdSet(),
		instance:    &rollbackTestConsensus{mainTip: mainTip},
		blockTotal:  2,
		lastSnapshot: func() *DAGSnapshot {
			snap := NewDAGSnapshot()
			snap.block = candidate
			snap.tips = NewIdSet()
			snap.tips.AddPair(parent.GetID(), parent)
			snap.mainChainTip = parent.GetID()
			return snap
		}(),
	}

	const workers = 8
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = bd.GetBlockById(parent.GetID())
					_ = bd.GetBlockById(candidate.GetID())
				}
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	if err := bd.Rollback(); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	close(stop)
	wg.Wait()

	if _, ok := bd.blocks[candidate.GetID()]; ok {
		t.Fatal("rollback should remove candidate block from cache")
	}
}
