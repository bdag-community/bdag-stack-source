package json

// TxRawResult models the data from the getrawtransaction command.
type TxRawResult struct {
	Hex           string `json:"hex"`
	TxHash        string `json:"txhash,omitempty"`
	Size          int32  `json:"size,omitempty"`
	Version       uint32 `json:"version"`
	Timestamp     string `json:"timestamp,omitempty"`
	BlockHash     string `json:"blockhash,omitempty"`
	BlockOrder    uint64 `json:"blockorder,omitempty"`
	TxIndex       uint32 `json:"txindex,omitempty"`
	Confirmations int64  `json:"confirmations"`
	Time          int64  `json:"time,omitempty"`
	Blocktime     int64  `json:"blocktime,omitempty"`
	Txsvalid      bool   `json:"txsvalid"`
}

// GetRawTransactionsResult models the data from the getrawtransactions
// command.
type GetRawTransactionsResult struct {
	Hex           string `json:"hex,omitempty"`
	Txid          string `json:"txid"`
	Hash          string `json:"hash"`
	Size          string `json:"size"`
	Vsize         string `json:"vsize"`
	Version       uint32 `json:"version"`
	LockTime      uint32 `json:"locktime"`
	BlockHash     string `json:"blockhash,omitempty"`
	Confirmations uint64 `json:"confirmations,omitempty"`
	Time          int64  `json:"time,omitempty"`
	Blocktime     int64  `json:"blocktime,omitempty"`
}

type DecodeRawTransactionResult struct {
	Order     uint64 `json:"order"`
	BlockHash string `json:"blockhash"`
	Txvalid   bool   `json:"txvalid"`
	Duplicate bool   `json:"duplicate,omitempty"`
	Confirms  uint64 `json:"confirms"`
	IsBlue    bool   `json:"is_blue"`
	Txid      string `json:"txid"`
	Hash      string `json:"txhash"`
	Version   uint32 `json:"version"`
	LockTime  uint32 `json:"locktime"`
	Time      string `json:"timestamp"`
}

type Amount struct {
	Amount int64 `json:"amount"`
}
