package rpc

import (
	"fmt"
	"math"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	eparams "github.com/ethereum/go-ethereum/params"
)

// MaxInvPerMsg is the maximum number of inventory vectors that can be in a
// single inv message.
const MaxInvPerMsg = 50000

// ErrRescanReorg defines the error that is returned when an unrecoverable
// reorganize is detected during a rescan.
var ErrRescanReorg = cmds.ErrRPCDatabase

// recoverFromReorg attempts to recover from a detected reorganize during a
// rescan.  It fetches a new range of block shas from the database and
// verifies that the new range of blocks is on the same fork as a previous
// range of blocks.  If this condition does not hold true, the JSON-RPC error
// for an unrecoverable reorganize is returned.
func recoverFromReorg(chain *blockchain.BlockChain, minBlock, maxBlock uint64,
	lastBlock *common.Hash) ([]common.Hash, error) {

	hashList, err := chain.OrderRange(minBlock, maxBlock)
	if err != nil {
		log.Error(fmt.Sprintf("Error looking up block range: %v", err))
		return nil, cmds.ErrRPCDatabase
	}
	if lastBlock == nil || len(hashList) == 0 {
		return hashList, nil
	}

	blk := chain.Blockdag().GetBlock(&hashList[0])
	if blk == nil {
		log.Error(fmt.Sprintf("Error looking up possibly reorged block: %v", hashList[0].String()))
		return nil, cmds.ErrRPCBlockNotFound
	}
	jsonErr := descendantBlock(chain, lastBlock, blk)
	if jsonErr != nil {
		return nil, jsonErr
	}
	return hashList, nil
}

// descendantBlock returns the appropriate JSON-RPC error if a current block
// fetched during a reorganize is not a direct child of the parent block hash.
func descendantBlock(chain *blockchain.BlockChain, lastBlockHash *common.Hash, curBlock blockdag.IBlock) error {
	preHash, err := chain.BlockHashByOrder(uint64(curBlock.GetOrder()) - 1)
	if err != nil {
		return fmt.Errorf("failed fetch order:%v", curBlock.GetOrder()-1)
	}
	if preHash.Cmp(*lastBlockHash) != 0 {
		err = fmt.Errorf("stopping rescan for reorged block %v (replaced by block %v)", lastBlockHash, preHash)
		log.Error(err.Error())
		return err
	}

	return nil
}

