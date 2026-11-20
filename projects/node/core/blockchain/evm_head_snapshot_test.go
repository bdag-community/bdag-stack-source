package blockchain

import (
	"sync"
	"testing"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestEVMMutationLockGeneration(t *testing.T) {
	var lock evmMutationLock
	if got := lock.Generation(); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}
	lock.Lock()
	if got := lock.Generation(); got != 1 {
		t.Fatalf("locked generation = %d, want 1", got)
	}
	lock.Unlock()
	if got := lock.Generation(); got != 2 {
		t.Fatalf("unlocked generation = %d, want 2", got)
	}
	if !lock.TryLock() {
		t.Fatal("TryLock() = false, want true")
	}
	if got := lock.Generation(); got != 3 {
		t.Fatalf("try-locked generation = %d, want 3", got)
	}
	if lock.TryLock() {
		t.Fatal("nested TryLock() = true, want false")
	}
	if got := lock.Generation(); got != 3 {
		t.Fatalf("failed TryLock generation = %d, want 3", got)
	}
	lock.Unlock()
	if got := lock.Generation(); got != 4 {
		t.Fatalf("final generation = %d, want 4", got)
	}
}

func TestCommittedEVMHeadPublishesCoherentSnapshots(t *testing.T) {
	first := &committedNativeEVMHead{
		hash:   ethcommon.HexToHash("0x11"),
		number: 11,
		root:   ethcommon.HexToHash("0x1111"),
		valid:  true,
	}
	second := &committedNativeEVMHead{
		hash:   ethcommon.HexToHash("0x22"),
		number: 22,
		root:   ethcommon.HexToHash("0x2222"),
		valid:  false,
	}

	bc := &BlockChain{}
	bc.committedEVMHead.Store(first)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10_000; i++ {
			if i%2 == 0 {
				bc.committedEVMHead.Store(first)
			} else {
				bc.committedEVMHead.Store(second)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10_000; i++ {
			hash, number, root, valid, ok := bc.CommittedEVMHead()
			if !ok {
				t.Errorf("committed head unexpectedly unavailable")
				return
			}
			matchesFirst := hash == first.hash && number == first.number && root == first.root && valid == first.valid
			matchesSecond := hash == second.hash && number == second.number && root == second.root && valid == second.valid
			if !matchesFirst && !matchesSecond {
				t.Errorf("observed torn committed head: hash=%s number=%d root=%s valid=%t", hash, number, root, valid)
				return
			}
		}
	}()
	wg.Wait()
}

func TestCommittedEVMHeadUnavailableBeforePublication(t *testing.T) {
	bc := &BlockChain{}
	if _, _, _, _, ok := bc.CommittedEVMHead(); ok {
		t.Fatal("empty blockchain unexpectedly exposed a committed EVM head")
	}
}
