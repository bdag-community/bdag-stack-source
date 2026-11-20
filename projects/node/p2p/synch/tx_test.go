package synch

import (
	"bytes"
	"errors"
	"math/big"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
)

type txTestBlockChain struct {
	config   *params.ChainConfig
	gasLimit atomic.Uint64
	statedb  *state.StateDB
	headFeed *event.Feed
}

func newTxTestBlockChain(config *params.ChainConfig, gasLimit uint64, statedb *state.StateDB) *txTestBlockChain {
	chain := &txTestBlockChain{config: config, statedb: statedb, headFeed: new(event.Feed)}
	chain.gasLimit.Store(gasLimit)
	return chain
}

func (c *txTestBlockChain) Config() *params.ChainConfig { return c.config }

func (c *txTestBlockChain) CurrentBlock() *types.Header {
	return &types.Header{
		Number:   new(big.Int),
		GasLimit: c.gasLimit.Load(),
	}
}

func (c *txTestBlockChain) GetBlock(common.Hash, uint64) *types.Block {
	return types.NewBlock(c.CurrentBlock(), nil, nil, trie.NewStackTrie(nil))
}

func (c *txTestBlockChain) StateAt(common.Hash) (*state.StateDB, error) {
	return c.statedb, nil
}

func (c *txTestBlockChain) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
	return c.headFeed.Subscribe(ch)
}

type txTestP2P struct {
	*testP2P
	pool *legacypool.LegacyPool
}

func (p *txTestP2P) TxMemPool() *legacypool.LegacyPool {
	return p.pool
}

func newTxTestAddressReserver() txpool.AddressReserver {
	var (
		reserved = make(map[common.Address]struct{})
		lock     sync.Mutex
	)
	return func(addr common.Address, reserve bool) error {
		lock.Lock()
		defer lock.Unlock()
		if reserve {
			reserved[addr] = struct{}{}
			return nil
		}
		delete(reserved, addr)
		return nil
	}
}

func TestHandleTxMsgRejectsGovernanceRejectedTxBeforeAnnounce(t *testing.T) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	key, _ := crypto.GenerateKey()
	from := crypto.PubkeyToAddress(key.PublicKey)
	statedb.AddBalance(from, uint256.MustFromBig(big.NewInt(100000000000000)), tracing.BalanceChangeUnspecified)

	config := legacypool.DefaultConfig
	config.Journal = ""
	chain := newTxTestBlockChain(params.TestChainConfig, 10000000, statedb)
	pool := legacypool.New(config, chain)
	if err := pool.Init(config.PriceLimit, chain.CurrentBlock(), newTxTestAddressReserver()); err != nil {
		t.Fatalf("failed to initialize txpool: %v", err)
	}
	defer pool.Close()

	rejected := errors.New("test governance reject")
	pool.SetGovernanceAdmissibilityHook(func(txpool.GovernanceStateReader, types.Signer, *types.Transaction) error {
		return rejected
	})

	tx, err := types.SignTx(types.NewTransaction(0, common.Address{}, big.NewInt(1), 100000, big.NewInt(1), nil), types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatalf("failed to sign tx: %v", err)
	}
	var buf bytes.Buffer
	if err := tx.EncodeRLP(&buf); err != nil {
		t.Fatalf("failed to encode tx: %v", err)
	}

	s := &Sync{p2p: &txTestP2P{pool: pool}}
	_, err = s.handleTxMsg(&pb.Transaction{TxBytes: buf.Bytes()}, peer.ID("remote"))
	if !errors.Is(err, txpool.ErrGovernanceRejected) {
		t.Fatalf("handleTxMsg error = %v, want ErrGovernanceRejected", err)
	}
	if !errors.Is(err, rejected) {
		t.Fatalf("handleTxMsg error = %v, want wrapped governance cause", err)
	}
	if pool.Has(tx.Hash()) {
		t.Fatal("governance-rejected peer transaction was retained in the pool")
	}
}

