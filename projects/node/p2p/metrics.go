package p2p

import (
	"strings"

	"github.com/ethereum/go-ethereum/metrics"
)

var (
	blockBroadcastFanoutGauge         = metrics.NewRegisteredGauge("p2p/block/broadcast/fanout/last", nil)
	blockBroadcastFanoutTotal         = metrics.NewRegisteredCounter("p2p/block/broadcast/fanout/total", nil)
	blockBroadcastFanoutFreshGauge    = metrics.NewRegisteredGauge("p2p/block/broadcast/fanout/fresh", nil)
	blockBroadcastFanoutStaleGauge    = metrics.NewRegisteredGauge("p2p/block/broadcast/fanout/stale", nil)
	blockBroadcastSerializationTimer  = metrics.NewRegisteredTimer("p2p/block/broadcast/serialization", nil)
	blockBroadcastSerializationErrors = metrics.NewRegisteredCounter("p2p/block/broadcast/serialization/errors", nil)
	blockBroadcastSendAttempts        = metrics.NewRegisteredCounter("p2p/block/broadcast/send/attempts", nil)
	blockBroadcastSendSuccesses       = metrics.NewRegisteredCounter("p2p/block/broadcast/send/successes", nil)
	blockBroadcastSendErrors          = metrics.NewRegisteredCounter("p2p/block/broadcast/send/errors", nil)
	blockBroadcastSendLatencyTimer    = metrics.NewRegisteredTimer("p2p/block/broadcast/send/latency", nil)
	blockBroadcastFirstRelayTimer     = metrics.NewRegisteredTimer("p2p/block/broadcast/firstrelay", nil)
	blockBroadcastFirstRelayPeerAge   = metrics.NewRegisteredGauge("p2p/block/broadcast/firstrelay/peerGraphStateAgeMs", nil)
	blockBroadcastFirstRelayPeerLead  = metrics.NewRegisteredGauge("p2p/block/broadcast/firstrelay/peerLeadBlocks", nil)
	blockBroadcastFirstRelayPeerScore = metrics.NewRegisteredGauge("p2p/block/broadcast/firstrelay/peerRelayScore", nil)
)

func markBlockBroadcastPeerSendError(peerID string) {
	metrics.GetOrRegisterCounter("p2p/block/broadcast/peer/"+sanitizeMetricNamePart(peerID)+"/send/errors", nil).Inc(1)
}

func sanitizeMetricNamePart(value string) string {
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
