package index

import (
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/schollz/progressbar/v3"
)

const (
	invalidTxIndexName = "invalid tx index"
)

type InvalidTxIndex struct {
	consensus model.Consensus
}

func (idx *InvalidTxIndex) Name() string {
	return invalidTxIndexName
}

func (idx *InvalidTxIndex) Init() error {
	log.Info("Init", "index", idx.Name())
	//
	bc := idx.consensus.BlockChain()
	mainOrder := bc.GetMainOrder()
	mainHash := bc.GetBlockHashByOrder(mainOrder)
	if mainHash == nil {
		return fmt.Errorf("No block in order:%d", mainOrder)
	}
	if idx.DB().IsInvalidTxIdxEmpty() {
		return idx.caughtUpFrom(0)
	} else {
		tipOrder, tipHash, err := idx.DB().GetInvalidTxIdxTip()
		if err != nil {
			return err
		}
		if tipHash == nil {
			return nil
		}
		if tipOrder != uint64(mainOrder) || mainHash.Cmp(*tipHash) != 0 {
			if tipOrder < uint64(mainOrder) {
				// It shows that the data is encounter
				bh := bc.GetBlockHashByOrder(uint(tipOrder))
				if bh != nil && bh.Cmp(*tipHash) == 0 {
					return idx.caughtUpFrom(uint(tipOrder + 1))
				}
			}
			return fmt.Errorf("vm block index(%s:%d) is out of synchronization(%s:%d) and can only be deleted and rebuilt:index --dropvmblock",
				tipHash, tipOrder, mainHash, mainOrder)
		}
		log.Info(fmt.Sprintf("Current %s tip:%s,%d", idx.Name(), tipHash.String(), tipOrder))
	}
	return nil
}

func (idx *InvalidTxIndex) caughtUpFrom(startOrder uint) error {
	bc := idx.consensus.BlockChain()
	mainOrder := bc.GetMainOrder()
	mainHash := bc.GetBlockHashByOrder(mainOrder)
	if startOrder > mainOrder {
		return nil
	}
	if mainOrder > 0 {
		log.Info(fmt.Sprintf("Start caught up %s from (order:%d) to tip(hash:%s,order:%d)", idx.Name(), startOrder, mainHash, mainOrder))
		logLvl := l.Glogger().GetVerbosity()
		bar := progressbar.Default(int64(mainOrder-startOrder), fmt.Sprintf("%s:", idx.Name()))
		l.Glogger().Verbosity(l.LvlCrit)
		for i := uint(startOrder); i <= mainOrder; i++ {
			bar.Add(1)
			if i == 0 {
				continue
			}
			blk := bc.GetBlockByOrder(uint64(i))
			if blk == nil {
				return fmt.Errorf("No DAG block:%d", i)
			}
			if !blk.GetState().GetStatus().KnownInvalid() {
				continue
			}
			block, err := bc.FetchBlockByHash(blk.GetHash())
			if err != nil {
				return err
			}
			err = idx.ConnectBlock(block, blk)
			if err != nil {
				return err
			}
		}
		l.Glogger().Verbosity(logLvl)
	}
	log.Info(fmt.Sprintf("Current %s tip:%s,%d", idx.Name(), mainHash.String(), mainOrder))
	return idx.UpdateMainTip(mainHash, uint64(mainOrder))
}

func (idx *InvalidTxIndex) ConnectBlock(sblock *types.SerializedBlock, block model.Block) error {
	if !block.GetState().GetStatus().KnownInvalid() {
		return nil
	}
	return idx.DB().PutInvalidTxs(sblock, block)
}

func (idx *InvalidTxIndex) DisconnectBlock(sblock *types.SerializedBlock, block model.Block) error {
	return idx.DB().DeleteInvalidTxs(sblock, block)
}

func (idx *InvalidTxIndex) UpdateMainTip(bh *common.Hash, order uint64) error {
	return idx.DB().PutInvalidTxIdxTip(order, bh)
}

func (idx *InvalidTxIndex) Get(txid *common.Hash) (*ethtypes.Transaction, error) {
	return nil, nil
}

func (idx *InvalidTxIndex) DB() model.DataBase {
	return idx.consensus.DatabaseContext()
}

func NewInvalidTxIndex(consensus model.Consensus) *InvalidTxIndex {
	log.Info(fmt.Sprintf("%s is enabled", invalidTxIndexName))

	return &InvalidTxIndex{
		consensus: consensus,
	}
}
