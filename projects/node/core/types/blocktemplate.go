package types

import (
	"github.com/ethereum/go-ethereum/common"
)

// BlockTemplate houses a block that has yet to be solved along with additional
// details about the fees and the number of signature operations for each
// transaction in the block.
type BlockTemplate struct {
	// Block is a block that is ready to be solved by miners.  Thus, it is
	// completely valid with the exception of satisfying the proof-of-work
	// requirement.
	Block *Block

	SerializedBlock *SerializedBlock

	// Fees contains the amount of fees each transaction in the generated
	// template pays in base units.  Since the first transaction is the
	// coinbase, the first entry (offset 0) will contain the negative of the
	// sum of the fees of all other transactions.
	Fees []int64

	// Height is the height at which the block template connects to the main
	// chain.
	Height uint64

	// Blues is the count of the blue set in the block past set to
	// the DAG
	Blues int64

	// ValidPayAddress indicates whether or not the template coinbase pays
	// to an address or is redeemable by anyone.  See the documentation on
	// NewBlockTemplate for details on which this can be useful to generate
	// templates without a coinbase payment address.
	ValidPayAddress bool

	//pow diff
	Difficulty uint32

	// BlockFees represents the total fees collected from transactions in the block.
	BlockFees int64

	// TxMerklePath is the path of hashes used to verify the inclusion of a
	// transaction in the block's Merkle tree
	TxMerklePath []*common.Hash
}