// scanBlockChunks executes a rescan in chunked stages. We do this to limit the
// amount of memory that we'll allocate to a given rescan. Every so often,
// we'll send back a rescan progress notification to the websockets client. The
// final block and block hash that we've scanned will be returned.
func scanBlockChunks(wsc *wsClient, cmd *cmds.RescanCmd, lookups *rescanKeys, minBlock,
	maxBlock uint64, chain *blockchain.BlockChain) (
	blockdag.IBlock, *common.Hash, *common.Hash, error) {

	// lastBlock and lastBlockHash track the previously-rescanned block.
	// They equal nil when no previous blocks have been rescanned.
	var (
		lastBlock     blockdag.IBlock
		lastBlockHash *common.Hash
		lastTxHash    *common.Hash
	)

	// A ticker is created to wait at least 10 seconds before notifying the
	// websocket client of the current progress completed by the rescan.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// Instead of fetching all block shas at once, fetch in smaller chunks
	// to ensure large rescans consume a limited amount of memory.
fetchRange:
	for minBlock < maxBlock {
		// Limit the max number of hashes to fetch at once to the
		// maximum number of items allowed in a single inventory.
		// This value could be higher since it's not creating inventory
		// messages, but this mirrors the limiting logic used in the
		// peer-to-peer protocol.
		maxLoopBlock := maxBlock
		if maxLoopBlock-minBlock > MaxInvPerMsg {
			maxLoopBlock = minBlock + MaxInvPerMsg
		}
		hashList, err := chain.OrderRange(minBlock, maxLoopBlock)
		if err != nil {
			log.Error(fmt.Sprintf("Error looking up block range: %v", err))
			return nil, nil, nil, cmds.ErrRPCBlockNotFound
		}
		if len(hashList) == 0 {
			// The rescan is finished if no blocks hashes for this
			// range were successfully fetched and a stop block
			// was provided.
			if maxBlock != math.MaxInt64 {
				break
			}

			// If the rescan is through the current block, set up
			// the client to continue to receive notifications
			// regarding all rescanned addresses and the current set
			// of unspent outputs.
			//
			// This is done safely by temporarily grabbing exclusive
			// access of the block manager.  If no more blocks have
			// been attached between this pause and the fetch above,
			// then it is safe to register the websocket client for
			// continuous notifications if necessary.  Otherwise,
			// continue the fetch loop again to rescan the new
			// blocks (or error due to an irrecoverable reorganize).
			best := wsc.server.BC.BestSnapshot()
			curHash := &best.Hash
			again := true
			if lastBlockHash == nil || *lastBlockHash == *curHash {
				again = false
			}
			if err != nil {
				log.Error(fmt.Sprintf("Error fetching best block "+
					"hash: %v", err))
				return nil, nil, nil, cmds.ErrRPCBlockNotFound
			}
			if again {
				continue
			}
			break
		}
	loopHashList:
		for i := range hashList {
			node := chain.Blockdag().GetBlock(&hashList[i])
			if node == nil {
				// If an absolute max block was specified, don't
				// attempt to handle the reorg.
				if maxBlock != math.MaxInt64 {
					log.Error(fmt.Sprintf("Stopping rescan for "+
						"reorged block %v",
						cmd.EndBlock))
					return nil, nil, nil, ErrRescanReorg
				}

				// If the lookup for the previously valid block
				// hash failed, there may have been a reorg.
				// Fetch a new range of block hashes and verify
				// that the previously processed block (if there
				// was any) still exists in the database.  If it
				// doesn't, we error.
				//
				// A goto is used to branch executation back to
				// before the range was evaluated, as it must be
				// reevaluated for the new hashList.
				minBlock += uint64(i)
				hashList, err = recoverFromReorg(
					chain, minBlock, maxBlock, lastBlockHash,
				)
				if err != nil {
					return nil, nil, nil, err
				}
				if len(hashList) == 0 {
					break fetchRange
				}
				goto loopHashList
			}
			if i == 0 && lastBlockHash != nil {
				// Ensure the new hashList is on the same fork
				// as the last block from the old hashList.
				var jsonErr error
				jsonErr = descendantBlock(chain, lastBlockHash, node)
				if jsonErr != nil {
					return nil, nil, nil, jsonErr
				}
			}

			// A select statement is used to stop rescans if the
			// client requesting the rescan has disconnected.
			select {
			case <-wsc.quit:
				log.Debug(fmt.Sprintf("Stopped rescan at order %v "+
					"for disconnected client", node.GetOrder()))
				return nil, nil, nil, nil
			default:
				blk := chain.GetBlockNode(node)
				h := rescanBlock(wsc, lookups, blk.GetBody())
				if h != nil {
					lastTxHash = h
				}
				lastBlock = node
				lastBlockHash = node.GetHash()
			}
			log.Debug("lastBlock", "order", lastBlock.GetOrder())
			// Periodically notify the client of the progress
			// completed.  Continue with next block if no progress
			// notification is needed yet.
			select {
			case <-ticker.C: // fallthrough
			default:
			}
			n := cmds.NewRescanProgressNtfn(
				hashList[i].String(), uint64(node.GetOrder()),
				chain.GetBlockHeader(node).Timestamp.Unix(),
			)
			mn, err := cmds.MarshalCmd(nil, n)
			if err != nil {
				log.Error(fmt.Sprintf("Failed to marshal rescan "+
					"progress notification: %v", err))
				continue
			}

			if err = wsc.QueueNotification(mn); err == ErrClientQuit {
				// Finished if the client disconnected.
				log.Debug(fmt.Sprintf("Stopped rescan at order %v "+
					"for disconnected client", node.GetOrder()))
				return nil, nil, nil, nil
			}
		}

		minBlock += uint64(len(hashList))
	}

	return lastBlock, lastBlockHash, lastTxHash, nil
}

// rescanBlock rescans all Ethereum transactions in a single block.
// It looks for transactions where either the sender or the recipient
// matches one of the addresses in our filter (lookups.addrs).
func rescanBlock(wsc *wsClient, lookups *rescanKeys, blk *types.SerializedBlock) *common.Hash {
	var lastTxHash *common.Hash

	// Create a signer to recover the sender from the tx's signature.
	// Assumes that your server has the chainID (of type *big.Int) available.
	signer := ethtypes.LatestSignerForChainID(eparams.BdagMainnetChainConfig.ChainID)

	// Iterate over all transactions in the block.
	for _, tx := range blk.Transactions() {
		needNotifyTx := false

		// Recover the sender address.
		sender, err := ethtypes.Sender(signer, tx)
		if err == nil {
			// Use sender.Hex() to get the string representation.
			if _, ok := lookups.addrs[sender.Hex()]; ok {
				needNotifyTx = true
			}
		}
		// Check the recipient address (if not a contract creation tx).
		if tx.To() != nil {
			if _, ok := lookups.addrs[tx.To().Hex()]; ok {
				needNotifyTx = true
			}
		}

		// If the transaction involves one of our addresses, notify the client.
		if needNotifyTx {
			notifyTx := func() error {
				wsc.server.ntfnMgr.NotifyBlockTx(wsc, tx, blk)
				return nil
			}

			if err := notifyTx(); err != nil {
				// Stop the rescan early if the websocket client disconnected.
				if err == ErrClientQuit {
					return nil
				}
				// Log the error and continue to the next transaction.
				log.Error(fmt.Sprintf("Unable to notify transaction %v: %v", tx.Hash().Hex(), err))
				continue
			}

			// Record the last transaction hash that matched our filter.
			hash := tx.Hash()
			lastTxHash = &hash
		}
	}
	return lastTxHash
}
