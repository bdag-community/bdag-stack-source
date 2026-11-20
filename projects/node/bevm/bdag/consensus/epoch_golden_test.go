package consensus

import (
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

// Golden vectors match floor((timestamp-genesis)/epochSeconds), shared with
// StakingV2.clock and the Reader. Height must not affect timestamp epochs.
func TestMinimalEpochGoldenVectors(t *testing.T) {
	old := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = old })
	qparams.ActiveNetParams = &qparams.DevNetParam
	genesis := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	require.EqualValues(t, 60, qparams.ActiveNetParams.StakingEpochDurationSeconds)
	for _, v := range []struct {
		delta int64
		epoch uint64
	}{{-1, 0}, {0, 0}, {1, 0}, {59, 0}, {60, 1}, {61, 1}, {119, 1}, {120, 2}, {604799, 10079}, {604800, 10080}} {
		for _, n := range []int64{1, 53, 12345678} {
			h := &types.Header{Number: big.NewInt(n), Time: uint64(int64(genesis) + v.delta)}
			require.Equal(t, v.epoch, stakingEpochForHeader(h))
			require.Equal(t, v.epoch, chainTallyEpochForHeader(h))
		}
	}
	qparams.ActiveNetParams = &qparams.MainNetParam
	genesis = uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	for _, v := range []struct{ delta, epoch uint64 }{{604799, 0}, {604800, 1}, {1209600, 2}} {
		require.Equal(t, v.epoch, stakingEpochForHeader(&types.Header{Number: big.NewInt(20), Time: genesis + v.delta}))
	}
}

func TestTestnetPreservesLegacyFinancialEpochs(t *testing.T) {
	old := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = old })
	qparams.ActiveNetParams = &qparams.TestNetParam
	genesis := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())

	require.Zero(t, qparams.ActiveNetParams.StakingEpochDurationSeconds)
	for _, v := range []struct {
		delta uint64
		epoch uint64
	}{{0, 0}, {59, 0}, {60, 1}, {119, 1}, {120, 2}} {
		header := &types.Header{Number: big.NewInt(5_083), Time: genesis + v.delta}
		require.Zero(t, stakingEpochForHeader(header))
		require.Zero(t, chainTallyEpochForHeader(header))
	}
}
