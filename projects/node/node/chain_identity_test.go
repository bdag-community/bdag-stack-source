package node

import (
	stdjson "encoding/json"
	"testing"

	bevmgenesis "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/stretchr/testify/require"
)

func TestGetChainIdentityAllNetworks(t *testing.T) {
	tests := []struct {
		name       string
		network    string
		magic      string
		chainID    string
		evmGenesis string
	}{
		{name: "mainnet", network: "mainnet", magic: "0xb4c3dce8", chainID: "1404", evmGenesis: bevmgenesis.MainNetGenesisHash},
		{name: "mainnet-isolated", network: "mainnet-isolated", magic: "0xb4c3dce8", chainID: "1404", evmGenesis: bevmgenesis.MainNetGenesisHash},
		{name: "testnet", network: "testnet", magic: "0x35e0c424", chainID: "1043", evmGenesis: bevmgenesis.TestNetGenesisHash},
		{name: "privnet", network: "privnet", magic: "0xf1eb0001", chainID: "1048", evmGenesis: bevmgenesis.PrivNetGenesisHash},
		{name: "mixnet", network: "mixnet", magic: "0xc459b247", chainID: "1046", evmGenesis: bevmgenesis.MixNetGenesisHash},
		{name: "devnet", network: "devnet", magic: "0x80808080", chainID: "8008", evmGenesis: bevmgenesis.DevNetGenesisHash},
	}

	previous := params.ActiveNetParams
	t.Cleanup(func() { params.ActiveNetParams = previous })
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.True(t, setActiveParams(test.name))

			result, err := (&PublicBlockChainAPI{}).GetChainIdentity()
			require.NoError(t, err)
			identity, ok := result.(*corejson.ChainIdentityResult)
			require.True(t, ok)
			require.Equal(t, corejson.ChainIdentitySchema, identity.Schema)
			require.Equal(t, test.network, identity.Network)
			require.Equal(t, test.magic, identity.NativeNetworkMagic)
			require.Equal(t, params.ActiveNetParams.GenesisHash.String(), identity.NativeGenesisHash)
			require.Equal(t, test.chainID, identity.EVMChainID)
			require.Equal(t, test.evmGenesis, identity.EVMGenesisHash)

			encoded, err := stdjson.Marshal(identity)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, stdjson.Unmarshal(encoded, &fields))
			require.Len(t, fields, 6, "identity must not expose runtime, address, path, or configuration fields")
		})
	}
}

func setActiveParams(name string) bool {
	for _, candidate := range params.AllNetParams {
		if candidate.Name == name {
			params.ActiveNetParams = candidate
			return true
		}
	}
	return false
}

func TestGetChainIdentityDoesNotRequireNodeOrMiner(t *testing.T) {
	previous := params.ActiveNetParams
	params.ActiveNetParams = &params.MainNetParam
	t.Cleanup(func() { params.ActiveNetParams = previous })

	result, err := (&PublicBlockChainAPI{}).GetChainIdentity()
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestGetChainIdentityRejectsUnavailableOrInconsistentParams(t *testing.T) {
	previous := params.ActiveNetParams
	t.Cleanup(func() { params.ActiveNetParams = previous })

	params.ActiveNetParams = nil
	result, err := (&PublicBlockChainAPI{}).GetChainIdentity()
	require.Error(t, err)
	require.Nil(t, result)

	params.ActiveNetParams = &params.MainNetParam
	originalName := params.MainNetParam.Name
	params.MainNetParam.Name = "testnet"
	result, err = (&PublicBlockChainAPI{}).GetChainIdentity()
	require.Error(t, err)
	require.Nil(t, result)
	params.MainNetParam.Name = originalName
}
