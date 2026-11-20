//go:build bdag_recovery_test

package bdag

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type bridgeRewindChain struct{}

func (bridgeRewindChain) CurrentBlock() *types.Header                 { return &types.Header{Number: big.NewInt(0)} }
func (bridgeRewindChain) Genesis() *types.Block                       { return nil }
func (bridgeRewindChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (bridgeRewindChain) GetBlock(common.Hash, uint64) *types.Block   { return nil }
func (bridgeRewindChain) GetBlockByNumber(uint64) *types.Block        { return nil }
func (bridgeRewindChain) HasState(common.Hash) bool                   { return false }
func (bridgeRewindChain) SetHead(uint64) error                        { return errors.New("bridge backend") }

func TestRewindBridgeIsolatesTargetAndRestoresProvider(t *testing.T) {
	target := &BdagChain{}
	other := &BdagChain{}
	backend := bridgeRewindChain{}
	previous := evmRewindChainProvider
	restore := BindTestEVMRewindChain(target, backend)
	t.Cleanup(restore)
	if got := evmRewindChainProvider(target); got != backend {
		t.Fatalf("target provider = %T, want bridge backend", got)
	}
	if got := evmRewindChainProvider(other); got == backend {
		t.Fatal("bridge backend leaked to a different BdagChain target")
	}
	restore()
	restore()
	if got := evmRewindChainProvider(target); got == backend {
		t.Fatal("restore left bridge backend installed")
	}
	if evmRewindChainProvider(target) != previous(target) {
		t.Fatal("restore did not reinstate the previous provider")
	}
}

func TestRewindBridgeRejectsNilBindings(t *testing.T) {
	assertPanic := func(name string, bind func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not reject nil binding", name)
			}
		}()
		bind()
	}
	assertPanic("nil target", func() { BindTestEVMRewindChain(nil, bridgeRewindChain{}) })
	assertPanic("nil backend", func() { BindTestEVMRewindChain(&BdagChain{}, nil) })
}
