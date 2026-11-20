package blockchain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
)

func TestProcessBlockContextRejectsCanceledCallerBeforeQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := (*BlockChain)(nil).ProcessBlockContext(ctx, newProcessContextTestBlock(1), 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessBlockContext error = %v, want context canceled", err)
	}
}

func TestProcessBlockContextCancellationDuringApplyKeepsReservation(t *testing.T) {
	bc := &BlockChain{msgChan: make(chan *processMsg, 1), quit: make(chan struct{})}
	block := newProcessContextTestBlock(2)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := bc.ProcessBlockContext(ctx, block, 0, nil)
		result <- err
	}()

	var msg *processMsg
	select {
	case msg = <-bc.msgChan:
	case <-time.After(time.Second):
		t.Fatal("block was not queued")
	}
	if !msg.phase.CompareAndSwap(processMessageQueued, processMessageApplying) {
		t.Fatal("could not mark queued block as applying")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not return")
	}
	if got := bc.ProcessQueueSize(); got != 1 {
		t.Fatalf("queue reservation = %d, want 1 while apply is active", got)
	}
	if _, _, err := bc.ProcessBlock(block, 0, nil); err == nil || !strings.Contains(err.Error(), "Already exists in the queue") {
		t.Fatalf("duplicate block error = %v, want queue reservation", err)
	}
	replyProcessMessage(msg, &processResult{})
	deadline := time.Now().Add(time.Second)
	for bc.ProcessQueueSize() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := bc.ProcessQueueSize(); got != 0 {
		t.Fatalf("queue reservation = %d after apply result, want 0", got)
	}
}

func TestBlockChainHandlerCancellationBeforeApplyAndShutdownDrain(t *testing.T) {
	bc := &BlockChain{msgChan: make(chan *processMsg, 2), quit: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	block := newProcessContextTestBlock(3)
	key := *block.Hash()
	reservation := &processReservation{}
	bc.processQueueMap.Store(key, reservation)
	msg := &processMsg{ctx: ctx, block: block, queueKey: key, reservation: reservation, result: make(chan *processResult, 1)}
	bc.msgChan <- msg

	done := make(chan struct{})
	bc.wg.Add(1)
	go func() {
		bc.handler()
		close(done)
	}()
	select {
	case result := <-msg.result:
		if result == nil || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("canceled message result = %+v, want context canceled", result)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not reject canceled queued block")
	}
	if got := bc.ProcessQueueSize(); got != 0 {
		t.Fatalf("queue reservation = %d after pre-apply cancellation, want 0", got)
	}
	close(bc.quit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after queue shutdown")
	}

	bc2 := &BlockChain{msgChan: make(chan *processMsg, 2), quit: make(chan struct{})}
	messages := []*processMsg{
		{result: make(chan *processResult, 1)},
		{result: make(chan *processResult, 1)},
	}
	for _, queued := range messages {
		bc2.msgChan <- queued
	}
	close(bc2.quit)
	bc2.wg.Add(1)
	done2 := make(chan struct{})
	go func() {
		bc2.handler()
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("handler did not drain on shutdown")
	}
	for i, queued := range messages {
		select {
		case result := <-queued.result:
			if result == nil || !errors.Is(result.err, errBlockChainShutdown) {
				t.Fatalf("shutdown message %d result = %+v, want shutdown", i, result)
			}
		case <-time.After(time.Second):
			t.Fatalf("shutdown message %d received no result", i)
		}
	}
}

func TestAbandonedMessageCannotDeleteReplacementReservation(t *testing.T) {
	bc := &BlockChain{}
	key := *newProcessContextTestBlock(4).Hash()
	abandoned := &processReservation{}
	replacement := &processReservation{}
	bc.processQueueMap.Store(key, abandoned)
	if !bc.processQueueMap.CompareAndDelete(key, abandoned) {
		t.Fatal("could not release abandoned reservation")
	}
	bc.processQueueMap.Store(key, replacement)

	// The old message can be observed by the handler after a new caller has
	// reserved the same hash. Its cleanup must not erase the new reservation.
	deleteProcessReservation(bc, key, abandoned)
	got, ok := bc.processQueueMap.Load(key)
	if !ok || got != replacement {
		t.Fatalf("replacement reservation was removed: ok=%t got=%p want=%p", ok, got, replacement)
	}
}

func newProcessContextTestBlock(nonce uint32) *types.SerializedBlock {
	return types.NewBlock(&types.Block{Header: types.BlockHeader{
		Timestamp: time.Unix(int64(nonce+1), 0),
		Pow:       pow.GetInstance(pow.BDAGXKECCAKV1, nonce, nonce, nil),
	}})
}
