package types

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	s "github.com/BlockdagNetworkLabs/bdag/core/serialization"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// MaxBlockHeaderPayload is the maximum number of bytes a block header can be.
const MaxBlockHeaderPayload = BaseBlockHeaderPayload

// BaseBlockHeaderPayload is the serialized header size.
const BaseBlockHeaderPayload = 185 + pow.PROOFDATA_LENGTH

// MaxBlockPayload is the maximum bytes a block message can be in bytes.
const MaxBlockPayload = 1048576 // 1024*1024 (1MB)

// MaxTxPerBlock is the maximum number of transactions that could
// possibly fit into a block.
const MaxTxPerBlock = (MaxBlockPayload / minTxPayload) + 1

// Limited parents quantity
const MaxParentsPerBlock = 50

// header. common header need 185 bytes , proof data need extra 169 bytes
const blockHeaderLen = BaseBlockHeaderPayload

// MaxBlocksPerMsg is the maximum number of blocks allowed per message.
const MaxBlocksPerMsg = 500

type BlockHeader struct {

	// block version
	Version uint32

	// The merkle root of the previous parent blocks (the dag layer)
	ParentRoot common.Hash

	// The merkle root of the tx tree  (tx of the block)
	// included Witness here instead of the separated witness commitment
	TxRoot common.Hash

	StateRoot common.Hash

	PowRoot common.Hash

	// Difficulty target for tx
	Difficulty uint32

	// TimeStamp
	Timestamp time.Time

	BlockHeight uint64

	CoinbaseAddress common.Address

	Reward uint64

	Pow pow.IPow
}

// BlockHash computes the block identifier hash for the given block header.
func (h *BlockHeader) BlockHash() common.Hash {
	// Encode the header and hash256 everything prior to the number of
	// transactions.  Ignore the error returns since there is no way the
	// encode could fail except being out of memory which would cause a
	// run-time panic.
	// x := hash.DoubleHashHEVM(h.BlockData())
	x := hash.DoubleHashHEVM(h.BlockData())
	return x
}

func (bh *BlockHeader) GetBlockHeight() uint64 {
	return bh.BlockHeight
}

func (bh *BlockHeader) BlockData() []byte {
	buf := bytes.NewBuffer(make([]byte, 0, BaseBlockHeaderPayload))

	// powType(1) + 4 zero pad = 5 bytes
	// Ensure Pow.BlockData() begins as [powType, 0,0,0,0, nonce..., proof...]
	powHead5 := bh.Pow.BlockData()[:5]

	sec := uint32(bh.Timestamp.Unix())

	// GetNonce() must serialize to 4 bytes LE (or be a uint32 that WriteElements writes LE)
	_ = s.WriteElements(
		buf,
		bh.Version,
		&bh.ParentRoot,
		&bh.TxRoot,
		&bh.StateRoot,
		&bh.PowRoot,
		bh.Difficulty,
		sec,
		bh.BlockHeight,
		bh.CoinbaseAddress,
		bh.Reward,
		powHead5,          // 5 bytes: powType + 4 zeros
		bh.Pow.GetNonce(), // 4 bytes LE
	)
	return buf.Bytes()
}

// readBlockHeader reads a block header from io reader.  See Deserialize for
// decoding block headers stored to disk, such as in a database, as opposed to
// decoding from the type.
// TODO, redefine the protocol version and storage
func readBlockHeader(r io.Reader, pver uint32, bh *BlockHeader) error {
	// TODO fix time ambiguous
	return s.ReadElements(r, &bh.Version, &bh.ParentRoot, &bh.TxRoot,
		&bh.StateRoot, &bh.PowRoot, &bh.Difficulty, (*s.Uint32Time)(&bh.Timestamp), &bh.BlockHeight, &bh.CoinbaseAddress, &bh.Reward,
		&bh.Pow)
	// (*s.Uint32Time)(&bh.Timestamp),&bh.Difficulty,&bh.Pow)
}

// writeBlockHeader writes a block header to w.  See Serialize for
// encoding block headers to be stored to disk, such as in a database, as
// opposed to encoding for the type.
// TODO, redefine the protocol version and storage
func writeBlockHeader(w io.Writer, pver uint32, bh *BlockHeader) error {
	// TODO fix time ambiguous
	sec := uint32(bh.Timestamp.Unix())
	return s.WriteElements(w, bh.Version, &bh.ParentRoot, &bh.TxRoot,
		&bh.StateRoot, &bh.PowRoot, bh.Difficulty, sec, bh.BlockHeight, bh.CoinbaseAddress, bh.Reward, bh.Pow.Bytes())
	// &bh.StateRoot,sec,bh.Difficulty,bh.Pow.Bytes())
}

