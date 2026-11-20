// Package metrics provides general system and process level metrics collection.
package metrics

import (
	"os"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/log"
	emetrics "github.com/ethereum/go-ethereum/metrics"
)

// Init enables or disables the metrics system. Since we need this to run before
// any other code gets to create meters and timers, we'll actually do an ugly hack
// and peek into the command line args for the metrics flag.

func init() {
	// enablerFlags is the CLI flag names to use to enable metrics collections.
	var enablerFlags = []string{"metrics"}
	for _, arg := range os.Args {
		flag := strings.TrimLeft(arg, "-")
		for _, enabler := range enablerFlags {
			if !emetrics.Enabled && flag == enabler {
				log.Info("Enabling metrics collection.")
				emetrics.Enabled = true
			}
		}
	}
}
