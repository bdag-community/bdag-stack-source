package model

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Notify interface manage message announce & relay & notification between mempool, websocket, gbt long pull
// and rpc server.
type Notify interface {
	AnnounceNewTransactions(newTxs []*types.TxDesc, filters []peer.ID)
	RelayInventory(block *types.SerializedBlock, flags uint32, source *peer.ID)
	BroadcastMessage(data any)
	TransactionConfirmed(tx *etypes.Transaction)
	TransactionsConfirmed(txs []*etypes.Transaction)
	AddRebroadcastInventory(newTxs []*types.TxDesc)
	RelayTransaction(tx *etypes.Transaction) error
}
