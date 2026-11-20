package blockchain

import (
	"github.com/ethereum/go-ethereum/metrics"
)

var (
	blockProcessTimer                      = metrics.NewRegisteredTimer("blockchain/process", nil)
	blockProcessCanceledBeforeApply        = metrics.NewRegisteredCounter("blockchain/process/canceledBeforeApply", nil)
	blockProcessCompletedAfterCallerCancel = metrics.NewRegisteredCounter("blockchain/process/completedAfterCallerCancel", nil)
	acceptedBlockValidation                = metrics.NewRegisteredTimer("blockchain/acceptedblock/validation", nil)
	acceptedBlockRPCValidation             = metrics.NewRegisteredTimer("blockchain/acceptedblock/validation/rpc", nil)
	acceptedBlockP2PValidation             = metrics.NewRegisteredTimer("blockchain/acceptedblock/validation/p2p", nil)
	blockConnectedNotifications            = metrics.NewRegisteredTimer("blockchain/process/connectnotifications", nil)
	evmReorgTotal                          = metrics.NewRegisteredTimer("blockchain/evmreorg/total", nil)
	evmReorgValidation                     = metrics.NewRegisteredTimer("blockchain/evmreorg/validation", nil)
	evmReorgRewind                         = metrics.NewRegisteredTimer("blockchain/evmreorg/rewind", nil)
	evmReorgFastPath                       = metrics.NewRegisteredTimer("blockchain/evmreorg/fastpath", nil)
	evmReorgFastBuild                      = metrics.NewRegisteredTimer("blockchain/evmreorg/fastpath/build", nil)
	evmReorgFastInsert                     = metrics.NewRegisteredTimer("blockchain/evmreorg/fastpath/insert", nil)
	evmReorgFastSetCanonical               = metrics.NewRegisteredTimer("blockchain/evmreorg/fastpath/setcanonical", nil)
	resyncTimeouts                         = metrics.NewRegisteredCounter("blockchain/resyncTimeouts", nil)
)
