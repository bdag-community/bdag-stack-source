package blockchain

import (
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
)

// dbFetchBlockByHash uses an existing database transaction to retrieve the raw
// block for the provided hash, deserialize it, retrieve the appropriate height
// from the index, and return a dcrutil.Block with the height set.
func dbFetchBlockByHash(db model.DataBase, hash *common.Hash) (*types.SerializedBlock, error) {
	return db.GetBlock(hash)
}

// dbFetchHeaderByHash uses an existing database transaction to retrieve the
// block header for the provided hash.
func dbFetchHeaderByHash(db model.DataBase, hash *common.Hash) (*types.BlockHeader, error) {
	return db.GetHeader(hash)
}

// dbMaybeStoreBlock stores the provided block in the database if it's not
// already there.
func dbMaybeStoreBlock(db model.DataBase, block *types.SerializedBlock) error {
	err := db.PutBlock(block)
	if err != nil {
		return err
	}
	return nil
}
