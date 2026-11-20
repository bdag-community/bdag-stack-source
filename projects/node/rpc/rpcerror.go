package rpc

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

// RpcNoTxInfoError is a convenience function for returning a nicely formatted
// RPC error which indicates there is no information available for the provided
// transaction hash.
func RpcNoTxInfoError(txHash *common.Hash) error {
	return fmt.Errorf("No information available about transaction %v", txHash)
}

// RpcInvalidError is a convenience function to convert an invalid parameter
// error to an RPC error with the appropriate code set.
func RpcInvalidError(fmtStr string, args ...any) error {
	str := fmt.Sprintf(fmtStr, args...)
	return fmt.Errorf("Invalid Parameter : %s", str)
}

// RpcDecodeHexError is a convenience function for returning a nicely formatted
// RPC error which indicates the provided hex string failed to decode.
func RpcDecodeHexError(gotHex string) error {
	return fmt.Errorf("Argument must be hexadecimal string (not %q)", gotHex)
}

// RpcDeserializetionError is a convenience function to convert a
// deserialization error to an RPC error
func RpcDeserializationError(fmtStr string, args ...any) error {
	str := fmt.Sprintf(fmtStr, args...)
	return fmt.Errorf("Deserialization Error : %s", str)
}

func RpcSubmitError(fmtStr string, args ...any) error {
	str := fmt.Sprintf(fmtStr, args...)
	return fmt.Errorf("Submit Error : %s", str)
}

// RpcDuplicateTxError is a convenience function to convert a
// rejected duplicate tx  error to an RPC error
func RpcDuplicateTxError(fmtStr string, args ...any) error {
	str := fmt.Sprintf(fmtStr, args...)
	return fmt.Errorf("Duplicate Tx Error : %s", str)
}

// RpcRuleError is a convenience function to convert a
// rule error to an RPC error
func RpcRuleError(fmtStr string, args ...any) error {
	str := fmt.Sprintf(fmtStr, args...)
	return fmt.Errorf("Rule Error : %s", str)
}

// RpcAddressKeyError is a convenience function to convert an address/key error to
// an RPC error.
func RpcAddressKeyError(fmtStr string, args ...any) error {
	msg := fmt.Sprintf(fmtStr, args...)
	return fmt.Errorf("Invalid AddressOrKey : %s", msg)
}

func RpcInternalError(err, context string) error {
	return fmt.Errorf("%s : %s", context, err)
}

// LL(getblocktemplate RPC) 2018-10-28
// client errors.
func NodeIsSyncingError(context, err string) error {
	return fmt.Errorf("%s : %s", context, err)
}
