package synch

import (
	l "github.com/BlockdagNetworkLabs/bdag/log"
)

// log is a logger that is initialized with no output filters.  This
// means the package will not perform any logging by default until the caller
// requests it.
var log l.Logger

// UseLogger uses a specified Logger to output package logging info.
func UseLogger(logger l.Logger) {
	log = logger
}

// lazyTraceValue defers formatting until the configured logger handler has
// accepted the trace record. In particular, this preserves vmodule and custom
// handler filtering, which cannot be inferred from the global verbosity
// ceiling alone.
func lazyTraceValue(fn func() any) any {
	return l.Lazy{Fn: fn}
}

// The default amount of logging is none.
func init() {
	UseLogger(l.New(l.Ctx{"module": "SYNC"}))
}
