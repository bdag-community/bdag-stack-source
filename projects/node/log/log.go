package log

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/log/term"
	"github.com/jrick/logrotate/rotator"
	"github.com/mattn/go-colorable"
)

var (
	glogger *GlogHandler

	logWrite *logWriter
)

// logWriter implements an io.Writer that outputs to both standard output and
// the write-end pipe of an initialized log rotator.
type logWriter struct {
	// logRotator is one of the logging outputs.  It should be closed on
	// application shutdown.
	logRotator *rotator.Rotator

	// Use for color terminal
	colorableWrite io.Writer
}

func (lw *logWriter) Init() {
	// init a colorful logger if possible
	usecolor := term.IsTty(os.Stdout.Fd()) && os.Getenv("TERM") != "dumb"

	if usecolor {
		lw.colorableWrite = colorable.NewColorableStderr()
	}
}

func (lw *logWriter) Close() {
	if lw.logRotator != nil {
		lw.logRotator.Close()
	}
}

func (lw *logWriter) IsUseColor() bool {
	return lw.colorableWrite != nil
}

func (lw *logWriter) Write(p []byte) (n int, err error) {
	// Drop pure whitespace writes (progress bars, accidental blank prints) to avoid bloating logs.
	if len(bytes.TrimSpace(p)) == 0 {
		return len(p), nil
	}
	if lw.logRotator != nil {
		lw.logRotator.Write(p)
	}

	if lw.colorableWrite != nil {
		n, err = lw.colorableWrite.Write(p)
		if err != nil {
			return n, err
		}
	} else {
		n, err = os.Stderr.Write(p)
		if err != nil {
			return n, err
		}
	}
	return len(p), nil
}

func init() {
	// output set to Stderr
	// it's easier to handle when run as a daemon through systemd or supervisord,
	// and Go runtime exceptions are printed to stderr as well.
	logWrite = &logWriter{}
	logWrite.Init()
	glogger = NewGlogHandler(StreamHandler(io.Writer(logWrite), TerminalFormat(logWrite.IsUseColor())))

	Root().SetHandler(glogger)

	glogger.Verbosity(LvlInfo)

	LocationTrims = append(LocationTrims, "github.com/BlockdagNetworkLabs/bdag/")
}

// initLogRotator initializes the logging rotater to write logs to logFile and
// create roll files in the same directory.  It must be called before the
// package-global log rotater variables are used.
func InitLogRotator(logFile string, logRotatorSize int64) {
	logDir, _ := filepath.Split(logFile)
	err := os.MkdirAll(logDir, 0700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create log directory: %v\n", err)
		os.Exit(1)
	}
	r, err := rotator.New(logFile, logRotatorSize, false, 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create file rotator: %v\n", err)
		os.Exit(1)
	}

	logWrite.logRotator = r
}

func LogWrite() *logWriter {
	return logWrite
}

func Glogger() *GlogHandler {
	return glogger
}

func LogAndMeasureExecutionTime(log Logger, functionName string) (onEnd func()) {
	if Glogger().GetVerbosity() < LvlTrace {
		return nil
	}
	start := time.Now()
	log.Trace(fmt.Sprintf("%s start", functionName))
	return func() {
		log.Trace(fmt.Sprintf("%s end. Took: %s", functionName, time.Since(start)))
	}
}