func TestShouldRequestTxHash(t *testing.T) {
	first := common.HexToHash("0x01")
	second := common.HexToHash("0x02")
	seen := map[common.Hash]struct{}{}
	if !shouldRequestTxHash(seen, &first, false) {
		t.Fatal("first unseen hash should be requested")
	}
	if shouldRequestTxHash(seen, &first, false) {
		t.Fatal("duplicate hash should not be requested")
	}
	if shouldRequestTxHash(seen, &second, true) {
		t.Fatal("local hash should not be requested")
	}
	if _, ok := seen[second]; ok {
		t.Fatal("local hash should not be marked")
	}
	if !shouldRequestTxHash(seen, &second, false) {
		t.Fatal("hash should be requested after local skip")
	}
}

func TestProcessGetTxsFlushesAcceptedTailAfterDuplicateOrLocalHit(t *testing.T) {
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	key, _ := crypto.GenerateKey()
	from := crypto.PubkeyToAddress(key.PublicKey)
	statedb.AddBalance(from, uint256.MustFromBig(big.NewInt(100000000000000)), tracing.BalanceChangeUnspecified)
	config := legacypool.DefaultConfig
	config.Journal = ""
	chain := newTxTestBlockChain(params.TestChainConfig, 10000000, statedb)
	pool := legacypool.New(config, chain)
	if err := pool.Init(config.PriceLimit, chain.CurrentBlock(), newTxTestAddressReserver()); err != nil {
		t.Fatalf("failed to initialize txpool: %v", err)
	}
	defer pool.Close()
	local, err := types.SignTx(types.NewTransaction(0, common.Address{}, big.NewInt(1), 100000, big.NewInt(1), nil), types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatalf("sign local transaction: %v", err)
	}
	if errs := pool.Add([]*types.Transaction{local}, false, false); len(errs) != 1 || errs[0] != nil {
		t.Fatalf("add local transaction: %v", errs)
	}

	remote := common.HexToHash("0x01")
	peer := peers.NewPeer(peer.ID("remote"), nil)
	var requests [][]common.Hash
	s := &Sync{p2p: &txTestP2P{pool: pool}}
	s.sendOverride = func(_ *peers.Peer, protocol string, message any) (any, error) {
		if protocol != RPCTransaction {
			t.Fatalf("protocol = %s, want %s", protocol, RPCTransaction)
		}
		request := message.(*pb.GetTxs)
		hashes := make([]common.Hash, 0, len(request.Txs))
		for _, tx := range request.Txs {
			h, err := changePBHashToHash(tx)
			if err != nil {
				t.Fatalf("request hash: %v", err)
			}
			hashes = append(hashes, *h)
		}
		requests = append(requests, hashes)
		return &pb.Transactions{}, nil
	}
	ps := &PeerSync{sy: s, started: 1}
	localHash := local.Hash()
	if err := ps.processGetTxs(peer, []*common.Hash{&remote, &localHash}); err != nil {
		t.Fatalf("processGetTxs: %v", err)
	}
	if len(requests) != 1 || len(requests[0]) != 1 || requests[0][0] != remote {
		t.Fatalf("requests = %v, want one request containing remote hash only", requests)
	}
}

func TestProcessGetTxsFlushesFullBatchesAndTail(t *testing.T) {
	pool := testTxPool(t)
	defer pool.Close()
	peer := peers.NewPeer(peer.ID("remote"), nil)
	s := &Sync{p2p: &txTestP2P{pool: pool}}
	var requestSizes []int
	s.sendOverride = func(_ *peers.Peer, protocol string, message any) (any, error) {
		if protocol != RPCTransaction {
			t.Fatalf("protocol = %s, want %s", protocol, RPCTransaction)
		}
		requestSizes = append(requestSizes, len(message.(*pb.GetTxs).Txs))
		return &pb.Transactions{}, nil
	}
	ps := &PeerSync{sy: s, started: 1}
	hashes := make([]*common.Hash, MaxInvPerMsg+1)
	for i := range hashes {
		h := common.BigToHash(big.NewInt(int64(i + 1)))
		hashes[i] = &h
	}
	if err := ps.processGetTxs(peer, hashes); err != nil {
		t.Fatalf("processGetTxs: %v", err)
	}
	if len(requestSizes) != 2 || requestSizes[0] != MaxInvPerMsg || requestSizes[1] != 1 {
		t.Fatalf("request sizes = %v, want [%d 1]", requestSizes, MaxInvPerMsg)
	}
}

