package blockchain

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
)

const (
	// currentBlockIndexVersion indicates what the current block index
	// database version.
	currentBlockIndexVersion = 1

	// blockHdrSize is the size of a block header.  This is simply the
	// constant from wire and is only provided here for convenience since
	// wire.MaxBlockHeaderPayload is quite long.
	blockHdrSize = types.MaxBlockHeaderPayload

	// medianTimeBlocks is the number of previous blocks which should be
	// used to calculate the median time used to validate block timestamps.
	medianTimeBlocks = 11
)

const (

	// MaxTimeOffsetSeconds is the maximum number of seconds a block time
	// is allowed to be ahead of the current time.
	// 2 hours -> BTC settings (2 hours / 2016 block -> 2 weeks) = 0.6 %
	// 360 sec -> Blockdag settings ( 30*2016*6/1000 )
	MaxTimeOffsetSeconds = 6 * 60 // 6 minutes

)