// This function get the simple hash use each parents string, so it can't use to
// check for block body .At present we use the merkles tree.
func GetParentsRoot(parents []*common.Hash) common.Hash {
	if len(parents) == 0 {
		return common.Hash{}
	}
	hashStr := ""
	for _, v := range parents {
		hashStr += v.String()
	}
	return hash.DoubleHashHEVM([]byte(hashStr))
}

// Serialize encodes a block header from r into the receiver using a format
// that is suitable for long-term storage such as a database while respecting
// the Version field.
func (h *BlockHeader) Serialize(w io.Writer) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.  As
	// a result, make use of writeBlockHeader.
	return writeBlockHeader(w, 0, h)
}

func (h *BlockHeader) SerializeForMiningSubmit(w io.Writer) error {
	return writeBlockHeader(w, 0, h)
}

// Deserialize decodes a block header from r into the receiver using a format
// that is suitable for long-term storage such as a database while respecting
// the Version field.
func (h *BlockHeader) Deserialize(r io.Reader) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.  As
	// a result, make use of readBlockHeader.
	return readBlockHeader(r, 0, h)
}

func (h *BlockHeader) DeserializeForMiningSubmit(r io.Reader) error {
	return readBlockHeader(r, 0, h)
}

func (h *BlockHeader) SerializeSize() int {
	return BaseBlockHeaderPayload
}

type Block struct {
	Header       BlockHeader
	Parents      []*common.Hash
	Transactions types.Transactions
}

// BlockHash computes the block identifier hash for this block.
func (block *Block) BlockHash() common.Hash {
	return block.Header.BlockHash()
}

// SerializeSize returns the number of bytes it would take to serialize the
// the block.
func (block *Block) SerializeSize() int {
	// Check to make sure that all transactions have the correct
	// type and version to be included in a block.

	// Block header bytes + Serialized varint size for the number of
	// transactions + Serialized varint size for the number of
	// stake transactions

	n := block.Header.SerializeSize() + s.VarIntSerializeSize(uint64(len(block.Parents))) + s.VarIntSerializeSize(uint64(block.Transactions.Len()))

	for i := 0; i < len(block.Parents); i++ {
		n += common.HashLength
	}

	return n
}

// Serialize encodes the block to w using a format that suitable for long-term
// storage such as a database while respecting the Version field in the block.
func (block *Block) Serialize(w io.Writer) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.
	// TODO, redefine the protocol version and storage
	return block.Encode(w, 0)
}

func (b *Block) Encode(w io.Writer, pver uint32) error {
	// Write the block header
	err := writeBlockHeader(w, pver, &b.Header)
	if err != nil {
		return err
	}

	// Write the parent block count
	pbCount := uint64(len(b.Parents))
	err = s.WriteVarInt(w, pver, pbCount)
	if err != nil {
		return err
	}
	// Ensure the count does not exceed the maximum
	if pbCount > MaxParentsPerBlock {
		str := fmt.Sprintf("too many parents to fit into a block "+
			"[count %d, max %d]", pbCount, MaxParentsPerBlock)
		return fmt.Errorf("MsgBlock.Encode %s", str)
	}

	// Write each parent block hash
	for _, phash := range b.Parents {
		err = s.WriteElements(w, phash)
		if err != nil {
			return err
		}
	}

	// Write the transaction count
	txCount := uint64(b.Transactions.Len())
	err = s.WriteVarInt(w, pver, txCount)
	if err != nil {
		return err
	}

	// Write each transaction
	for _, tx := range b.Transactions {
		err := tx.EncodeRLP(w)
		if err != nil {
			return err
		}
	}

	return nil
}

// Deserialize decodes a block from r into the receiver using a format that is
// suitable for long-term storage such as a database while respecting the
// Version field in the block.
func (b *Block) Deserialize(r io.Reader) error {
	// At the current time, there is no difference between the wire encoding
	// at protocol version 0 and the stable long-term storage format.  As
	// a result, make use of Decode.
	return b.Decode(r, 0)
}

