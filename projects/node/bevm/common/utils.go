package common

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	qtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/urfave/cli/v2"
)

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

// func NewBEVMAddress(pubkeyHex []byte) (common.Address, error) {
// 	publicKey, err := ecc.Secp256k1.ParsePubKey(pubkeyHex)
// 	if err != nil {
// 		return common.Address{}, err
// 	}
// 	return crypto.PubkeyToAddress(*publicKey.ToECDSA()), nil
// }

var (
	Precision = big.NewInt(params.Ether).Div(big.NewInt(params.Ether), big.NewInt(qtypes.AtomsPerCoin))
)

func CopyReceipts(receipts []*types.Receipt) []*types.Receipt {
	result := make([]*types.Receipt, len(receipts))
	for i, l := range receipts {
		cpy := *l
		result[i] = &cpy
	}
	return result
}

func TotalFees(block *types.Block, receipts []*types.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.Transactions() {
		minerFee, _ := tx.EffectiveGasTip(block.BaseFee())
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(params.Ether)))
}

func ToEVMHash(h *common.Hash) common.Hash {
	ehb := h.Bytes()
	ReverseBytes(&ehb)
	return common.BytesToHash(ehb)
}

func FromEVMHash(h common.Hash) *common.Hash {
	ehb := h.Bytes()
	ReverseBytes(&ehb)
	th, err := hash.NewHash(ehb)
	if err != nil {
		return nil
	}
	return th
}

func ToBDAGTx(tx *types.Transaction, timestamp int64, newEncoding bool) *qtypes.Tx {
	// txmb, err := tx.MarshalBinary()
	// if err != nil {
	// 	log.Error(err.Error())
	// 	return nil
	// }
	// var txData []byte
	// if newEncoding {
	// 	txData = txmb
	// } else {
	// 	txData = []byte(hexutil.Encode(txmb))
	// }
	// qtxhb := tx.Hash().Bytes()
	// ReverseBytes(&qtxhb)
	// qtxh := hash.MustBytesToHash(qtxhb)

	// mtx := qtypes.NewTransaction()
	// if timestamp > 0 {
	// 	mtx.Timestamp = time.Unix(timestamp, 0)
	// }

	// mtx.AddTxIn(&qtypes.TxInput{
	// 	PreviousOut: *qtypes.NewOutPoint(&qtxh, qtypes.SupperPrevOutIndex),
	// 	Sequence:    uint32(qtypes.TxTypeCrossChainVM),
	// 	AmountIn:    qtypes.Amount{Id: qtypes.BDAGB, Value: 0},
	// 	SignScript:  txData,
	// })
	// mtx.AddTxOut(&qtypes.TxOutput{
	// 	Amount:   qtypes.Amount{Value: 0, Id: qtypes.BDAGB},
	// 	PkScript: opreturn.NewEVMTx().PKScript(),
	// })
	// vmtx, _ := bdag.NewVMTxWithoutTx(tx)
	qtypetx := &qtypes.Tx{
		// Tx:     nil,
		Object: tx,
	}
	qtypetx.SetHash(tx.Hash())
	return qtypetx
}

// Merge merges the given flag slices.
func Merge(groups ...[]cli.Flag) []cli.Flag {
	var ret []cli.Flag
	for _, group := range groups {
		ret = append(ret, group...)
	}
	return ret
}

func ProcessEnv(env string, identifier string, exclusionFlags []cli.Flag) ([]string, error) {
	result := []string{identifier}
	if len(env) <= 0 {
		return result, nil
	}
	if e, err := strconv.Unquote(env); err == nil {
		env = e
	}
	args := strings.Fields(env)
	// Detect unsupported flags
	if len(exclusionFlags) > 0 {
		for _, flag := range exclusionFlags {
			for _, name := range flag.Names() {
				if hasCLIFlag(args, name) {
					return nil, fmt.Errorf("%s currently does not support the %s flag within --evmenv.\nTo configure HTTP/WS ports, please use the top-level flags instead:\n  --evm.http.port (default 18545)\n  --evm.ws.port   (default 18546)\n  --evm.http.addr\n  --evm.ws.addr", identifier, name)
				}
			}
		}
	}
	if len(args) <= 0 {
		return result, nil
	}
	log.Debug(fmt.Sprintf("Initialize bevm environment: %v %v ", len(args), args))
	result = append(result, args...)

	return result, nil
}

func hasCLIFlag(args []string, name string) bool {
	if name == "" {
		return false
	}
	long := "--" + name
	short := "-" + name
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
		if len(name) == 1 && (arg == short || strings.HasPrefix(arg, short+"=")) {
			return true
		}
	}
	return false
}

func DecodeTx(data []byte) (*types.Transaction, error) {
	if len(data) <= 2 {
		return nil, fmt.Errorf("No tx data:%v", data)
	}
	var txb []byte
	if data[0] == 48 && data[1] == 120 {
		txb = common.FromHex(string(data))
	} else {
		txb = data
	}
	var txmb = &types.Transaction{}
	if err := txmb.UnmarshalBinary(txb); err != nil {
		return nil, err
	}
	return txmb, nil
}

func ToTxHex(data []byte) []byte {
	if len(data) <= 2 {
		return nil
	}
	if data[0] == 48 && data[1] == 120 {
		return common.FromHex(string(data))
	}
	return data
}

func ToTxHexStr(data []byte) string {
	if len(data) <= 2 {
		return ""
	}
	if data[0] == 48 && data[1] == 120 {
		return string(data)
	}
	return hexutil.Encode(data)
}