func TestProcessGetTxsActualBoundaryDuplicateAndFailureCases(t *testing.T) {
	newHashes := func(n int) []*common.Hash {
		hashes := make([]*common.Hash, n)
		for i := range hashes {
			h := common.BigToHash(big.NewInt(int64(i + 1)))
			hashes[i] = &h
		}
		return hashes
	}

	tests := []struct {
		name        string
		input       func() []*common.Hash
		wantBatches []int
		wantErr     bool
	}{
		{name: "empty", input: func() []*common.Hash { return nil }},
		{name: "one", input: func() []*common.Hash { return newHashes(1) }, wantBatches: []int{1}},
		{name: "max", input: func() []*common.Hash { return newHashes(MaxInvPerMsg) }, wantBatches: []int{MaxInvPerMsg}},
		{name: "max plus one", input: func() []*common.Hash { return newHashes(MaxInvPerMsg + 1) }, wantBatches: []int{MaxInvPerMsg, 1}},
		{name: "duplicate across full and tail", input: func() []*common.Hash {
			hashes := newHashes(MaxInvPerMsg)
			first := *hashes[0]
			extra := common.BigToHash(big.NewInt(int64(MaxInvPerMsg + 1)))
			return append(hashes, &first, &extra)
		}, wantBatches: []int{MaxInvPerMsg, 1}},
		{name: "nil before lookup", input: func() []*common.Hash {
			h := common.BigToHash(big.NewInt(99))
			return []*common.Hash{nil, &h}
		}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool := testTxPool(t)
			defer pool.Close()
			peer := peers.NewPeer(peer.ID("remote"), nil)
			s := &Sync{p2p: &txTestP2P{pool: pool}}
			var gotBatches []int
			s.sendOverride = func(_ *peers.Peer, protocol string, message any) (any, error) {
				if protocol != RPCTransaction {
					t.Fatalf("protocol = %s, want %s", protocol, RPCTransaction)
				}
				gotBatches = append(gotBatches, len(message.(*pb.GetTxs).Txs))
				return &pb.Transactions{}, nil
			}
			ps := &PeerSync{sy: s, started: 1}
			err := ps.processGetTxs(peer, tc.input())
			if tc.wantErr != (err != nil) {
				t.Fatalf("processGetTxs error = %v, wantErr=%v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(gotBatches, tc.wantBatches) {
				t.Fatalf("batches = %v, want %v", gotBatches, tc.wantBatches)
			}
		})
	}

	t.Run("first send error stops later batches", func(t *testing.T) {
		pool := testTxPool(t)
		defer pool.Close()
		peer := peers.NewPeer(peer.ID("remote"), nil)
		s := &Sync{p2p: &txTestP2P{pool: pool}}
		calls := 0
		s.sendOverride = func(_ *peers.Peer, _ string, _ any) (any, error) {
			calls++
			return nil, errors.New("send failed")
		}
		ps := &PeerSync{sy: s, started: 1}
		if err := ps.processGetTxs(peer, newHashes(MaxInvPerMsg+1)); err == nil {
			t.Fatal("processGetTxs unexpectedly succeeded")
		}
		if calls != 1 {
			t.Fatalf("send calls = %d, want 1", calls)
		}
	})

	t.Run("pre-stopped and shutdown after first batch", func(t *testing.T) {
		pool := testTxPool(t)
		defer pool.Close()
		peer := peers.NewPeer(peer.ID("remote"), nil)
		s := &Sync{p2p: &txTestP2P{pool: pool}}
		calls := 0
		ps := &PeerSync{sy: s}
		s.sendOverride = func(_ *peers.Peer, _ string, _ any) (any, error) {
			calls++
			atomic.StoreInt32(&ps.shutdown, 1)
			return &pb.Transactions{}, nil
		}
		if err := ps.processGetTxs(peer, newHashes(MaxInvPerMsg+1)); err == nil {
			t.Fatal("pre-stopped processGetTxs unexpectedly succeeded")
		}
		if calls != 0 {
			t.Fatalf("pre-stopped send calls = %d, want 0", calls)
		}

		atomic.StoreInt32(&ps.started, 1)
		atomic.StoreInt32(&ps.shutdown, 0)
		if err := ps.processGetTxs(peer, newHashes(MaxInvPerMsg+1)); err == nil {
			t.Fatal("shutdown processGetTxs unexpectedly succeeded")
		}
		if calls != 1 {
			t.Fatalf("shutdown send calls = %d, want 1", calls)
		}
	})
}

