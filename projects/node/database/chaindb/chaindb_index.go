package chaindb

import (
	"encoding/binary"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	bdag "github.com/BlockdagNetworkLabs/bdag/database/common"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/services/index"
	"github.com/ethereum/go-ethereum/common"
	ethtype "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
)

func (cdb *ChainDB) PutTxIdxEntrys(sblock *types.SerializedBlock, block model.Block) error {
	if cdb.diff != nil {
		return cdb.diff.PutTxIdxEntrys(sblock, block)
	}

	return rawdb.WriteTxLookupEntriesByBlock(cdb.db, sblock, uint64(block.GetID()))
}

func (cdb *ChainDB) GetTxIdxEntry(id *common.Hash, verbose bool) (*ethtype.Transaction, *common.Hash, error) {
	if cdb.diff != nil {
		return cdb.diff.GetTxIdxEntry(id, verbose)
	}
	if !verbose {
		blockID := rawdb.ReadTxLookupEntry(cdb.db, id)
		if blockID == nil {
			return nil, nil, nil
		}
		blockhash, err := blockdag.DBGetDAGBlockHashByID(cdb, *blockID)
		if err != nil {
			return nil, nil, err
		}
		return nil, blockhash, nil
	}
	tx, _, blockhash, _ := rawdb.ReadTransaction(cdb.db, id)
	return tx, blockhash, nil
}

func (cdb *ChainDB) DeleteTxIdxEntrys(block *types.SerializedBlock) error {
	if cdb.diff != nil {
		return cdb.diff.DeleteTxIdxEntrys(block)
	}
	batch := cdb.db.NewBatch()
	for _, tx := range block.Transactions() {
		txhash := tx.Hash()
		_, blockHash, _ := cdb.GetTxIdxEntry(&txhash, false)
		if blockHash != nil && blockHash.Cmp(*block.Hash()) != 0 {
			continue
		}
		err := rawdb.DeleteTxLookupEntry(batch, &txhash)
		if err != nil {
			return err
		}
	}
	return batch.Write()
}

func (cdb *ChainDB) IsInvalidTxIdxEmpty() bool {
	return rawdb.IsInvalidTxEmpty(cdb.db)
}

func (cdb *ChainDB) GetInvalidTxIdxTip() (uint64, *common.Hash, error) {
	return 0, nil, nil
}

func (cdb *ChainDB) PutInvalidTxIdxTip(order uint64, bh *common.Hash) error {
	return nil
}

func (cdb *ChainDB) PutInvalidTxs(sblock *types.SerializedBlock, block model.Block) error {
	batch := cdb.db.NewBatch()
	err := rawdb.WriteInvalidTxLookupEntriesByBlock(cdb.db, sblock, uint64(block.GetID()))
	if err != nil {
		return err
	}
	return batch.Write()
}

func (cdb *ChainDB) DeleteInvalidTxs(sblock *types.SerializedBlock, block model.Block) error {
	batch := cdb.db.NewBatch()
	for _, tx := range sblock.Transactions() {
		txhash := tx.Hash()
		err := rawdb.DeleteTxLookupEntry(batch, &txhash)
		if err != nil {
			return err
		}
	}
	return batch.Write()
}

func (cdb *ChainDB) GetInvalidTx(id *common.Hash) (*ethtype.Transaction, error) {
	tx, _, _, _ := rawdb.ReadInvalidTransaction(cdb.db, id)
	return tx, nil
}

func (cdb *ChainDB) GetAddrIdxTip() (*common.Hash, uint, error) {
	return rawdb.ReadAddrIdxTip(cdb.db)
}

func (cdb *ChainDB) PutAddrIdxTip(bh *common.Hash, order uint) error {
	return rawdb.WriteAddrIdxTip(cdb.db, bh, order)
}

func (cdb *ChainDB) PutAddrIdx(sblock *types.SerializedBlock, block model.Block, stxos [][]byte) error {
	// Build all of the address to transaction mappings in a local map.
	addrsToTxns := make(index.WriteAddrIdxData)
	// index.AddrIndexBlock(addrsToTxns, sblock, stxos)
	b := &bucket{db: cdb.db}
	// Add all of the index entries for each address.
	for addrKey, txIdxs := range addrsToTxns {
		for _, txIdx := range txIdxs {
			// Switch to using the newest block ID for the stake transactions,
			// since these are not from the parent. Offset the index to be
			// correct for the location in this given block.
			err := index.DBPutAddrIndexEntry(b, addrKey,
				uint32(block.GetID()), types.TxLoc{TxStart: txIdx, TxLen: 0}, rawdb.AddridxPrefix)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (cdb *ChainDB) GetTxForAddress(addr common.Address, numToSkip, numRequested uint32, reverse bool) ([]*bdag.RetrievedTx, uint32, error) {
	addrKey, err := index.AddrToKey(addr)
	if err != nil {
		return nil, 0, err
	}
	fetchBlockHash := func(id []byte) (*common.Hash, error) {
		// Deserialize and populate the result.
		blockid := uint64(binary.LittleEndian.Uint32(id))
		return blockdag.DBGetDAGBlockHashByID(cdb, blockid)
	}
	fetchAddrLevelData := func(key []byte) []byte {
		levelData, _ := cdb.db.Get(key)
		return levelData
	}
	regions, dbSkipped, err := index.DBFetchAddrIndexEntries(fetchBlockHash, fetchAddrLevelData,
		addrKey, numToSkip, numRequested, reverse, rawdb.AddridxPrefix)
	if err != nil {
		return nil, 0, err
	}
	addressTxns := []*bdag.RetrievedTx{}
	for _, r := range regions {
		block, err := cdb.GetBlock(r.Hash)
		if err != nil {
			return nil, 0, err
		}
		addressTxns = append(addressTxns, &bdag.RetrievedTx{
			BlkHash: r.Hash,
			Tx:      block.Transactions()[r.Offset],
		})
	}
	return addressTxns, dbSkipped, nil
}

func (cdb *ChainDB) DeleteAddrIdx(sblock *types.SerializedBlock, stxos [][]byte) error {
	// Build all of the address to transaction mappings in a local map.
	addrsToTxns := make(index.WriteAddrIdxData)
	// index.AddrIndexBlock(addrsToTxns, sblock, stxos)
	b := &bucket{db: cdb.db}

	for addrKey, txIdxs := range addrsToTxns {
		err := index.DBRemoveAddrIndexEntries(b, addrKey, len(txIdxs), rawdb.AddridxPrefix)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cdb *ChainDB) CleanAddrIdx(finish bool) error {
	if finish {
		return nil
	}
	return rawdb.CleanAddrIdx(cdb.DB())
}

type bucket struct {
	db ethdb.Database
}

func (b *bucket) Get(key []byte) []byte {
	value, _ := b.db.Get(key)
	return value
}

func (b *bucket) Put(key []byte, value []byte) error {
	return b.db.Put(key, value)
}

func (b *bucket) Delete(key []byte) error {
	return b.db.Delete(key)
}