func (b *Block) Decode(r io.Reader, pver uint32) error {

	err := readBlockHeader(r, pver, &b.Header)
	if err != nil {
		return err
	}
	pbCount, err := s.ReadVarInt(r, pver)
	if err != nil {
		return err
	}
	if pbCount > MaxParentsPerBlock {
		str := fmt.Sprintf("too many parents to fit into a block "+
			"[count %d, max %d]", pbCount, MaxParentsPerBlock)
		return fmt.Errorf("MsgBlock.BtcDecode %s", str)
	}
	b.Parents = make([]*common.Hash, 0, pbCount)
	phash := common.Hash{}
	for i := uint64(0); i < pbCount; i++ {
		err = s.ReadElements(r, &phash)
		if err != nil {
			return err
		}
		ph := phash
		b.Parents = append(b.Parents, &ph)
	}
	//
	txCount, err := s.ReadVarInt(r, pver)
	if err != nil {
		return err
	}
	// Bound the transaction count before allocating decoder slots.
	if txCount > MaxTxPerBlock {
		return fmt.Errorf(
			"Block.Decode: too many transactions to fit into a block [count %d, max %d]",
			txCount,
			MaxTxPerBlock,
		)
	}

	b.Transactions = make([]*types.Transaction, 0, txCount)
	// RLP buffers readers without ReadByte. Avoid reading beyond this block,
	// since the caller may have another record after its transactions.
	rlpReader := r
	if _, ok := r.(rlp.ByteReader); !ok {
		rlpReader = blockRLPReader{r}
	}
	stream := rlp.NewStream(rlpReader, 0)
	for i := uint64(0); i < txCount; i++ {
		var tx types.Transaction
		err := tx.DecodeRLP(stream)
		if err != nil {
			return err
		}
		b.Transactions = append(b.Transactions, &tx)
	}
	return nil
}

// blockRLPReader supplies exact single-byte reads without speculative buffering.
type blockRLPReader struct {
	io.Reader
}

func (r blockRLPReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r.Reader, b[:])
	return b[0], err
}

// DeserializeTxLoc decodes r in the same manner Deserialize does, but it takes
// a byte buffer instead of a generic reader and returns a slice containing the
// start and length of each transaction within the raw data that is being
// deserialized.
func (b *Block) DeserializeTxLoc(r *bytes.Buffer) ([]TxLoc, error) {
	fullLen := r.Len()

	err := readBlockHeader(r, 0, &b.Header)
	if err != nil {
		return nil, err
	}

	pbCount, err := s.ReadVarInt(r, 0)
	if err != nil {
		return nil, err
	}
	if pbCount > MaxParentsPerBlock {
		str := fmt.Sprintf("too many parents to fit into a block "+
			"[count %d, max %d]", pbCount, MaxParentsPerBlock)
		return nil, fmt.Errorf("MsgBlock.BtcDecode %s", str)
	}
	b.Parents = make([]*common.Hash, 0, pbCount)
	phash := common.Hash{}
	for i := uint64(0); i < pbCount; i++ {
		err = s.ReadElements(r, &phash)
		if err != nil {
			return nil, err
		}
		ph := phash
		b.Parents = append(b.Parents, &ph)
	}

	txCount, err := s.ReadVarInt(r, 0)
	if err != nil {
		return nil, err
	}
	// Prevent more transactions than could possibly fit into a block.
	// It would be possible to cause memory exhaustion and panics without
	// a sane upper bound on this count.
	if txCount > MaxTxPerBlock {
		return nil, fmt.Errorf("Block.DeserializeTxLoc: too many transactions to fit into a block "+
			"[count %d, max %d]", txCount, MaxTxPerBlock)
	}

	// Deserialize each transaction while keeping track of its location
	// within the byte stream.
	b.Transactions = make([]*types.Transaction, 0, txCount)
	txLocs := make([]TxLoc, txCount)
	for i := uint64(0); i < txCount; i++ {
		txLocs[i].TxStart = fullLen - r.Len()
		var tx types.Transaction
		stream := rlp.NewStream(r, 0)
		if err := tx.DecodeRLP(stream); err != nil {
			return nil, err
		}
		b.Transactions = append(b.Transactions, &tx)
		txLocs[i].TxLen = (fullLen - r.Len()) - txLocs[i].TxStart
	}
	return txLocs, nil
}

// AddTransaction adds a transaction to the message.
func (b *Block) AddTransaction(tx *types.Transaction) error {
	b.Transactions = append(b.Transactions, tx)
	return nil
}

// AddTransaction adds a transaction to the message.
func (b *Block) AddParent(h *common.Hash) error {
	b.Parents = append(b.Parents, h)
	return nil

}

// Deep copy
func (b *Block) Clone() (*Block, error) {
	var w bytes.Buffer
	w.Grow(b.SerializeSize())
	err := b.Serialize(&w)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(w.Bytes())
	var block Block
	err = block.Deserialize(r)
	if err != nil {
		return nil, err
	}
	return &block, nil
}

// SerializedBlock provides easier and more efficient manipulation of raw blocks.
// It also memorizes hashes for the block and its transactions on their first
// access so subsequent accesses don't have to  repeat the relatively expensive
// hashing operations.
type SerializedBlock struct {
	block           *Block      // Underlying Block
	hash            common.Hash // Cached block hash
	serializedBytes []byte      // Serialized bytes for the block
}

// The stringer method makes SerializedBlock satisfy the Stringer interface.
// It simplifies the message printing in the trace logs.
func (sb *SerializedBlock) String() string {
	return fmt.Sprintf("blockhash: %v transactions:%v",
		sb.hash.String(), sb.block.Transactions)
}

