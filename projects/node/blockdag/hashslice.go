package Blockdag

import (
	"github.com/ethereum/go-ethereum/common"
)

// HashSlice is used to sort hash list
type HashSlice []*common.Hash

func (sh HashSlice) Len() int {
	return len(sh)
}

func (sh HashSlice) Less(i, j int) bool {
	return sh[i].String() < sh[j].String()
}

func (sh HashSlice) Swap(i, j int) {
	sh[i], sh[j] = sh[j], sh[i]
}

func (sh HashSlice) Has(h *common.Hash) bool {
	for _, v := range sh {
		if v.Cmp(*h) == 0 {
			return true
		}
	}
	return false
}
