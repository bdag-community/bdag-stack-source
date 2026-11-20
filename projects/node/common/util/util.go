package util

import (
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/log"
)

// TODO, refactor to more general method for panic handling
// panicf is a convenience function that formats according to the given format
// specifier and arguments and then logs the result at the critical level and
// panics with it.
func panicf(format string, args ...any) {
	str := fmt.Sprintf(format, args...)
	log.Crit(str)
	panic(str)
}
