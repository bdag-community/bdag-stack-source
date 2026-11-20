package chaindb

import (
	"github.com/BlockdagNetworkLabs/bdag/database/common"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

func (cdb *ChainDB) TryUpgrade(di *common.DatabaseInfo, interrupt <-chan struct{}) error {
	if di.Version() == common.CurrentDatabaseVersion {
		genesisTxs := params.ActiveNetParams.GenesisBlock.Transactions()
		if len(genesisTxs) == 0 {
			log.Info("Skipping genesis transaction re-indexing for empty block in EVM model")
			return nil
		}
		// Check and re-index if needed.
		txId := genesisTxs[0].Hash()
		_, blockHash, err := cdb.GetTxIdxEntry(&txId, false)
		if err != nil {
			return err
		}
		if blockHash == nil {
			log.Info("Re-indexing genesis transaction for database")
			return rawdb.WriteTxLookupEntriesByBlock(cdb.db, params.ActiveNetParams.GenesisBlock, 0)
		}
	}
	return nil
}
