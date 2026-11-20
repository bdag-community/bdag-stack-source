package log

import (
	"fmt"
	"os"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"

	"github.com/go-stack/stack"
)

const timeKey = "t"
const lvlKey = "lvl"
const msgKey = "msg"
const errorKey = "LOG15_ERROR"

// Maximum allowed context size (number of key-value entries, not bytes).
const maxLoggerContext = 1024
const skipLevel = 2
const maxContextPairs = 1024
const maxCtxMapSize = 10000

type Lvl int

const (
	LvlCrit Lvl = iota
	LvlError
	LvlWarn
	LvlInfo
	LvlDebug
	LvlTrace
)

// AlignedString returns a 5-character string containing the name of a Lvl.
func (l Lvl) AlignedString() string {
	switch l {
	case LvlTrace:
		return "TRACE"
	case LvlDebug:
		return "DEBUG"
	case LvlInfo:
		return "INFO "
	case LvlWarn:
		return "WARN "
	case LvlError:
		return "ERROR"
	case LvlCrit:
		return "CRIT "
	default:
		panic("bad level")
	}
}

// Strings returns the name of a Lvl.
func (l Lvl) String() string {
	switch l {
	case LvlTrace:
		return "trce"
	case LvlDebug:
		return "dbug"
	case LvlInfo:
		return "info"
	case LvlWarn:
		return "warn"
	case LvlError:
		return "eror"
	case LvlCrit:
		return "crit"
	default:
		panic("bad level")
	}
}

// LvlFromString returns the appropriate Lvl from a string name.
// Useful for parsing command line args and configuration files.
func LvlFromString(lvlString string) (Lvl, error) {
	switch lvlString {
	case "trace", "trce":
		return LvlTrace, nil
	case "debug", "dbug":
		return LvlDebug, nil
	case "info":
		return LvlInfo, nil
	case "warn":
		return LvlWarn, nil
	case "error", "eror":
		return LvlError, nil
	case "crit":
		return LvlCrit, nil
	default:
		return LvlDebug, fmt.Errorf("Unknown level: %v", lvlString)
	}
}

// A Record is what a Logger asks its handler to write
type Record struct {
	Time     time.Time
	Lvl      Lvl
	Msg      string
	Ctx      []any
	Call     stack.Call
	KeyNames RecordKeyNames
}

// RecordKeyNames gets stored in a Record when the write function is executed.
type RecordKeyNames struct {
	Time string
	Msg  string
	Lvl  string
}

// A Logger writes key/value pairs to a Handler
type Logger interface {
	// New returns a new Logger that has this logger's context plus the given context
	New(ctx ...any) Logger

	// GetHandler gets the handler associated with the logger.
	GetHandler() Handler

	// SetHandler updates the logger to write records to the specified handler.
	SetHandler(h Handler)

	// Log a message at the given level with context key/value pairs
	Trace(msg string, ctx ...any)
	Debug(msg string, ctx ...any)
	Info(msg string, ctx ...any)
	Warn(msg string, ctx ...any)
	Error(msg string, ctx ...any)
	Crit(msg string, ctx ...any)
}

type logger struct {
	ctx []any
	h   *swapHandler
}

func (l *logger) write(msg string, lvl Lvl, ctx []any, skip int) {
	err := l.h.Log(&Record{
		Time: roughtime.Now(),
		Lvl:  lvl,
		Msg:  msg,
		Ctx:  newContext(l.ctx, ctx),
		Call: stack.Caller(skip),
		KeyNames: RecordKeyNames{
			Time: timeKey,
			Msg:  msgKey,
			Lvl:  lvlKey,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to log record: %v\n", err)
	}
}

func (l *logger) New(ctx ...any) Logger {
	child := &logger{newContext(l.ctx, ctx), new(swapHandler)}
	child.SetHandler(l.h)
	return child
}

func newContext(prefix []any, suffix []any) []any {
	normalizedSuffix := normalize(suffix)

	lp := len(prefix)
	ls := len(normalizedSuffix)

	if lp > maxContextPairs {
		lp = maxContextPairs
	}
	remaining := maxContextPairs - lp
	if ls > remaining {
		ls = remaining
	}

	total := lp + ls
	if total == 0 {
		return nil
	}

	newCtx := make([]any, total)
	n := copy(newCtx, prefix[:lp])
	copy(newCtx[n:], normalizedSuffix[:ls])
	return newCtx
}
func (l *logger) Trace(msg string, ctx ...any) {
	l.write(msg, LvlTrace, ctx, skipLevel)
}

func (l *logger) Debug(msg string, ctx ...any) {
	l.write(msg, LvlDebug, ctx, skipLevel)
}

func (l *logger) Info(msg string, ctx ...any) {
	l.write(msg, LvlInfo, ctx, skipLevel)
}

func (l *logger) Warn(msg string, ctx ...any) {
	l.write(msg, LvlWarn, ctx, skipLevel)
}

func (l *logger) Error(msg string, ctx ...any) {
	l.write(msg, LvlError, ctx, skipLevel)
}

func (l *logger) Crit(msg string, ctx ...any) {
	l.write(msg, LvlCrit, ctx, skipLevel)
	os.Exit(1)
}

func (l *logger) GetHandler() Handler {
	return l.h.Get()
}

func (l *logger) SetHandler(h Handler) {
	l.h.Swap(h)
}

func normalize(ctx []any) []any {
	// if the caller passed a Ctx object, then expand it
	if len(ctx) == 1 {
		if ctxMap, ok := ctx[0].(Ctx); ok {
			ctx = ctxMap.toArray()
		}
	}

	// ctx needs to be even because it's a series of key/value pairs
	// no one wants to check for errors on logging functions,
	// so instead of erroring on bad input, we'll just make sure
	// that things are the right length and users can fix bugs
	// when they see the output looks wrong
	if len(ctx)%2 != 0 {
		ctx = append(ctx, nil, errorKey, "Normalized odd number of arguments by adding nil")
	}

	return ctx
}

// Lazy allows you to defer calculation of a logged value that is expensive
// to compute until it is certain that it must be evaluated with the given filters.
//
// Lazy may also be used in conjunction with a Logger's New() function
// to generate a child logger which always reports the current value of changing

// state.
//
// You may wrap any function which takes no arguments to Lazy. It may return any
// number of values of any type.
type Lazy struct {
	Fn any
}

// Ctx is a map of key/value pairs to pass as context to a log function
// Use this only if you really need greater safety around the arguments you pass
// to the logging functions.
type Ctx map[string]any

func (c Ctx) toArray() []any {
	if len(c) == 0 {
		return nil
	}

	if len(c) > maxCtxMapSize {
		// Protect against runaway allocations / overflow
		return []any{
			errorKey,
			fmt.Sprintf("Ctx truncated: too many key/value pairs (%d > %d)", len(c), maxCtxMapSize),
		}
	}

	const maxContextEntries = 1_000_000 // prevent overflow and excessive allocation
	if len(c) > maxContextEntries {
		// Indicate truncation or error. Could log, panic, or simply return nil/safe value.
		return []any{"LOG15_ERROR", fmt.Sprintf("Ctx too large: %d entries, max allowed is %d", len(c), maxContextEntries)}
	}

	arr := make([]any, len(c)*2)

	i := 0
	for k, v := range c {
		arr[i] = k
		arr[i+1] = v
		i += 2
	}
	return arr
}
