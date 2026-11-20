// Package dbnamespace contains constants that define the database namespaces
// for the purpose of the blockchain, so that external callers may easily access
// this data.
package dbnamespace

import (
	"encoding/binary"
)

var (
	// ByteOrder is the preferred byte order used for serializing numeric
	// fields for storage in the database.
	ByteOrder = binary.LittleEndian

	// BCDBInfoBucketName is the name of the database bucket used to house
	// global versioning and date information for the blockchain database.
	BCDBInfoBucketName = []byte("dbinfo")

	// ChainStateKeyName is the name of the db key used to store the best
	// chain state.
	ChainStateKeyName = []byte("chainstate")

	// IndexTipsBucketName is the name of the db bucket used to house the
	// current tip of each index.
	IndexTipsBucketName = []byte("idxtips")
)
