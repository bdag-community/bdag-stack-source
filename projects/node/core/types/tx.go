package types

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (

	// TxVersion is the current latest supported transaction version.
	TxVersion uint32 = 1

	// SequenceLockTimeDisabled is a flag that if set on a transaction
	// input's sequence number, the sequence number will not be interpreted
	// as a relative locktime.
	SequenceLockTimeDisabled = 1 << 31

	// SequenceLockTimeIsSeconds is a flag that if set on a transaction
	// input's sequence number, the relative locktime has units of 512
	// seconds.
	SequenceLockTimeIsSeconds = 1 << 22

	// SequenceLockTimeMask is a mask that extracts the relative locktime
	// when masked against the transaction input sequence number.
	SequenceLockTimeMask = 0x0000ffff

	// TODO, revisit lock time define
	// SequenceLockTimeGranularity is the defined time based granularity
	// for seconds-based relative time locks.  When converting from seconds
	// to a sequence number, the value is right shifted by this amount,
	// therefore the granularity of relative time locks in 512 or 2^9
	// seconds.  Enforced relative lock times are multiples of 512 seconds.
	SequenceLockTimeGranularity = 9

	// minTxPayload is the minimum payload size for a transaction.  Note
	// that any realistically usable transaction must have at least one
	// input or output, but that is a rule enforced at a higher layer, so
	// it is intentionally not included here.
	// Version 4 bytes + Varint number of transaction inputs 1 byte + Varint
	// number of transaction outputs 1 byte + LockTime 4 bytes + min input
	// payload + min output payload.
	minTxPayload = 10

	// MaxMessagePayload is the maximum bytes a message can be regardless of other
	// individual limits imposed by messages themselves.
	MaxMessagePayload = (1024 * 1024 * 32) // 32MB

	// NoExpiryValue is the value of expiry that indicates the transaction
	// has no expiry.
	NoExpiryValue uint32 = 0
)

// TxIndexUnknown is the value returned for a transaction index that is unknown.
// This is typically because the transaction has not been inserted into a block
// yet.
const TxIndexUnknown = -1

// TxSerializeType represents the serialized type of a transaction.
type TxSerializeType uint16

const (
	// TxSerializeFull indicates a transaction be serialized with the prefix
	// and all witness data.
	TxSerializeFull TxSerializeType = iota
	// TxSerializeNoWitness indicates a transaction be serialized with only
	// the prefix.
	TxSerializeNoWitness
)

// Tx defines a transaction that provides easier and more efficient manipulation
// of raw transactions.  It also memorizes the hash for the transaction on its
// first access so subsequent accesses don't have to repeat the relatively
// expensive hashing operations.
type Tx struct {
	hash        common.Hash // Cached transaction hash
	txIndex     int         // Position within a block or TxIndexUnknown
	IsDuplicate bool        // Whether duplicate tx.
	Object      any         // Generate different object for different transaction types when need env data
}

// Transaction() returns the underlying Tx for the transaction.
func (t *Tx) Transaction() *types.Transaction {
	// Return the cached transaction.
	return t.Object.(*types.Transaction)
}

// Transaction() returns the underlying Tx for the transaction.
func (t *Tx) SetHash(hash common.Hash) {
	// Return the cached transaction.
	t.hash = hash
}

// Hash returns the hash of the transaction.  This is equivalent to
// calling TxHash on the underlying wire.MsgTx, however it caches the
// result so subsequent calls are more efficient.
func (t *Tx) Hash() *common.Hash {
	return &t.hash
}

func (t *Tx) RefreshHash() {
	t.hash = t.Object.(*types.Transaction).Hash()
}

// SetIndex sets the index of the transaction in within a block.
func (t *Tx) SetIndex(index int) {
	t.txIndex = index
}

func (t *Tx) Index() int {
	return t.txIndex
}

func NewEVMTx(t *types.Transaction) *Tx {
	return &Tx{
		hash:        t.Hash(),
		txIndex:     TxIndexUnknown,
		IsDuplicate: false,
		Object:      t,
	}
}

// TxDesc is a descriptor about a transaction in a transaction source along with
// additional metadata.
type TxDesc struct {
	// Tx is the transaction associated with the entry.
	Tx *types.Transaction

	// Added is the time when the entry was added to the source pool.
	Added time.Time

	// Height is the block height when the entry was added to the the source
	// pool.
	Height int64

	// Fee is the total fee the transaction associated with the entry pays.
	Fee int64

	// FeePerKB is the fee the transaction pays in bdag per 1000 bytes.
	FeePerKB int64
}

// TxLoc holds locator data for the offset and length of where a transaction is
// located within a MsgBlock data buffer.
type TxLoc struct {
	TxStart int
	TxLen   int
}
