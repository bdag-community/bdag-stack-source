package blockchain

import (
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model/bdag"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

type BlockNode struct {
	block     *types.SerializedBlock
	bdagBlock *bdag.Block // Used for bdag chain
}

// return the block node hash.
func (node *BlockNode) GetHash() *ethcommon.Hash {
	return node.block.Hash()
}

// Include all parents for set
func (node *BlockNode) GetParents() []*ethcommon.Hash {
	return node.block.Block().Parents
}

// return the timestamp of node
func (node *BlockNode) GetTimestamp() int64 {
	return node.block.Block().Header.Timestamp.Unix()
}

func (node *BlockNode) GetHeader() *types.BlockHeader {
	return &node.block.Block().Header
}

func (node *BlockNode) GetBody() *types.SerializedBlock {
	return node.block
}

func (node *BlockNode) Difficulty() uint32 {
	return node.GetHeader().Difficulty
}

func (node *BlockNode) Pow() pow.IPow {
	return node.GetHeader().Pow
}

func (node *BlockNode) GetPowType() pow.PowType {
	return node.Pow().GetPowType()
}

func (node *BlockNode) Timestamp() time.Time {
	return node.GetHeader().Timestamp
}

func (node *BlockNode) GetPriority() int {
	if len(node.block.Block().Transactions) == 0 {
		return 1
	}
	return len(node.block.Block().Transactions)
}

func (node *BlockNode) GetMainParent() *ethcommon.Hash {
	return node.block.Block().Parents[0]
}

func (node *BlockNode) GetbdagBlock() *bdag.Block {
	return node.bdagBlock
}

func (node *BlockNode) SetbdagBlock(block *bdag.Block) {
	node.bdagBlock = block
}

func NewBlockNode(block *types.SerializedBlock) *BlockNode {
	bn := BlockNode{
		block: block,
	}
	return &bn
}
