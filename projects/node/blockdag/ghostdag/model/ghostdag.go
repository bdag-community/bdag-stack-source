package model

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// KType defines the size of GHOSTDAG consensus algorithm K parameter.
type KType byte

// BlockGHOSTDAGData represents GHOSTDAG data for some block
type BlockGHOSTDAGData struct {
	blueScore          uint64
	blueWork           *big.Int
	selectedParent     *common.Hash
	mergeSetBlues      []*common.Hash
	mergeSetReds       []*common.Hash
	bluesAnticoneSizes map[common.Hash]KType
}

// BlueScore returns the BlueScore of the block
func (bgd *BlockGHOSTDAGData) BlueScore() uint64 {
	return bgd.blueScore
}

func (bgd *BlockGHOSTDAGData) SetBlueScore(score uint64) {
	bgd.blueScore = score
}

// BlueWork returns the BlueWork of the block
func (bgd *BlockGHOSTDAGData) BlueWork() *big.Int {
	return bgd.blueWork
}

func (bgd *BlockGHOSTDAGData) SetBlueWork(work *big.Int) {
	bgd.blueWork.Set(work)
}

func (bgd *BlockGHOSTDAGData) SetBlueWorkUint64(work uint64) {
	bgd.blueWork.SetUint64(work)
}

func (bgd *BlockGHOSTDAGData) AddBlueWork(x, y *big.Int) {
	bgd.blueWork.Add(x, y)
}

// SelectedParent returns the SelectedParent of the block
func (bgd *BlockGHOSTDAGData) SelectedParent() *common.Hash {
	return bgd.selectedParent
}

func (bgd *BlockGHOSTDAGData) SetSelectedParent(sp *common.Hash) {
	bgd.selectedParent = sp
}

// MergeSetBlues returns the MergeSetBlues of the block (not a copy)
func (bgd *BlockGHOSTDAGData) MergeSetBlues() []*common.Hash {
	return bgd.mergeSetBlues
}

func (bgd *BlockGHOSTDAGData) MergeSetBluesLen() int {
	return len(bgd.mergeSetBlues)
}

func (bgd *BlockGHOSTDAGData) AppendMergeSetBlue(h *common.Hash) {
	bgd.mergeSetBlues = append(bgd.mergeSetBlues, h)
}

// MergeSetReds returns the MergeSetReds of the block (not a copy)
func (bgd *BlockGHOSTDAGData) MergeSetReds() []*common.Hash {
	return bgd.mergeSetReds
}

func (bgd *BlockGHOSTDAGData) AppendMergeSetRed(h *common.Hash) {
	bgd.mergeSetReds = append(bgd.mergeSetReds, h)
}

// BluesAnticoneSizes returns a map between the blocks in its MergeSetBlues and the size of their anticone
func (bgd *BlockGHOSTDAGData) BluesAnticoneSizes() map[common.Hash]KType {
	return bgd.bluesAnticoneSizes
}

func (bgd *BlockGHOSTDAGData) SetBluesAnticoneSize(h *common.Hash, value KType) {
	bgd.bluesAnticoneSizes[*h] = value
}

func (bgd *BlockGHOSTDAGData) Clone() *BlockGHOSTDAGData {
	return NewBlockGHOSTDAGData(bgd.blueScore, bgd.blueWork, bgd.selectedParent, bgd.mergeSetBlues, bgd.mergeSetReds, bgd.bluesAnticoneSizes)
}

// NewBlockGHOSTDAGData creates a new instance of BlockGHOSTDAGData
func NewBlockGHOSTDAGData(
	blueScore uint64,
	blueWork *big.Int,
	selectedParent *common.Hash,
	mergeSetBlues []*common.Hash,
	mergeSetReds []*common.Hash,
	bluesAnticoneSizes map[common.Hash]KType) *BlockGHOSTDAGData {

	return &BlockGHOSTDAGData{
		blueScore:          blueScore,
		blueWork:           blueWork,
		selectedParent:     selectedParent,
		mergeSetBlues:      mergeSetBlues,
		mergeSetReds:       mergeSetReds,
		bluesAnticoneSizes: bluesAnticoneSizes,
	}
}
