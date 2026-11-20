package p2p

import (
	"errors"
	"testing"
	"time"

	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
)

func TestCollectRebroadcastNotifyDataAddsPresentTransactions(t *testing.T) {
	hash := common.HexToHash("0x1")
	txd := &coretypes.TxDesc{}
	pendingInvs := map[common.Hash]pendingInv{
		hash: {data: txd, added: time.Now()},
	}

	nds := collectRebroadcastNotifyData(pendingInvs, func(h common.Hash) bool {
		return h == hash
	}, func() bool {
		return false
	})

	if len(nds) != 1 {
		t.Fatalf("expected 1 notify item, got %d", len(nds))
	}
	got, ok := nds[0].Data.(*coretypes.TxDesc)
	if !ok {
		t.Fatalf("expected TxDesc notify data, got %T", nds[0].Data)
	}
	if got != txd {
		t.Fatalf("expected original TxDesc pointer")
	}
	if _, ok := pendingInvs[hash]; !ok {
		t.Fatalf("expected present transaction to remain pending")
	}
}

func TestCollectRebroadcastNotifyDataRemovesMissingTransactions(t *testing.T) {
	hash := common.HexToHash("0x2")
	pendingInvs := map[common.Hash]pendingInv{
		hash: {data: &coretypes.TxDesc{}, added: time.Now()},
	}

	nds := collectRebroadcastNotifyData(pendingInvs, func(common.Hash) bool {
		return false
	}, func() bool {
		return false
	})

	if len(nds) != 0 {
		t.Fatalf("expected no notify items, got %d", len(nds))
	}
	if _, ok := pendingInvs[hash]; ok {
		t.Fatalf("expected missing transaction to be removed from pending inventory")
	}
}

func TestCollectRebroadcastNotifyDataIgnoresUnsupportedInventory(t *testing.T) {
	hash := common.HexToHash("0x3")
	pendingInvs := map[common.Hash]pendingInv{
		hash: {data: "unsupported", added: time.Now()},
	}

	nds := collectRebroadcastNotifyData(pendingInvs, func(common.Hash) bool {
		t.Fatalf("hasTx should not be called for unsupported inventory")
		return false
	}, func() bool {
		return false
	})

	if len(nds) != 0 {
		t.Fatalf("expected no notify items, got %d", len(nds))
	}
	if _, ok := pendingInvs[hash]; !ok {
		t.Fatalf("expected unsupported inventory to remain pending")
	}
}

func TestCollectRebroadcastNotifyDataStopsWhenBudgetExpires(t *testing.T) {
	pendingInvs := map[common.Hash]pendingInv{
		common.HexToHash("0x4"): {data: &coretypes.TxDesc{}, added: time.Now()},
		common.HexToHash("0x5"): {data: &coretypes.TxDesc{}, added: time.Now()},
	}
	calls := 0

	nds := collectRebroadcastNotifyData(pendingInvs, func(common.Hash) bool {
		return true
	}, func() bool {
		calls++
		return calls > 1
	})

	if len(nds) != 1 {
		t.Fatalf("expected one notify item before budget expiry, got %d", len(nds))
	}
}

func TestRebroadcastIntervalHasMinimumAndScales(t *testing.T) {
	target := params.ActiveNetParams.TargetTimePerBlock

	if got := rebroadcastInterval(0); got != target {
		t.Fatalf("expected minimum interval %s, got %s", target, got)
	}
	if got := rebroadcastInterval(49); got != target {
		t.Fatalf("expected minimum interval below batch threshold, got %s", got)
	}
	if got := rebroadcastInterval(100); got != 2*target {
		t.Fatalf("expected scaled interval %s, got %s", 2*target, got)
	}
}

func TestRegainMempoolSignalIsCoalescedAndConsumable(t *testing.T) {
	r := &Rebroadcast{}
	r.RegainMempool()
	r.RegainMempool()
	if !r.hasPendingRegainMempool() {
		t.Fatal("expected pending regain signal")
	}
	r.consumeRegainMempool()
	if r.hasPendingRegainMempool() {
		t.Fatal("expected consumed regain signal")
	}
}

func TestRegainMempoolDecision(t *testing.T) {
	tests := []struct {
		name             string
		pending, current bool
		count, peers     int
		send, consume    bool
	}{
		{"no pending", false, true, 0, 1, false, false},
		{"not current", true, false, 0, 1, false, false},
		{"no peers", true, true, 0, 0, false, false},
		{"oversize", true, true, MaxRegainMempoolSize + 1, 1, false, true},
		{"ready", true, true, MaxRegainMempoolSize, 1, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			send, consume := regainMempoolDecision(tc.pending, tc.current, tc.count, tc.peers)
			if send != tc.send || consume != tc.consume {
				t.Fatalf("got (%v,%v), want (%v,%v)", send, consume, tc.send, tc.consume)
			}
		})
	}
}

func TestOnRegainMempoolRetainsConcurrentNotificationDuringSend(t *testing.T) {
	sendStarted := make(chan struct{})
	release := make(chan struct{})
	sendDone := make(chan struct{})
	r := &Rebroadcast{sendMempool: func(*peers.Peer, uint64) error {
		close(sendStarted)
		<-release
		close(sendDone)
		return nil
	}, snapshotMempool: func() mempoolRegainSnapshot {
		return mempoolRegainSnapshot{isCurrent: true, peer: &peers.Peer{}, count: 1}
	}}
	r.RegainMempool()
	r.onRegainMempool()
	<-sendStarted
	r.RegainMempool()
	close(release)
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("blocked send did not complete")
	}
	if !r.hasPendingRegainMempool() {
		t.Fatal("concurrent notification was cleared after the first send")
	}
}

func TestOnRegainMempoolRearmsOversizeDrainWithoutNotification(t *testing.T) {
	count := MaxRegainMempoolSize + 1
	sends := make(chan uint64, 1)
	r := &Rebroadcast{
		sendMempool: func(_ *peers.Peer, got uint64) error {
			sends <- got
			return nil
		},
		snapshotMempool: func() mempoolRegainSnapshot {
			return mempoolRegainSnapshot{isCurrent: true, peer: &peers.Peer{}, count: count}
		},
	}
	r.RegainMempool()
	r.onRegainMempool()
	if !r.hasPendingRegainMempool() {
		t.Fatal("oversize regain was consumed before the pool drained")
	}
	select {
	case got := <-sends:
		t.Fatalf("oversize regain sent %d transactions", got)
	default:
	}

	count = MaxRegainMempoolSize - 1
	r.onRegainMempool()
	select {
	case got := <-sends:
		if got != uint64(count) {
			t.Fatalf("drained regain count = %d, want %d", got, count)
		}
	case <-time.After(time.Second):
		t.Fatal("drained regain was not sent without a new notification")
	}
}

func TestOnRegainMempoolRearmsAfterSendFailure(t *testing.T) {
	r := &Rebroadcast{sendMempool: func(*peers.Peer, uint64) error { return errors.New("send failed") }, snapshotMempool: func() mempoolRegainSnapshot {
		return mempoolRegainSnapshot{isCurrent: true, peer: &peers.Peer{}, count: 1}
	}}
	r.RegainMempool()
	r.onRegainMempool()
	deadline := time.After(time.Second)
	for !r.hasPendingRegainMempool() {
		select {
		case <-deadline:
			t.Fatal("send failure did not rearm notification")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
