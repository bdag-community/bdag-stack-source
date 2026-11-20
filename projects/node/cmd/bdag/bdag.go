// Copyright (c) 2015-2016 The Decred developers
// Copyright (c) 2013-2016 The btcsuite developers

package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"

	_ "github.com/BlockdagNetworkLabs/bdag/bevm/common"
	"github.com/BlockdagNetworkLabs/bdag/common/profiling"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/node"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/urfave/cli/v2"
)

func main() {
	// Initialize the goroutine count,  Use all processor cores.
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Block and transaction processing can cause bursty allocations.  This
	// limits the garbage collector from excessively overallocating during
	// bursts.  This value was arrived at with the help of profiling live
	// usage.
	debug.SetGCPercent(20)

	// Work around defer not working after os.Exit()
	if err := bDag(); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func bDag() error {
	app := &cli.App{
		Name:     "BDAG",
		Version:  version.String(),
		Compiled: roughtime.Now(),
		Authors: []*cli.Author{
			&cli.Author{
				Name: "Blockdag",
			},
		},
		Usage:                "The next generation of the Blockdag network implementation with the plug-able VMs under the BDAG consensus.",
		Commands:             commands(),
		Flags:                common.Flags,
		EnableBashCompletion: true,
		Before: func(ctx *cli.Context) error {
			return nil
		},
		Action: Blockdagd,
	}
	return app.Run(os.Args)
}

// BlockdagdMain is the real main function for Blockdagd.  It is necessary to work around
// the fact that deferred functions do not run when os.Exit() is called.  The
// optional nodeChan parameter is mainly used by the service code to be
// notified with the node once it is setup so it can gracefully stop it when
// requested from the service control manager.
func Blockdagd(ctx *cli.Context) error {
	var nodeChan chan<- *node.Node
	// Load configuration and parse command line.  This function also
	// initializes logging and configures it accordingly.
	cfg, err := common.LoadConfig(ctx, true)
	if err != nil {
		return err
	}

	log.Info("Network configured", "name", params.ActiveNetParams.Name)
	defer func() {
		if log.LogWrite() != nil {
			log.LogWrite().Close()
		}
	}()
	if len(cfg.Profile) > 0 {
		profiling.Start(cfg.Profile)
	}
	// Write cpu profile if requested.
	if len(cfg.CPUProfile) > 0 {
		f, err := os.Create(cfg.CPUProfile)
		if err != nil {
			log.Error(fmt.Sprintf("Unable to create cpu profile: %v", err.Error()))
			return err
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Error(fmt.Sprintf("Unable to start cpu profile: %v", err.Error()))
			return err
		}
		defer pprof.StopCPUProfile()
	}
	if cfg.TrackHeap {
		profiling.TrackHeap(cfg)
	}
	// Get a channel that will be closed when a shutdown signal has been
	// triggered either from an OS signal such as SIGINT (Ctrl+C) or from
	// another subsystem such as the RPC server.
	interrupt := system.InterruptListener()
	defer log.Info("Shutdown complete")

	// Show version and home dir at startup.
	log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version())
	log.Info("System info", "Home dir", cfg.HomeDir)

	if cfg.NoFileLogging {
		log.Info("File logging disabled")
	}

	// Create node and start it.
	n, err := node.NewNode(cfg, params.ActiveNetParams.Params, interrupt)
	if err != nil {
		log.Error("Unable to start server", "listeners", cfg.Listener, "error", err)
		return err
	}
	err = n.RegisterService()
	if err != nil {
		if err == node.ErrNodeNoDB {
			return nil
		}
		return err
	}
	defer func() {
		err := n.Stop()
		if err != nil {
			log.Error("node stop error", "error", err)
		}
	}()
	err = n.Start()
	if err != nil {
		log.Error("Uable to start server", "error", err)
		return err
	}
	showLogo(cfg)
	//
	if nodeChan != nil {
		nodeChan <- n
	}
	// Wait until the interrupt signal is received from an OS signal or
	// shutdown is requested through one of the subsystems such as the RPC
	// server.
	<-interrupt
	return nil
}

func showLogo(cfg *config.Config) {
	logo := `

	info : 
    .__  __  

    BDAG %s
    Port: %d
    PID : %d
    Network : %s                      



`

	fmt.Printf(logo, version.String(), cfg.P2PTCPPort, os.Getpid(), params.ActiveNetParams.Name)
}
