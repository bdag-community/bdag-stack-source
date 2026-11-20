package model

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
)

type BlockHeader interface {
	Bits() uint32
	Pow() pow.IPow
}
