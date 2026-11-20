package Blockdag

import (
	"github.com/ethereum/go-ethereum/common"
)

type ISpectre interface {
	Vote(x IBlock, y IBlock) int
}

type SpectreBlock struct {
	hash           common.Hash
	Votes1, Votes2 int // votes in future set, -1 means not voted yet
}

func (sb *SpectreBlock) GetHash() *common.Hash {
	return &sb.hash
}

type SpectreBlockData struct {
	hash      common.Hash
	parents   []*common.Hash
	timestamp int64
}

func (sd *SpectreBlockData) GetHash() *common.Hash {
	return &sd.hash
}

func (sd *SpectreBlockData) GetParents() []*common.Hash {
	return sd.parents
}

func (sd *SpectreBlockData) GetMainParent() *common.Hash {
	return sd.parents[0]
}

func (sd *SpectreBlockData) GetTimestamp() int64 {
	return sd.timestamp
}

// Acquire the weight of block
func (sd *SpectreBlockData) GetWeight() uint64 {
	return 1
}

func (sd *SpectreBlockData) GetPriority() int {
	return 1
}
