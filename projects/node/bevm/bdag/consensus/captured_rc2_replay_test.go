package consensus_test

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	engine "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/stretchr/testify/require"
)

// This optional, offline acceptance test uses a pinned JSON-RPC block capture.
// It re-executes EVM state from genesis with the real engine and checks every
// captured header, transaction and root. It does not certify native DAG replay
// or post-activation behavior, which require separate integration evidence.
func TestCapturedRC2EVMReplay(t *testing.T) {
	path := os.Getenv("BDAG_RC2_EVM_CAPTURE")
	if path == "" {
		t.Skip("set BDAG_RC2_EVM_CAPTURE to a pinned RC2 eth_getBlockByNumber capture")
	}
	previous := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.TestNetParam
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &entries))
	require.Greater(t, len(entries), 103)
	genesis := bdag.BdagTestnetGenesis(nil)
	// The shared RC2 chain includes the public, coordinator-pinned genesisalloc
	// entries from its operator configuration, not a replacement genesis.
	allocRaw, err := os.ReadFile(os.Getenv("BDAG_RC2_GENESIS_ALLOC"))
	require.NoError(t, err)
	var allocations map[string]string
	require.NoError(t, json.Unmarshal(allocRaw, &allocations))
	for address, amount := range allocations {
		require.True(t, common.IsHexAddress(address))
		balance, ok := new(big.Int).SetString(amount, 10)
		require.True(t, ok)
		require.GreaterOrEqual(t, balance.Sign(), 0)
		bdag.AddGenesisAlloc(genesis, common.HexToAddress(address), balance)
	}
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, nil, genesis, nil, engine.New(db), vm.Config{}, nil)
	require.NoError(t, err)
	t.Cleanup(chain.Stop)
	for n, entry := range entries {
		var header types.Header
		var body struct {
			Hash         common.Hash          `json:"hash"`
			Transactions []*types.Transaction `json:"transactions"`
			Uncles       []common.Hash        `json:"uncles"`
		}
		require.NoError(t, json.Unmarshal(entry, &header))
		require.NoError(t, json.Unmarshal(entry, &body))
		require.EqualValues(t, n, header.Number.Uint64())
		require.Equal(t, body.Hash, header.Hash(), "captured header hash %d", n)
		require.Empty(t, body.Uncles, "unexpected uncles require a complete body capture")
		if n == 0 {
			require.Equal(t, common.HexToHash("0x58d97c1c16a3ff988105c2bd101b2f473103cc2ef7106ade3296647de65a1663"), body.Hash)
			require.Equal(t, body.Hash, chain.Genesis().Hash())
			require.Equal(t, header.Root, chain.Genesis().Root())
			continue
		}
		block := types.NewBlockWithHeader(&header).WithBody(types.Body{Transactions: body.Transactions})
		_, err = chain.InsertChain(types.Blocks{block})
		require.NoError(t, err, "captured EVM block %d", n)
		require.Equal(t, body.Hash, chain.CurrentBlock().Hash(), "canonical hash at %d", n)
		require.Equal(t, header.Root, chain.CurrentBlock().Root, "replayed root at %d", n)
	}
	t.Logf("Replayed %d RC2 EVM blocks from genesis; final hash %s root %s", len(entries)-1, chain.CurrentBlock().Hash(), chain.CurrentBlock().Root)
}
