package miner

import (
	"fmt"
	"testing"
	"time"

	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
)

// templateWithStateRoot builds a template distinguishable by IsEqualForMiner,
// which keys on Version, ParentRoot and StateRoot.
func templateWithStateRoot(stateRoot string) *coretypes.BlockTemplate {
	return &coretypes.BlockTemplate{
		Block: &coretypes.Block{
			Header: coretypes.BlockHeader{
				Version:    1,
				ParentRoot: common.HexToHash("0xabc"),
				StateRoot:  common.HexToHash(stateRoot),
			},
		},
	}
}

// Templates are rebuilt on DAG, mempool and stall events, which on a busy node
// runs to ~2/second — far faster than a miner solves a job. Retention must
// therefore survive an unbounded number of rebuilds within the time window.
// A count-bounded window (the previous behaviour) evicted a template within a
// few seconds of it being handed out, so every solved block was rejected as
// tip_overdue.
func TestRecentTemplatesRetainAcrossManyRebuilds(t *testing.T) {
	m := &Miner{}

	const rebuilds = 50

	first := templateWithStateRoot("0x1")
	m.pushRecentTemplate(first)
	for i := 2; i <= rebuilds; i++ {
		m.pushRecentTemplate(templateWithStateRoot(fmt.Sprintf("0x%x", i)))
	}

	got := m.recentTemplateForHeader(&first.Block.Header)
	if got != first {
		t.Fatalf("template pushed %d rebuilds ago was evicted; a header mined "+
			"against it would be rejected as tip_overdue", rebuilds-1)
	}
}

func TestRecentTemplatesExpireByAge(t *testing.T) {
	m := &Miner{}

	stale := templateWithStateRoot("0x1")
	m.pushRecentTemplate(stale)
	fresh := templateWithStateRoot("0x2")
	m.pushRecentTemplate(fresh)

	// Age the first entry past the retention window.
	m.recentTemplates[0].addedAt = time.Now().Add(-recentTemplateRetention - time.Second)

	if got := m.recentTemplateForHeader(&stale.Block.Header); got != nil {
		t.Fatalf("template older than %s should have been evicted", recentTemplateRetention)
	}
	if got := m.recentTemplateForHeader(&fresh.Block.Header); got != fresh {
		t.Fatal("template within retention window was evicted")
	}
}

func TestRecentTemplatesCapBoundsMemory(t *testing.T) {
	m := &Miner{}

	for i := 0; i < recentTemplateMaxEntries+10; i++ {
		m.pushRecentTemplate(templateWithStateRoot(fmt.Sprintf("0x%x", i+1)))
	}

	if len(m.recentTemplates) != recentTemplateMaxEntries {
		t.Fatalf("retained %d templates, want cap of %d",
			len(m.recentTemplates), recentTemplateMaxEntries)
	}
	// The oldest entries are the ones dropped.
	oldest := templateWithStateRoot("0x1")
	if got := m.recentTemplateForHeader(&oldest.Block.Header); got != nil {
		t.Fatal("oldest template survived the entry cap")
	}
	newest := templateWithStateRoot(fmt.Sprintf("0x%x", recentTemplateMaxEntries+10))
	if got := m.recentTemplateForHeader(&newest.Block.Header); got == nil {
		t.Fatal("newest template was evicted by the entry cap")
	}
}

// A rebuild that leaves every miner-visible field unchanged must not consume a
// retention slot, otherwise stall refreshes age out genuinely distinct templates.
func TestRecentTemplatesDoNotRetainDuplicates(t *testing.T) {
	m := &Miner{}

	m.pushRecentTemplate(templateWithStateRoot("0x1"))
	m.pushRecentTemplate(templateWithStateRoot("0x1"))
	m.pushRecentTemplate(templateWithStateRoot("0x1"))

	if len(m.recentTemplates) != 1 {
		t.Fatalf("retained %d entries for one distinct template, want 1", len(m.recentTemplates))
	}
}

// Throttling a mempool change must delay the rebuild, never drop it: on an idle
// chain nothing else would call back in before the one-minute stall backstop,
// which would hold pending transactions out of the template that whole time.
func TestThrottledMempoolChangeStillRebuilds(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.armMempoolRebuild(time.Now())
	if !m.mempoolRebuildArmed {
		t.Fatal("arming a deferred rebuild did not record it")
	}

	select {
	case <-m.templateReqChan:
	case <-time.After(2 * time.Second):
		t.Fatal("throttled mempool change never scheduled a rebuild")
	}

	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("no template build request was enqueued")
	}
	if req.kind != templateBuildMempool {
		t.Fatalf("deferred rebuild kind = %v, want %v", req.kind, templateBuildMempool)
	}
	if req.invalidationCause != templateInvalidationMempoolRootChanged {
		t.Fatalf("deferred rebuild cause = %v, want %v",
			req.invalidationCause, templateInvalidationMempoolRootChanged)
	}
	if req.force {
		t.Fatal("deferred mempool rebuild must not force; that would evict retained templates")
	}
}

func TestMempoolRebuildArmsAtMostOnce(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1), mempoolRebuildArmed: true}

	m.armMempoolRebuild(time.Now())

	select {
	case <-m.templateReqChan:
		t.Fatal("re-armed a deferred rebuild while one was already pending")
	case <-time.After(200 * time.Millisecond):
	}
}
