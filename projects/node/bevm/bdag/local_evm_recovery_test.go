package bdag

import "testing"

type heldCanonicalMiningChain struct{ canonicalMiningTestChain }

func (heldCanonicalMiningChain) LocalEVMRecoveryStatus() (bool, string) {
	return true, "missing exact local anchor"
}

func TestLocalEVMRecoveryDisablesCanonicalTemplateBypass(t *testing.T) {
	pool, consensus := newCanonicalMiningResetTestPool(t)
	consensus.chain = heldCanonicalMiningChain{canonicalMiningTestChain{header: pool.chain.CurrentBlock()}}
	if pool.CanonicalMiningTemplateReady() {
		t.Fatal("exact canonical old head bypassed local recovery hold")
	}
}
