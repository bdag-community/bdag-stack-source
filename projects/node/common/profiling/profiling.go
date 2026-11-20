package profiling

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/google/gops/agent"

	// Required for profiling
	_ "net/http/pprof"

	"runtime"
	"runtime/pprof"
)

const (
	DefaultTrackHeapLimit = 7
)

// heapDumpFileName is the name of the heap dump file. We want every run to have its own
// file, so we append the timestamp of the program launch time to the file name (note the
// custom format for compliance with file name rules on all OSes).
var heapDumpFileName = fmt.Sprintf("heap-%s.pprof", time.Now().Format("01-02-2006T15.04.05"))

// Start starts the profiling server

func Start(port string) {
	// Enable mutex/block contention profiling so /debug/pprof/mutex and
	// /debug/pprof/block are actually populated -- without this they report
	// "sampling period=0" with no data, which makes diagnosing lock
	// contention (e.g. which call site holds BlockChain.chainLock or
	// blockdag.BDAG.stateLock the longest) impossible without guessing from
	// goroutine dumps alone. Fraction 1 profiles every contention event;
	// acceptable overhead for a diagnostic build, not meant to be the
	// permanent default for a production release.
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	go func() {
		listenAddr := profileListenAddr(port)
		log.Info(fmt.Sprintf("Profile server listening on %s", listenAddr))
		profileRedirect := http.RedirectHandler("/debug/pprof", http.StatusSeeOther)
		http.Handle("/", profileRedirect)
		server := &http.Server{
			Addr:              listenAddr,
			ReadHeaderTimeout: 3 * time.Second,
		}
		err := server.ListenAndServe()

		if err != nil {
			log.Error(err.Error())
		}
	}()
	go func() {
		if err := agent.Listen(agent.Options{}); err != nil {
			log.Error(err.Error())
		}
	}()
}

func profileListenAddr(configured string) string {
	if _, _, err := net.SplitHostPort(configured); err == nil {
		return configured
	}
	return net.JoinHostPort("", configured)
}

// TrackHeap tracks the size of the heap and dumps a profile if it passes a limit
func TrackHeap(cfg *config.Config) {
	go func() {
		dumpFolder := filepath.Join(cfg.DataDir, "dumps")
		err := os.MkdirAll(dumpFolder, 0700)
		if err != nil {
			log.Error(fmt.Sprintf("Could not create heap dumps folder at %s", dumpFolder))
			return
		}
		limitInGigabytes := cfg.TrackHeapLimit
		if limitInGigabytes <= 0 {
			limitInGigabytes = DefaultTrackHeapLimit
		}
		trackHeapSize(uint64(cfg.TrackHeapLimit)*1024*1024*1024, dumpFolder)
	}()
}

func trackHeapSize(heapLimit uint64, dumpFolder string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		memStats := &runtime.MemStats{}
		runtime.ReadMemStats(memStats)
		// If we passed the expected heap limit, dump the heap profile to a file
		if memStats.HeapAlloc > heapLimit {
			dumpHeapProfile(heapLimit, dumpFolder, memStats)
		}
	}
}

func dumpHeapProfile(heapLimit uint64, dumpFolder string, memStats *runtime.MemStats) {
	heapFile := filepath.Join(dumpFolder, heapDumpFileName)
	log.Info(fmt.Sprintf("Saving heap statistics into %s (HeapAlloc=%d > %d=heapLimit)", heapFile, memStats.HeapAlloc, heapLimit))
	f, err := os.Create(heapFile)
	defer f.Close()
	if err != nil {
		log.Info(fmt.Sprintf("Could not create heap profile: %s", err))
		return
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		log.Info(fmt.Sprintf("Could not write heap profile: %s", err))
	}
}
