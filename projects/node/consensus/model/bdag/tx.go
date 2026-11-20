package bdag

type Tx struct {
	From  []byte
	To    []byte
	Value uint64
	Data  []byte
}

func (tx *Tx) GetFrom() []byte {
	return tx.From
}
func (tx *Tx) GetTo() []byte {
	return tx.To
}
func (tx *Tx) GetValue() uint64 {
	return tx.Value
}
func (tx *Tx) GetData() []byte {
	return tx.Data
}
