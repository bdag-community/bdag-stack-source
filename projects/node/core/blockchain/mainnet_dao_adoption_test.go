package blockchain

import (
	"math/big"
	"testing"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestMainnetDAOAdoptionProofReadsStoredNativeTip(t *testing.T) {
	previous := params.ActiveNetParams
	params.ActiveNetParams = &params.MainNetParam
	t.Cleanup(func() { params.ActiveNetParams = previous })
	f := newLocalRecoveryFixture(t)
	require.Nil(t, f.bc.mainnetDAOAdoptionProof(), "genesis alone is not a paired adoption proof")
	data := rollbackTestBlockData{hash: common.BigToHash(big.NewInt(99)), parents: []*common.Hash{f.genesis.GetHash()}, mainParent: f.genesis.GetHash(), timestamp: 2}
	_, _, child, _ := f.bc.bd.AddBlock(data)
	require.NotNil(t, child)
	f.commit(t, child)
	// Match startup: reconstruct the DAG object without loading it into memory.
	f.bc.bd = blockdag.New(blockdag.PHANTOM, -1, f.db, nil)
	proof := f.bc.mainnetDAOAdoptionProof()
	require.NotNil(t, proof)
	require.EqualValues(t, 1, proof.NativeLayer)
	require.Equal(t, f.reader.head.Hash(), proof.EVMHash)
	require.Equal(t, f.reader.head.Root, proof.EVMRoot)
	require.Equal(t, f.reader.head.Number.Uint64(), proof.EVMNumber)
	params.ActiveNetParams = &params.TestNetParam
	require.Nil(t, f.bc.mainnetDAOAdoptionProof())
}
