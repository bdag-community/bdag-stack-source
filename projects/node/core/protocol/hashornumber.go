package protocol

import (
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
)

type HashOrNumber struct {
	Hash   *common.Hash
	Number uint32
}

func (hn *HashOrNumber) IsHash() bool {
	return hn.Hash != nil
}

func (hn *HashOrNumber) String() string {
	if hn.IsHash() {
		return "hash:" + hn.Hash.String()
	}
	return fmt.Sprintf("number:%d", hn.Number)
}

func NewHashOrNumber(data string) (*HashOrNumber, error) {
	if len(data) <= 0 {
		return nil, fmt.Errorf("HashOrNumber:no input data")
	}
	num, err := strconv.ParseUint(data, 10, 32)
	if err == nil {
		return &HashOrNumber{Number: uint32(num)}, nil
	}
	h, err := NewHashFromHex(data)
	if err != nil {
		return nil, err
	}
	return &HashOrNumber{Hash: h}, nil
}

// NewHashFromHex creates a new Hash from a hex string (with or without 0x prefix)
func NewHashFromHex(hexStr string) (*common.Hash, error) {
	// Remove 0x prefix if present
	if len(hexStr) >= 2 && hexStr[0] == '0' && (hexStr[1] == 'x' || hexStr[1] == 'X') {
		hexStr = hexStr[2:]
	}

	// Check length
	if len(hexStr) != common.HashLength*2 {
		return nil, fmt.Errorf("invalid hash hex string length %d, want %d", len(hexStr), common.HashLength*2)
	}

	// Convert from common.Hash
	evmHash := common.HexToHash(hexStr)

	// Convert to our Hash type
	var h common.Hash
	ehb := evmHash.Bytes()
	ReverseBytes(&ehb) // Maintain byte ordering consistency
	copy(h[:], ehb)

	return &h, nil
}
func ReverseBytes(bs *[]byte) *[]byte {
	length := len(*bs)
	for i := 0; i < length/2; i++ {
		index := length - 1 - i
		temp := (*bs)[index]
		(*bs)[index] = (*bs)[i]
		(*bs)[i] = temp
	}
	return bs
}
