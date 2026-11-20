package hash

/*
#cgo CFLAGS: -I${SRCDIR}
#include "scrypt.h"
*/
import "C"
import (
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
)

func HashbdagScrypt(headerData []byte) common.Hash {
	// if len(headerData) != 80 {
	// 	panic("headerData must be exactly 80 bytes")
	// }

	out := make([]byte, 32)

	C.scrypt_hash_data(
		unsafe.Pointer(&out[0]),
		unsafe.Pointer(&headerData[0]),
	)
	return common.BytesToHash(out)
}