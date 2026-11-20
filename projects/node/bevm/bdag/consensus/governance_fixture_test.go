package consensus_test

import (
	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

// Run with GOVERNANCE_FORK_BLOCK/CONFIG_JSON against the isolated generated
// genesis. Baseline builds have no governance fixture and intentionally skip it.
func TestGovernanceExportedGenesisFixture(t *testing.T) {
	config := mparams.BdagDevnetChainConfig
	if config.BdagGovernanceForkBlock == nil {
		t.Skip("isolated governance fixture not configured")
	}
	g := config.BdagGovernanceFork
	alloc := bdag.DecodeAlloc(qparams.DevNetParam.Params)
	s := bdag.MakePreState(rawdb.NewMemoryDatabase(), alloc)
	balance := new(big.Int).Set(s.GetBalance(mparams.StakingContractAddr).ToBig())
	require.Positive(t, balance.Sign())
	header := &types.Header{Number: new(big.Int).Set(config.BdagGovernanceForkBlock), Time: g.NotBeforeTimestamp, Difficulty: big.NewInt(1), GasLimit: 45000000, BaseFee: big.NewInt(0)}
	require.NoError(t, bdagconsensus.ApplyGovernanceFork(config, header, s))
	require.Equal(t, balance, s.GetBalance(mparams.StakingContractAddr).ToBig())
	for _, a := range []common.Address{mparams.StakingContractAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, mparams.ChainTallyAddr} {
		for slot, value := range alloc[a].Storage {
			if slot == (common.Hash{}) && a != mparams.ChainTallyAddr {
				continue
			}
			if a == mparams.StakingContractAddr && slot == mparams.SlotERC1967Implementation {
				continue
			}
			if a == mparams.StakingContractAddr && (slot == common.BigToHash(big.NewInt(22)) || slot == common.BigToHash(big.NewInt(23)) || slot == common.BigToHash(big.NewInt(32))) {
				continue
			}
			require.Equal(t, value, s.GetState(a, slot), "fixture storage preserved %s %s", a, slot)
		}
	}
	require.Equal(t, g.StakingRuntimeHash, s.GetCodeHash(mparams.StakingV2ImplAddr))
	require.Equal(t, g.ReaderRuntimeHash, s.GetCodeHash(mparams.ChainTallyAddr))
	require.Equal(t, g.HubRuntimeHash, s.GetCodeHash(common.HexToAddress("0x1000000000000000000000000000000000000016")))
}
