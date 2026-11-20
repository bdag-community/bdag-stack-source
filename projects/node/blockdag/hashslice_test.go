package Blockdag

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
)

func Test_Has(t *testing.T) {
	t.Parallel()
	hl := HashSlice{}
	hl = append(hl, hash.ZeroHash.ToEVMHashPtr())

	if !hl.Has(hash.ZeroHash.ToEVMHashPtr()) {
		t.FailNow()
	}
}
