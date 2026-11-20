package state

import (
	"bytes"
	"io"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/serialization"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

type BlockState struct {
	id        uint64
	order     uint64
	weight    uint64
	status    model.BlockStatus
	evmRoot   common.Hash
	evmHash   common.Hash
	evmNumber uint64
	root      common.Hash
}

func (b *BlockState) GetID() uint64 {
	return b.id
}

func (b *BlockState) SetOrder(o uint64) {
	b.order = o
}

func (b *BlockState) GetOrder() uint64 {
	return b.order
}

func (b *BlockState) IsOrdered() bool {
	return b.GetOrder() != uint64(blockdag.MaxBlockOrder)
}

func (b *BlockState) SetWeight(weight uint64) {
	b.weight = weight
}

func (b *BlockState) GetWeight() uint64 {
	return b.weight
}

func (b *BlockState) setStatusFlags(flags model.BlockStatus) {
	b.status |= flags
}

func (b *BlockState) unsetStatusFlags(flags model.BlockStatus) {
	b.status &^= flags
}

func (b *BlockState) Valid() {
	b.unsetStatusFlags(model.StatusInvalid)
}

func (b *BlockState) Invalid() {
	b.setStatusFlags(model.StatusInvalid)
}

func (b *BlockState) GetStatus() model.BlockStatus {
	return b.status
}

func (b *BlockState) Root() *common.Hash {
	return &b.root
}

func (b *BlockState) SetRoot(root *common.Hash) {
	b.root = *root
}

func (b *BlockState) SetDefault(parent model.BlockState) {
	b.root = *parent.Root()
	b.evmHash = parent.GetEVMHash()
	b.evmNumber = parent.GetEVMNumber()
	b.evmRoot = parent.GetEVMRoot()
}

func (b *BlockState) GetEVMRoot() common.Hash {
	return b.evmRoot
}

func (b *BlockState) GetEVMHash() common.Hash {
	return b.evmHash
}

func (b *BlockState) GetEVMNumber() uint64 {
	return b.evmNumber
}

func (b *BlockState) SetEVM(header *etypes.Header) {
	b.evmNumber = header.Number.Uint64()
	b.evmHash = header.Hash()
	b.evmRoot = header.Root
}

func (b *BlockState) Update(block *types.SerializedBlock, prev model.BlockState, header *etypes.Header) {
	defer func() {
		log.Trace("Update block state", "id", b.id, "order", b.order, "root", b.root.String())
	}()
	b.SetDefault(prev)
	if b.status.KnownInvalid() {
		return
	}
	b.evmRoot = header.Root
	b.evmHash = header.Hash()
	b.evmNumber = header.Number.Uint64()
	txRoot := block.Block().Header.TxRoot
	data := prev.Root().Bytes()
	data = append(data, serialization.SerializeUint64(b.order)...)
	data = append(data, serialization.SerializeUint64(b.weight)...)
	data = append(data, byte(b.status))
	data = append(data, txRoot.Bytes()...)
	data = append(data, b.evmRoot.Bytes()...)
	b.root = hash.DoubleHashHEVM(data)
}

func (b *BlockState) EncodeRLP(_w io.Writer) error {
	w := rlp.NewEncoderBuffer(_w)
	_tmp0 := w.List()
	w.WriteUint64(b.id)
	w.WriteUint64(b.order)
	w.WriteUint64(b.weight)
	w.WriteUint64(uint64(b.status))
	w.WriteBytes(b.evmRoot.Bytes())
	w.WriteBytes(b.evmHash.Bytes())
	w.WriteUint64(b.evmNumber)
	w.WriteBytes(b.root.Bytes())
	w.ListEnd(_tmp0)
	return w.Flush()
}

func (b *BlockState) DecodeRLP(dec *rlp.Stream) error {
	var _tmp0 BlockState
	{
		if _, err := dec.List(); err != nil {
			return err
		}
		// Id:
		_tmp1, err := dec.Uint64()
		if err != nil {
			return err
		}
		_tmp0.id = _tmp1
		// Order:
		_tmp2, err := dec.Uint64()
		if err != nil {
			return err
		}
		_tmp0.order = _tmp2
		// Weight:
		_tmp3, err := dec.Uint64()
		if err != nil {
			return err
		}
		_tmp0.weight = _tmp3
		// Status:
		_tmp4, err := dec.Uint8()
		if err != nil {
			return err
		}
		_tmp0.status = model.BlockStatus(_tmp4)
		// EvmRoot:
		var _tmp7 common.Hash
		if err := dec.ReadBytes(_tmp7[:]); err != nil {
			return err
		}
		_tmp0.evmRoot = _tmp7
		// EvmHash:
		var _tmp8 common.Hash
		if err := dec.ReadBytes(_tmp8[:]); err != nil {
			return err
		}
		_tmp0.evmHash = _tmp8
		// evmNumber:
		_tmp9, err := dec.Uint64()
		if err != nil {
			return err
		}
		_tmp0.evmNumber = _tmp9
		// Root:
		var _tmp10 common.Hash
		if err := dec.ReadBytes(_tmp10[:]); err != nil {
			return err
		}
		_tmp0.root = _tmp10
		if err := dec.ListEnd(); err != nil {
			return err
		}
	}
	*b = _tmp0
	return nil
}

func (b *BlockState) Bytes() ([]byte, error) {
	return rlp.EncodeToBytes(b)
}

func NewBlockState(id uint64) *BlockState {
	return &BlockState{id: id, status: model.StatusNone, root: hash.ZeroHash.ToEVMHash(), order: uint64(blockdag.MaxBlockOrder)}
}

func NewBlockStateFull(id uint64, order uint64, weight uint64, status model.BlockStatus, evmRoot common.Hash, root common.Hash) *BlockState {
	return &BlockState{id: id,
		status:  status,
		root:    root,
		weight:  weight,
		evmRoot: evmRoot,
		order:   order,
	}
}

func NewBlockStateFromBytes(data []byte) (*BlockState, error) {
	var bs BlockState
	err := rlp.Decode(bytes.NewReader(data), &bs)
	if err != nil {
		return nil, err
	}
	return &bs, nil
}