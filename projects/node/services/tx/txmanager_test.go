package tx

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
)

func TestTxManagerMemPoolReturnsCanonicalPointer(t *testing.T) {
	pool := &bdag.BdagPool{}
	tm := &TxManager{txMemPool: pool}

	if got := tm.MemPool(); got != pool {
		t.Fatalf("MemPool returned %p, want canonical pool %p", got, pool)
	}
}