func testTxPool(t *testing.T) *legacypool.LegacyPool {
	t.Helper()
	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	config := legacypool.DefaultConfig
	config.Journal = ""
	chain := newTxTestBlockChain(params.TestChainConfig, 10000000, statedb)
	pool := legacypool.New(config, chain)
	if err := pool.Init(config.PriceLimit, chain.CurrentBlock(), newTxTestAddressReserver()); err != nil {
		t.Fatalf("failed to initialize txpool: %v", err)
	}
	return pool
}

func TestTransactionsIncrementalSizeMatchesGeneratedSSZ(t *testing.T) {
	txs := &pb.Transactions{}
	size := transactionsSSZBaseSize

	for _, txBytes := range [][]byte{
		nil,
		[]byte{0x01, 0x02},
		make([]byte, 128),
	} {
		size += transactionEntrySSZSize(txBytes)
		txs.Txs = append(txs.Txs, &pb.Transaction{TxBytes: txBytes})
		if got := txs.SizeSSZ(); got != size {
			t.Fatalf("incremental size = %d, generated SizeSSZ = %d", size, got)
		}
	}
}

func TestTransactionsBudgetCutoffMatchesGeneratedSSZCheck(t *testing.T) {
	cases := []struct {
		name  string
		limit uint64
		txs   [][]byte
	}{
		{name: "empty first tx under limit", limit: 32, txs: [][]byte{nil}},
		{name: "first tx reaches limit", limit: 12, txs: [][]byte{nil}},
		{name: "second tx reaches limit", limit: 30, txs: [][]byte{{0x01}, {0x02, 0x03}, {0x04}}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			oldTxs := &pb.Transactions{}
			newSize := transactionsSSZBaseSize
			oldAccepted := 0
			newAccepted := 0

			for _, txBytes := range tt.txs {
				pbtx := &pb.Transaction{TxBytes: txBytes}
				if uint64(oldTxs.SizeSSZ()+pbtx.SizeSSZ()+TXDATA_SSZ_HEAD_SIZE) >= tt.limit {
					break
				}
				oldTxs.Txs = append(oldTxs.Txs, pbtx)
				oldAccepted++
			}

			for _, txBytes := range tt.txs {
				entrySize := transactionEntrySSZSize(txBytes)
				if uint64(newSize+entrySize) >= tt.limit {
					break
				}
				newSize += entrySize
				newAccepted++
			}

			if newAccepted != oldAccepted {
				t.Fatalf("accepted txs = %d, want old behavior %d", newAccepted, oldAccepted)
			}
			if newSize != oldTxs.SizeSSZ() {
				t.Fatalf("final incremental size = %d, generated SizeSSZ = %d", newSize, oldTxs.SizeSSZ())
			}
		})
	}
}
