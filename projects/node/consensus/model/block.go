package model

import (
	"github.com/ethereum/go-ethereum/common"
)

type Block interface {
	GetID() uint
	GetHash() *common.Hash
	GetState() BlockState
	GetOrder() uint
	HasParents() bool
	GetMainParent() uint
	GetHeight() uint
	GetLayer() uint
}
