package blockchain

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

const SafeEVMCheckpointDepth uint64 = 5_000

type EVMCheckpoint struct {
	Number uint64
	Hash   common.Hash
	Root   common.Hash
}

func (b *BlockChain) SafeEVMCheckpoint() (uint64, common.Hash, common.Hash, bool) {
	if b == nil || b.bdagChain == nil {
		return 0, common.Hash{}, common.Hash{}, false
	}
	return b.bdagChain.SafeEVMCheckpoint(SafeEVMCheckpointDepth)
}

func (b *BlockChain) SetPeerEVMCheckpoint(pid peer.ID, number uint64, hash common.Hash, root common.Hash) {
	if b == nil || pid == "" {
		return
	}
	b.evmPeerCheckpoints.Store(pid, EVMCheckpoint{
		Number: number,
		Hash:   hash,
		Root:   root,
	})
}

func (b *BlockChain) peerEVMCheckpoint(source *peer.ID) (*EVMCheckpoint, bool) {
	if b == nil || source == nil || *source == "" {
		return nil, false
	}
	value, ok := b.evmPeerCheckpoints.Load(*source)
	if !ok {
		return nil, false
	}
	checkpoint, ok := value.(EVMCheckpoint)
	if !ok {
		return nil, false
	}
	return &checkpoint, true
}

func (b *BlockChain) ValidateEVMCheckpoint(number uint64, hash common.Hash, root common.Hash) (bool, bool) {
	if b == nil || b.bdagChain == nil {
		return false, false
	}
	return b.bdagChain.ValidateEVMCheckpoint(number, hash, root)
}

func (b *BlockChain) CheckEVMCheckpoint(number uint64, hash common.Hash, root common.Hash) error {
	valid, known := b.ValidateEVMCheckpoint(number, hash, root)
	if !known {
		return nil
	}
	if !valid {
		return fmt.Errorf("evm checkpoint mismatch at block %d: want hash=%s root=%s", number, hash, root)
	}
	return nil
}
