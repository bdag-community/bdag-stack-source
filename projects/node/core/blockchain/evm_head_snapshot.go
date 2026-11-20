package blockchain

import ethcommon "github.com/ethereum/go-ethereum/common"

// committedNativeEVMHead is immutable after publication. Readers use it to
// validate the geth head without touching mutable native block state or taking
// the chain lock held by block acceptance.
type committedNativeEVMHead struct {
	hash   ethcommon.Hash
	number uint64
	root   ethcommon.Hash
	valid  bool
}

// publishCommittedEVMHead must run after the native state transition is
// durable and while the caller either holds chainLock or is still single-
// threaded during startup.
func (b *BlockChain) publishCommittedEVMHead() {
	if b == nil || b.bd == nil {
		return
	}
	tip := b.bd.GetMainChainTip()
	if tip == nil || tip.GetState() == nil {
		return
	}
	state := tip.GetState()
	b.committedEVMHead.Store(&committedNativeEVMHead{
		hash:   state.GetEVMHash(),
		number: state.GetEVMNumber(),
		root:   state.GetEVMRoot(),
		valid:  !state.GetStatus().KnownInvalid(),
	})
}

// CommittedEVMHead returns one atomically published native-to-EVM mapping.
// The returned values never come from a partially applied native block.
func (b *BlockChain) CommittedEVMHead() (hash ethcommon.Hash, number uint64, root ethcommon.Hash, valid bool, ok bool) {
	if b == nil {
		return ethcommon.Hash{}, 0, ethcommon.Hash{}, false, false
	}
	head := b.committedEVMHead.Load()
	if head == nil {
		return ethcommon.Hash{}, 0, ethcommon.Hash{}, false, false
	}
	return head.hash, head.number, head.root, head.valid, true
}

// WithCommittedEVMHead runs a read only across a stable EVM generation whose
// current geth head exactly matches the atomically published native commitment.
// The caller must buffer externally visible output until this returns true.
func (b *BlockChain) WithCommittedEVMHead(read func()) bool {
	if b == nil || read == nil {
		return false
	}
	generation := b.evmChainLock.Generation()
	if generation&1 != 0 || !b.committedEVMHeadMatchesCurrent() {
		return false
	}
	read()
	return generation == b.evmChainLock.Generation() && generation&1 == 0 && b.committedEVMHeadMatchesCurrent()
}

func (b *BlockChain) committedEVMHeadMatchesCurrent() bool {
	evmChain := b.EVMChain()
	if evmChain == nil || evmChain.ETHChain() == nil || evmChain.ETHChain().Ether() == nil ||
		evmChain.ETHChain().Ether().BlockChain() == nil {
		return false
	}
	current := evmChain.ETHChain().Ether().BlockChain().CurrentBlock()
	if current == nil || current.Number == nil {
		return false
	}
	hash, number, root, valid, known := b.CommittedEVMHead()
	if !known || !valid || current.Number.Uint64() != number || current.Hash() != hash || current.Root != root {
		return false
	}
	return true
}
