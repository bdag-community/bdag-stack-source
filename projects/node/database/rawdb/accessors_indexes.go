package rawdb

import (
	"encoding/binary"
	"math"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
)

func ReadTxLookupEntry(db ethdb.Reader, hash *common.Hash) *uint64 {
	data, err := db.Get(txLookupKey(hash))
	if len(data) == 0 {
		log.Trace("tx lookup entry", "err", err.Error())
		return nil
	}
	id := binary.BigEndian.Uint64(data)
	return &id
}

func WriteTxLookupEntry(db ethdb.KeyValueWriter, hash *common.Hash, id uint64) error {
	var serializedID [8]byte
	binary.BigEndian.PutUint64(serializedID[:], id)
	return db.Put(txLookupKey(hash), serializedID[:])
}

func WriteTxLookupEntriesByBlock(db ethdb.KeyValueWriter, block *types.SerializedBlock, id uint64) error {
	var serializedID [8]byte
	binary.BigEndian.PutUint64(serializedID[:], id)
	for _, tx := range block.Transactions() {
		// if tx.IsDuplicate {
		// 	continue
		// }
		txhash := tx.Hash()
		err := db.Put(txLookupKey(&txhash), serializedID[:])
		if err != nil {
			return err
		}
	}
	return nil
}

func DeleteTxLookupEntry(db ethdb.KeyValueWriter, hash *common.Hash) error {
	return db.Delete(txLookupKey(hash))
}

func ReadTransaction(db ethdb.Reader, hash *common.Hash) (*etypes.Transaction, uint64, *common.Hash, int) {
	blockID := ReadTxLookupEntry(db, hash)
	if blockID == nil {
		return nil, 0, nil, 0
	}
	body := ReadBodyByID(db, *blockID)
	if body == nil {
		log.Error("Transaction referenced missing", "blockID", *blockID, "txHash", hash.String())
		return nil, 0, nil, 0
	}
	for txIndex, tx := range body.Transactions() {
		if tx.Hash().Cmp(*hash) == 0 {
			return tx, *blockID, body.Hash(), txIndex
		}
	}
	log.Error("Transaction not found", "blockID", *blockID, "txHash", hash.String())
	return nil, 0, nil, 0
}

// invalid tx index
func ReadInvalidTxLookupEntry(db ethdb.Reader, hash *common.Hash) *uint64 {
	data, err := db.Get(invalidtxLookupKey(hash))
	if len(data) == 0 {
		log.Error(err.Error())
		return nil
	}
	id := binary.BigEndian.Uint64(data)
	return &id
}

func writeInvalidTxLookupEntry(db ethdb.KeyValueWriter, hash *common.Hash, id uint64) error {
	var serializedID [8]byte
	binary.BigEndian.PutUint64(serializedID[:], id)
	return db.Put(invalidtxLookupKey(hash), serializedID[:])
}

func WriteInvalidTxLookupEntriesByBlock(db ethdb.KeyValueWriter, block *types.SerializedBlock, id uint64) error {
	var serializedID [8]byte
	binary.BigEndian.PutUint64(serializedID[:], id)
	for _, tx := range block.Transactions() {
		txhash := tx.Hash()
		err := db.Put(invalidtxLookupKey(&txhash), serializedID[:])
		if err != nil {
			return err
		}
	}
	return nil
}

func DeleteInvalidTxLookupEntry(db ethdb.KeyValueWriter, hash *common.Hash) error {
	return db.Delete(invalidtxLookupKey(hash))
}

func ReadInvalidTransaction(db ethdb.Reader, hash *common.Hash) (*etypes.Transaction, uint64, *common.Hash, int) {
	blockID := ReadInvalidTxLookupEntry(db, hash)
	if blockID == nil {
		return nil, 0, nil, 0
	}
	body := ReadBodyByID(db, *blockID)
	if body == nil {
		log.Error("Transaction referenced missing", "blockID", *blockID, "txHash", hash.String())
		return nil, 0, nil, 0
	}
	for txIndex, tx := range body.Transactions() {
		if tx.Hash().Cmp(*hash) == 0 {
			return tx, *blockID, body.Hash(), txIndex
		}
	}
	log.Error("Transaction not found", "blockID", *blockID, "txHash", hash.String())
	return nil, 0, nil, 0
}

func IsInvalidTxEmpty(db ethdb.Iteratee) bool {
	it := db.NewIterator(invalidtxLookupPrefix, nil)
	for it.Next() {
		return false
	}
	return true
}

func CleanInvalidTxs(db ethdb.Database) error {
	it := db.NewIterator(invalidtxLookupPrefix, nil)
	total := 0
	defer func() {
		log.Debug("Clean invalid transactions", "total", total)
	}()
	for it.Next() {
		err := db.Delete(it.Key())
		if err != nil {
			return err
		}
		total++
	}
	return nil
}

// addr index
func ReadAddrIdxTip(db ethdb.Reader) (*common.Hash, uint, error) {
	serialized, _ := db.Get(addridxTipKey)
	if len(serialized) < common.HashLength+4 {
		return hash.ZeroHash.ToEVMHashPtr(), math.MaxUint32, nil
	}

	var h common.Hash
	copy(h[:], serialized[:common.HashLength])
	order := uint32(binary.BigEndian.Uint32(serialized[common.HashLength:]))
	return &h, uint(order), nil
}

func WriteAddrIdxTip(db ethdb.KeyValueWriter, bh *common.Hash, order uint) error {
	serialized := make([]byte, common.HashLength+4)
	copy(serialized, bh[:])
	binary.BigEndian.PutUint32(serialized[common.HashLength:], uint32(order))
	return db.Put(addridxTipKey, serialized)
}

func CleanAddrIdx(db ethdb.Database) error {
	err := db.Delete(addridxTipKey)
	if err != nil {
		return err
	}
	it := db.NewIterator(AddridxPrefix, nil)
	total := 0
	defer func() {
		log.Debug("Clean addr index", "total", total)
	}()
	for it.Next() {
		err := db.Delete(it.Key())
		if err != nil {
			return err
		}
		total++
	}
	return nil
}