// NewBlock returns a new instance of the serialized block given an underlying Block.
// the block hash has been calculated and cached
func NewBlock(block *Block) *SerializedBlock {
	sb := &SerializedBlock{
		hash:  block.BlockHash(),
		block: block,
	}
	return sb
}

// NewBlockFromBlockAndBytes returns a new instance of a block given
// an underlying Block and the serialized bytes for it.
func NewBlockFromBlockAndBytes(block *Block, serializedBytes []byte) *SerializedBlock {
	return &SerializedBlock{
		hash:            block.BlockHash(),
		block:           block,
		serializedBytes: serializedBytes,
	}
}

// NewBlockDeepCopyCoinbase returns a new instance of a block given an underlying
// wire.MsgBlock
func NewBlockDeepCopyCoinbase(msgBlock *Block) *SerializedBlock {
	// Copy the msgBlock and the pointers to all the transactions.
	msgBlockCopy := new(Block)

	msgBlockCopy.Parents = msgBlock.Parents

	lenTxs := len(msgBlock.Transactions)
	mtxsCopy := make([]*types.Transaction, lenTxs)
	copy(mtxsCopy, msgBlock.Transactions)

	msgBlockCopy.Transactions = mtxsCopy

	msgBlockCopy.Header = msgBlock.Header

	bl := &SerializedBlock{
		block: msgBlockCopy,
	}
	bl.hash = msgBlock.BlockHash()

	return bl
}

// Hash returns the block identifier hash for the Block.  This is equivalent to
// calling BlockHash on the underlying Block, however it caches the
// result so subsequent calls are more efficient.
func (sb *SerializedBlock) Hash() *common.Hash {
	return &sb.hash
}

func (sb *SerializedBlock) AssertImmutability() error {
	h := sb.block.BlockHash()
	if (&h).Cmp(sb.hash) == 0 {
		return nil
	}
	return fmt.Errorf("block hash inconsistent:%s != %s", h.String(), sb.hash.String())
}

func (sb *SerializedBlock) Block() *Block {
	return sb.block
}

// NewBlockFromBytes returns a new instance of a block given the
// complete serialized bytes of one block. Trailing bytes are rejected.
func NewBlockFromBytes(serializedBytes []byte) (*SerializedBlock, error) {
	br := bytes.NewReader(serializedBytes)
	b, err := NewBlockFromReader(br)
	if err != nil {
		return nil, err
	}
	if br.Len() != 0 {
		return nil, errors.New("serialized block contains trailing bytes")
	}
	b.serializedBytes = serializedBytes

	return b, nil
}

// NewBlockFromReader returns a new instance of a block given a
// Reader to deserialize the block.  See Block.
func NewBlockFromReader(r io.Reader) (*SerializedBlock, error) {
	// Deserialize the bytes into a MsgBlock.
	var block Block
	err := block.Deserialize(r)
	if err != nil {
		return nil, err
	}
	sb := NewBlock(&block)
	return sb, nil
}

// Bytes returns the serialized bytes for the Block.  This is equivalent to
// calling Serialize on the underlying Block, however it caches the
// result so subsequent calls are more efficient.
func (sb *SerializedBlock) Bytes() ([]byte, error) {
	// Return the cached serialized bytes if it has already been generated.
	if len(sb.serializedBytes) != 0 {
		return sb.serializedBytes, nil
	}

	// Serialize the MsgBlock.
	var w bytes.Buffer
	w.Grow(sb.block.SerializeSize())
	err := sb.block.Serialize(&w)
	if err != nil {
		return nil, err
	}
	serialized := w.Bytes()

	// Cache the serialized bytes and return them.
	sb.serializedBytes = serialized
	return serialized, nil
}

// TxLoc returns the offsets and lengths of each transaction in a raw block.
// It is used to allow fast indexing into transactions within the raw byte
// stream.
func (sb *SerializedBlock) TxLoc() ([]TxLoc, error) {
	rawMsg, err := sb.Bytes()
	if err != nil {
		return nil, err
	}
	rbuf := bytes.NewBuffer(rawMsg)

	var mblock Block
	txLocs, err := mblock.DeserializeTxLoc(rbuf)
	if err != nil {
		return nil, err
	}
	return txLocs, err
}

// Transactions returns a slice of wrapped transactions for all
// transactions in the Block.  This is nearly equivalent to accessing the raw
// transactions (types.Transaction) in the underlying types.Block, however it
// instead provides easy access to wrapped versions of them.
func (sb *SerializedBlock) Transactions() []*types.Transaction {
	return sb.Block().Transactions
}

func (sb *SerializedBlock) SetInnerBlock(block Block) {
	sb.block = &block
}
