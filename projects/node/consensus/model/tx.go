package model

type Tx interface {
	// GetTxType() types.TxType
	GetFrom() []byte
	GetTo() []byte
	GetValue() uint64
	GetData() []byte
}
