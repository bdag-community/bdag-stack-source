package consensus

import (
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/triedb"
)

// Governance relies on this native invariant: a decreasing positive timestamp
// cannot reopen a closed work epoch, even though it remains a valid EVM time.
func TestStakingTalliesKeepNativeEpochOnTimestampRegression(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	defer func() { qparams.ActiveNetParams = previousNet }()
	for _, config := range []struct {
		name     string
		activate func()
	}{
		{"mainnet", func() { qparams.ActiveNetParams = &qparams.MainNetParam }},
		{"devnet", func() { qparams.ActiveNetParams = &qparams.DevNetParam }},
	} {
		t.Run(config.name, func(t *testing.T) {
			config.activate()
			net := qparams.ActiveNetParams
			db := rawdb.NewMemoryDatabase()
			tdb := triedb.NewDatabase(db, nil)
			defer tdb.Close()
			statedb, err := state.New(common.Hash{}, state.NewDatabase(tdb, nil))
			if err != nil {
				t.Fatal(err)
			}
			engine := New(db)
			miner := common.HexToAddress("0x1234567890123456789012345678901234567890")
			genesis := uint64(net.GenesisBlock.Block().Header.Timestamp.Unix())
			duration := uint64(net.StakingEpochDurationSeconds)
			if net == &qparams.MainNetParam {
				duration = 7 * 24 * 60 * 60
			}
			if duration == 0 {
				t.Fatal("fixture epoch duration is zero")
			}
			// First process epoch 5 normally, then regress the next two headers to
			// epoch 2. No artificial storage writes establish the native marker.
			header := &types.Header{Number: big.NewInt(1), Time: genesis + 5*duration + 1, Coinbase: miner}
			engine.updateStakingTallies(nil, header, statedb, big.NewInt(100))
			base := crypto.Keccak256Hash(big.NewInt(5).Bytes())
			closedBase := crypto.Keccak256Hash(big.NewInt(2).Bytes())
			lastBase := statedb.GetState(params.ChainTallyAddr, params.SlotLastTallyBase)
			for i := int64(2); i <= 3; i++ {
				header = &types.Header{Number: big.NewInt(i), Time: genesis + 2*duration + 1, Coinbase: miner}
				if got := chainTallyEpochForHeader(header); got != 2 {
					t.Fatalf("regressing header maps to epoch %d, want 2", got)
				}
				engine.updateStakingTallies(nil, header, statedb, big.NewInt(100))
				if got := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentEpoch).Big().Uint64(); got != 5 {
					t.Fatalf("native epoch regressed to %d", got)
				}
				if statedb.GetState(params.ChainTallyAddr, params.SlotCurrentTallyBase) != base ||
					statedb.GetState(params.ChainTallyAddr, params.SlotLastTallyBase) != lastBase {
					t.Fatal("timestamp regression rotated a tally base")
				}
				if got := statedb.GetState(params.ChainTallyAddr, calculateMappingSlot(miner, base)).Big().Int64(); got != i*100 {
					t.Fatalf("active-epoch reward = %d, want %d", got, i*100)
				}
				if statedb.GetState(params.ChainTallyAddr, calculateMappingSlot(miner, closedBase)) != (common.Hash{}) {
					t.Fatal("closed epoch received work after timestamp regression")
				}
			}
		})
	}
}
