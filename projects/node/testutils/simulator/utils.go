package simulator

// import (
// 	"fmt"
// 	"strconv"
// 	"testing"

// 	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
// 	"github.com/ethereum/go-ethereum/common"
// )

// func NewHashFromHex(hexStr string) (*common.Hash, error) {
// 	// Remove 0x prefix if present
// 	if len(hexStr) >= 2 && hexStr[0] == '0' && (hexStr[1] == 'x' || hexStr[1] == 'X') {
// 		hexStr = hexStr[2:]
// 	}

// 	// Check length
// 	if len(hexStr) != common.HashLength*2 {
// 		return nil, fmt.Errorf("invalid hash hex string length %d, want %d", len(hexStr), common.HashLength*2)
// 	}

// 	// Convert from common.Hash
// 	evmHash := common.HexToHash(hexStr)

// 	// Convert to our Hash type
// 	var h common.Hash
// 	ehb := evmHash.Bytes()
// 	ReverseBytes(&ehb) // Maintain byte ordering consistency
// 	copy(h[:], ehb)

// 	return &h, nil
// }
// func ReverseBytes(bs *[]byte) *[]byte {
// 	length := len(*bs)
// 	for i := 0; i < length/2; i++ {
// 		index := length - 1 - i
// 		temp := (*bs)[index]
// 		(*bs)[index] = (*bs)[i]
// 		(*bs)[i] = temp
// 	}
// 	return bs
// }

// // GenerateBlock will generate a number of blocks by the input number
// // It will return the hashes of the generated blocks or an error
// func GenerateBlock(t *testing.T, node *MockNode, num uint64) []*common.Hash {
// 	result := make([]*common.Hash, 0)
// 	blocks, err := node.GetPrivateMinerAPI().Generate(uint32(num), pow.BDAGXKECCAKV1)
// 	if err != nil {
// 		t.Errorf("generate block failed : %v", err)
// 		return nil
// 	}

// 	for _, b := range blocks {
// 		bh, _ := NewHashFromHex(b)
// 		result = append(result, bh)
// 		t.Logf("%v: generate block [%v] ok", node.ID(), b)
// 	}
// 	return result
// }

// // AssertBlockOrderAndHeight will verify the current block order, total block number
// // and current main-chain height of the appointed test node and assert it ok or
// // cause the test failed.
// func AssertBlockOrderAndHeight(t *testing.T, node *MockNode, order, total, height uint) {
// 	// order
// 	c, err := node.GetPublicBlockAPI().GetBlockCount()
// 	if err != nil {
// 		t.Errorf("test failed : %v", err)
// 	} else {
// 		expect := order
// 		if c.(uint) != expect {
// 			t.Errorf("test failed, expect %v , but got %v", expect, c)
// 		}
// 	}
// 	// total block
// 	tal, err := node.GetPublicBlockAPI().GetBlockTotal()
// 	if err != nil {
// 		t.Errorf("test failed : %v", err)
// 	} else {
// 		expect := total
// 		if tal != expect {
// 			t.Errorf("test failed, expect %v , but got %v", expect, tal)
// 		}
// 	}
// 	// main height
// 	h, err := node.GetPublicBlockAPI().GetMainChainHeight()
// 	if err != nil {
// 		t.Errorf("test failed : %v", err)
// 	} else {
// 		expect := height
// 		hi, err := strconv.ParseUint(h.(string), 10, 64)
// 		if err != nil {
// 			t.Errorf("test failed : %v", err)
// 		}
// 		if hi != uint64(expect) {
// 			t.Errorf("test failed, expect %v , but got %v", expect, h)
// 		}
// 	}
// }
