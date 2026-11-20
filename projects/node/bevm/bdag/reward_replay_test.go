package bdag

import (
	"errors"
	"math/big"
	"testing"

	bconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	bparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// The keys below are public synthetic fixtures and never hold real funds.
func newRewardReplayFixture(t *testing.T, activation, split uint64) (*core.BlockChain, *types.Block, common.Address) {
	t.Helper()
	previous := qparams.ActiveNetParams
	active := qparams.TestNetParam
	params := *active.Params
	params.StakingActivationEpoch = activation
	active.Params = &params
	qparams.ActiveNetParams = &active
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	poolKey, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	require.NoError(t, err)
	otherKey, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000002")
	require.NoError(t, err)
	pool := crypto.PubkeyToAddress(poolKey.PublicKey)
	other := crypto.PubkeyToAddress(otherKey.PublicKey)
	genesis := BdagTestnetGenesis(types.GenesisAlloc{})
	if split != 0 {
		genesis.Alloc[bparams.StakingContractAddr] = types.Account{Balance: new(big.Int), Storage: map[common.Hash]common.Hash{bparams.SlotMinerSplit: common.BigToHash(new(big.Int).SetUint64(split))}}
	}
	AddGenesisAlloc(genesis, pool, new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil))
	AddGenesisAlloc(genesis, other, new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil))
	db := rawdb.NewMemoryDatabase()
	chain, err := core.NewBlockChain(db, nil, genesis, nil, bconsensus.New(db), vm.Config{}, nil)
	require.NoError(t, err)
	t.Cleanup(chain.Stop)
	baseFee := eip1559.CalcBaseFee(genesis.Config, chain.Genesis().Header())
	price := new(big.Int).Add(baseFee, big.NewInt(1_000_000_000))
	require.Positive(t, price.Cmp(baseFee))
	maxCost := new(big.Int).Add(new(big.Int).Mul(big.NewInt(21000), price), big.NewInt(17))
	require.Positive(t, genesis.Alloc[pool].Balance.Cmp(maxCost))
	require.Positive(t, genesis.Alloc[other].Balance.Cmp(maxCost))
	genDB := rawdb.NewMemoryDatabase()
	_, blocks, _ := core.GenerateChainWithGenesis(genesis, bconsensus.New(genDB), 1, func(_ int, b *core.BlockGen) {
		b.SetCoinbase(pool)
		b.SetExtra(bconsensus.EncodeRewardExtra(1000))
		toPool := types.NewTransaction(0, pool, big.NewInt(17), 21000, price, nil)
		toOther := types.NewTransaction(0, other, big.NewInt(9), 21000, price, nil)
		signer := types.LatestSigner(genesis.Config)
		external, signErr := types.SignTx(toPool, signer, otherKey)
		require.NoError(t, signErr)
		self, signErr := types.SignTx(toOther, signer, poolKey)
		require.NoError(t, signErr)
		b.AddTx(external)
		b.AddTx(self)
	})
	_, err = chain.InsertChain(blocks)
	require.NoError(t, err)
	return chain, blocks[0], pool
}

func TestRewardReplayCreditsAndFees(t *testing.T) {
	for _, tc := range []struct {
		name              string
		activation, split uint64
		subsidy           int64
	}{
		{"before staking", 1_000_000, 0, 10_000_000_000_000},
		{"after staking", 0, 0, 7_000_000_000_000},
		{"governance split", 0, 37, 3_700_000_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain, block, wallet := newRewardReplayFixture(t, tc.activation, tc.split)
			before, err := chain.StateAt(block.Root())
			require.NoError(t, err)
			balance := before.GetBalance(wallet).ToBig()
			evidence, err := replayEVMReward(chain, block.Hash(), wallet)
			require.NoError(t, err)
			require.Equal(t, block.Hash(), evidence.BlockHash)
			require.Equal(t, block.Root(), evidence.StateRoot)
			require.Equal(t, big.NewInt(tc.subsidy), evidence.SubsidyWei)
			fee := new(big.Int).Mul(big.NewInt(21000), big.NewInt(1_000_000_000))
			require.Equal(t, fee, evidence.OtherSenderFeesWei)
			require.Equal(t, fee, evidence.SelfPaidFeesWei)
			after, err := chain.StateAt(block.Root())
			require.NoError(t, err)
			require.Equal(t, balance, after.GetBalance(wallet).ToBig(), "replay must not commit state")
		})
	}
}

type changingRewardReplayChain struct {
	*core.BlockChain
	calls int
}

func (c *changingRewardReplayChain) GetBlockByNumber(n uint64) *types.Block {
	c.calls++
	if c.calls > 1 {
		return nil
	}
	return c.BlockChain.GetBlockByNumber(n)
}

type missingRewardStateChain struct{ *core.BlockChain }

func (c *missingRewardStateChain) StateAt(common.Hash) (*state.StateDB, error) {
	return nil, errors.New("pruned")
}

type rejectingRewardValidator struct{ core.Validator }

func (v rejectingRewardValidator) ValidateState(*types.Block, *state.StateDB, *core.ProcessResult, bool) error {
	return errors.New("invalid state root")
}

type invalidRewardReplayChain struct{ *core.BlockChain }

func (c *invalidRewardReplayChain) Validator() core.Validator {
	return rejectingRewardValidator{c.BlockChain.Validator()}
}

func TestRewardReplayRejectsUnprovenInputs(t *testing.T) {
	chain, block, wallet := newRewardReplayFixture(t, 0, 0)
	for _, tc := range []struct {
		name   string
		chain  rewardReplayChain
		hash   common.Hash
		wallet common.Address
	}{
		{"wrong wallet", chain, block.Hash(), common.HexToAddress("0x1234")},
		{"missing block", chain, common.HexToHash("0x1234"), wallet},
		{"genesis", chain, chain.Genesis().Hash(), wallet},
		{"staking contract", chain, block.Hash(), bparams.StakingContractAddr},
		{"pruned state", &missingRewardStateChain{chain}, block.Hash(), wallet},
		{"invalid replay state", &invalidRewardReplayChain{chain}, block.Hash(), wallet},
		{"canonical change", &changingRewardReplayChain{BlockChain: chain}, block.Hash(), wallet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := replayEVMReward(tc.chain, tc.hash, tc.wallet)
			require.Error(t, err)
			require.Nil(t, evidence)
		})
	}
}
