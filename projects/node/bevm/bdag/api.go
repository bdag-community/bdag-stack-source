package bdag

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

type PublicBdagChainAPI struct {
	mc *BdagChain
}

func NewPublicBdagChainAPI(mc *BdagChain) *PublicBdagChainAPI {
	return &PublicBdagChainAPI{mc}
}

func (api *PublicBdagChainAPI) GetBdagChainInfo() (any, error) {
	mi := BdagChainInfo{
		BdagVer:   Version,
		EvmVer:    api.mc.chain.Config().Node.Version,
		ChainID:   api.mc.chain.Config().Eth.Genesis.Config.ChainID.Uint64(),
		NetworkID: api.mc.chain.Config().Eth.NetworkId,
	}
	if len(api.mc.chain.Config().Node.IPCEndpoint()) > 0 {
		mi.IPC = api.mc.chain.Config().Node.IPCEndpoint()
	}
	if len(api.mc.chain.Config().Node.HTTPHost) > 0 {
		mi.HTTP = api.mc.chain.Config().Node.HTTPEndpoint()
	}
	if len(api.mc.chain.Config().Node.WSHost) > 0 {
		mi.WS = api.mc.chain.Config().Node.WSEndpoint()
	}
	mi.EVMRecovery = api.observeLocalEVMRecovery(context.Background())
	return mi, nil
}

type BdagChainInfo struct {
	BdagVer     string                       `json:"bdagver"`
	EvmVer      string                       `json:"evmver"`
	ChainID     uint64                       `json:"chainid"`
	NetworkID   uint64                       `json:"networkid"`
	IPC         string                       `json:"ipc,omitempty"`
	HTTP        string                       `json:"http,omitempty"`
	WS          string                       `json:"ws,omitempty"`
	EVMRecovery *LocalEVMRecoveryDiagnostics `json:"evm_recovery,omitempty"`
}

type LocalEVMRecoveryDiagnostics struct {
	Head       LocalEVMRecoveryReport `json:"head"`
	Held       bool                   `json:"held"`
	HoldReason string                 `json:"hold_reason,omitempty"`
}

// observeLocalEVMRecovery reads only local execution state and the existing
// recovery hold provider. It never changes recovery state, canonicality,
// checkpoints, markers, datasets, or signing authority.
func (api *PublicBdagChainAPI) observeLocalEVMRecovery(ctx context.Context) *LocalEVMRecoveryDiagnostics {
	diagnostics := &LocalEVMRecoveryDiagnostics{Head: LocalEVMRecoveryReport{
		State: LocalEVMRecoveryUnknown, Reason: "local EVM observation is unavailable",
	}}
	if api == nil || api.mc == nil || api.mc.chain == nil || api.mc.chain.Backend() == nil || api.mc.chain.Ether() == nil {
		return diagnostics
	}
	backend := api.mc.chain.Backend()
	if ctx == nil {
		ctx = context.Background()
	}
	safeHeader, err := backend.HeaderByNumber(ctx, rpc.LatestBlockNumber)
	unsafeHeader := backend.CurrentHeader()
	if err != nil {
		safeHeader = nil
	}
	chain := api.mc.chain.Ether().BlockChain()
	safeAvailable := localEVMHeaderStateAvailable(chain, safeHeader)
	unsafeAvailable := localEVMHeaderStateAvailable(chain, unsafeHeader)
	diagnostics.Head = AssessLocalEVMHeadObservations(
		LocalEVMHeadObservation{Header: safeHeader, Known: err == nil && safeHeader != nil, Available: safeAvailable},
		LocalEVMHeadObservation{Header: unsafeHeader, Known: unsafeHeader != nil, Available: unsafeAvailable},
	)

	if api.mc.consensus != nil {
		if provider, ok := api.mc.consensus.BlockChain().(interface{ LocalEVMRecoveryStatus() (bool, string) }); ok {
			diagnostics.Held, diagnostics.HoldReason = provider.LocalEVMRecoveryStatus()
		}
	}

	// Re-read the same local identities after state observation. If the head or
	// recovery hold changed during the diagnostic read, report unknown rather
	// than presenting a mixed-height snapshot.
	safeAfter, safeErrAfter := backend.HeaderByNumber(ctx, rpc.LatestBlockNumber)
	unsafeAfter := backend.CurrentHeader()
	heldAfter, reasonAfter := diagnostics.Held, diagnostics.HoldReason
	if api.mc.consensus != nil {
		if provider, ok := api.mc.consensus.BlockChain().(interface{ LocalEVMRecoveryStatus() (bool, string) }); ok {
			heldAfter, reasonAfter = provider.LocalEVMRecoveryStatus()
		}
	}
	if safeErrAfter != nil || !sameLocalEVMHeader(safeHeader, safeAfter) || !sameLocalEVMHeader(unsafeHeader, unsafeAfter) || heldAfter != diagnostics.Held || reasonAfter != diagnostics.HoldReason {
		diagnostics.Head.State = LocalEVMRecoveryUnknown
		diagnostics.Head.Reason = "local EVM heads or recovery hold changed during observation"
	}
	return diagnostics
}

func localEVMHeaderStateAvailable(chain *core.BlockChain, header *types.Header) bool {
	if chain == nil || header == nil || header.Root == (common.Hash{}) || !chain.HasState(header.Root) {
		return false
	}
	_, err := chain.StateAt(header.Root)
	return err == nil
}

func sameLocalEVMHeader(left, right *types.Header) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Number != nil && right.Number != nil && left.Number.Cmp(right.Number) == 0 && left.Hash() == right.Hash() && left.Root == right.Root
}
