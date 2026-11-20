package bdag

import (
	"errors"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	corestate "github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

type interruptRecoveryConsensus struct {
	model.Consensus
	interrupt <-chan struct{}
	chain     model.BlockChain
}

func (c interruptRecoveryConsensus) Interrupt() <-chan struct{}   { return c.interrupt }
func (c interruptRecoveryConsensus) BlockChain() model.BlockChain { return c.chain }

type interruptRecoveryNativeChain struct {
	model.BlockChain
	onRead func(uint64) model.BlockState
}

func (c interruptRecoveryNativeChain) GetBlockState(order uint64) model.BlockState {
	return c.onRead(order)
}

func TestPrepareEnvironmentHonoursAlreadyRequestedInterrupt(t *testing.T) {
	interrupt := make(chan struct{})
	close(interrupt)
	b := &BdagChain{consensus: interruptRecoveryConsensus{interrupt: interrupt}}
	_, err := b.PrepareEnvironment(corestate.NewBlockState(1))
	if !errors.Is(err, errEVMStateReconstructionInterrupted) {
		t.Fatalf("closed interrupt returned %v, want reconstruction cancellation", err)
	}
}

func TestPrepareEnvironmentInterruptsAnchorScanAndRestore(t *testing.T) {
	for _, stage := range []string{"anchor scan", "restore"} {
		t.Run(stage, func(t *testing.T) {
			chain, _ := newRewindTestChain(t)
			before := types.CopyHeader(chain.CurrentBlock())
			targetHeader := types.CopyHeader(before)
			targetHeader.Number = new(big.Int).Add(before.Number, big.NewInt(1))
			targetHeader.Extra = []byte("unrestored target")
			target := corestate.NewBlockState(3)
			target.SetOrder(3)
			target.SetEVM(targetHeader)
			interrupt := make(chan struct{})
			reads := 0
			native := interruptRecoveryNativeChain{onRead: func(order uint64) model.BlockState {
				reads++
				if reads > 1 {
					t.Fatal("continued reading native states after interrupt")
				}
				close(interrupt)
				previous := corestate.NewBlockState(order)
				previous.SetOrder(order)
				if stage == "restore" {
					previous.SetEVM(before)
				} else {
					previous.SetEVM(targetHeader)
				}
				return previous
			}}
			previousProvider := evmRewindChainProvider
			evmRewindChainProvider = func(*BdagChain) evmRewindChain { return chain }
			t.Cleanup(func() { evmRewindChainProvider = previousProvider })
			memo := &evmRecoveryFailureMemo{err: errors.New("existing recovery evidence")}
			b := &BdagChain{consensus: interruptRecoveryConsensus{interrupt: interrupt, chain: native}, lastEVMRecoveryFailure: memo}
			_, err := b.PrepareEnvironment(target)
			if !errors.Is(err, errEVMStateReconstructionInterrupted) {
				t.Fatalf("%s returned %v, want cancellation", stage, err)
			}
			if chain.CurrentBlock().Hash() != before.Hash() {
				t.Fatal("cancellation mutated canonical EVM head")
			}
			if b.lastEVMRecoveryFailure != memo {
				t.Fatal("cancellation altered recorded recovery evidence")
			}
		})
	}
}
