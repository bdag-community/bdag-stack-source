package snapshot

import (
	l "github.com/BlockdagNetworkLabs/bdag/log"
)

var log l.Logger

func UseLogger(logger l.Logger) {
	log = logger
}

func init() {
	UseLogger(l.New(l.Ctx{"module": "SNAP"}))
}
