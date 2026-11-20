package node

import (
	"errors"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type nodeInfoReader struct {
	best      *blockchain.BestState
	header    types.BlockHeader
	err       error
	reads     int
	requested common.Hash
}

func (r *nodeInfoReader) BestSnapshot() *blockchain.BestState { return r.best }
func (r *nodeInfoReader) HeaderByHash(hash *common.Hash) (types.BlockHeader, error) {
	r.reads++
	r.requested = *hash
	return r.header, r.err
}

func TestNodeInfoSnapshotUsesOneExactTipHeader(t *testing.T) {
	for _, algorithm := range []pow.PowType{pow.SCRYPT, pow.BLAKE2S, pow.BDAGXKECCAKV1} {
		t.Run(pow.GetPowName(algorithm), func(t *testing.T) {
			cfg := params.TestNetParams
			cfg.PowConfig = &pow.PowConfig{
				ScryptPowLimit:            pow.CompactToBig(0x1f0fffff),
				ScryptPowLimitBits:        0x1f0fffff,
				Blake2sPowLimit:           pow.CompactToBig(0x1f00ffff),
				Blake2sPowLimitBits:       0x1f00ffff,
				BdagXKeccakV1PowLimit:     pow.CompactToBig(0x1f000fff),
				BdagXKeccakV1PowLimitBits: 0x1f000fff,
			}
			reader := &nodeInfoReader{
				best:   &blockchain.BestState{Hash: common.HexToHash("0x1234"), Bits: 0x1e00ffff},
				header: types.BlockHeader{Difficulty: 0x1e00ffff, Pow: pow.GetInstance(algorithm, 0, 0, nil)},
			}
			best, target, gotType, err := nodeInfoSnapshot(reader)
			require.NoError(t, err)
			require.Same(t, reader.best, best)
			require.Equal(t, reader.best.Hash, reader.requested)
			require.Equal(t, 1, reader.reads, "metadata must not traverse historical mining algorithms")
			require.Equal(t, algorithm, gotType)
			require.Equal(t, pow.CompactToBig(reader.header.Difficulty), target)
			difficulty, err := nodeInfoDifficulty(target, gotType, &cfg)
			require.NoError(t, err)
			require.Equal(t, getDifficultyRatio(target, &cfg, algorithm), difficulty.CurrentDiff)
			require.Positive(t, difficulty.CurrentDiff)
		})
	}
}

func TestNodeInfoSnapshotRejectsUnavailableMetadata(t *testing.T) {
	tests := []struct {
		name   string
		reader nodeInfoReader
	}{
		{"snapshot", nodeInfoReader{}},
		{"header", nodeInfoReader{best: &blockchain.BestState{}, err: errors.New("header unavailable")}},
		{"pow", nodeInfoReader{best: &blockchain.BestState{}}},
		{"zero target", nodeInfoReader{best: &blockchain.BestState{}, header: types.BlockHeader{Pow: pow.GetInstance(pow.SCRYPT, 0, 0, nil)}}},
		{"negative target", nodeInfoReader{best: &blockchain.BestState{}, header: types.BlockHeader{Difficulty: 0x1e80ffff, Pow: pow.GetInstance(pow.SCRYPT, 0, 0, nil)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := nodeInfoSnapshot(&test.reader)
			require.Error(t, err)
			require.LessOrEqual(t, test.reader.reads, 1)
		})
	}
}
