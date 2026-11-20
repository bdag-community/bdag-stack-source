package json

// ChainIdentitySchema identifies the immutable network and genesis values
// used when comparing a Core node with an independently produced dataset.
const ChainIdentitySchema = "bdag.chain-identity.v1"

// ChainIdentityResult is intentionally limited to static chain identity. It
// must not acquire fields that depend on synchronization, mining, or runtime
// configuration.
type ChainIdentityResult struct {
	Schema             string `json:"schema"`
	Network            string `json:"network"`
	NativeNetworkMagic string `json:"native_network_magic"`
	NativeGenesisHash  string `json:"native_genesis_hash"`
	EVMChainID         string `json:"evm_chain_id"`
	EVMGenesisHash     string `json:"evm_genesis_hash"`
}
