package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/urfave/cli/v2"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := &cli.App{
		Name:     "node-worker",
		Usage:    "auto-upgrades bdag binary on on-chain governance",
		Version:  version.String(),
		Metadata: map[string]interface{}{"source_commit": version.SourceCommit},
		Commands: []*cli.Command{snapshotControlCmd(), stopControlCmd()},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "node-binary", Value: "bdag"},
			&cli.StringFlag{Name: "node-args"},
			&cli.StringFlag{Name: "rpc-url", Usage: "Ethereum WebSocket endpoint"},
			&cli.StringFlag{Name: "evm-rpc-url", Usage: "EVM HTTP JSON-RPC endpoint for sync status"},
			&cli.StringFlag{Name: "dag-rpc-url", Usage: "DAG RPC endpoint for health checks"},
			&cli.StringFlag{
				Name:  "persist-root",
				Value: "/opt/bdag",
				Usage: "directory where the worker persists promoted and staged binaries",
			},
			&cli.Uint64Flag{
				Name:  "health-min-peers",
				Value: 20,
				Usage: "minimum peer count required before restarting a staged upgrade",
			},
			&cli.DurationFlag{
				Name:  "rollout-window",
				Usage: "duration to deterministically spread restart after staging an upgrade (0 disables jitter)",
			},
			&cli.DurationFlag{
				Name:  "health.startup-timeout",
				Value: 48 * time.Hour,
				Usage: "maximum time the worker will wait for the node readiness probe before failing (0 to wait indefinitely)",
			},
			&cli.DurationFlag{
				Name:  "health.liveness-timeout",
				Value: 60 * time.Second,
				Usage: "duration a failing liveness probe must persist before the worker forces a shutdown",
			},
			&cli.DurationFlag{
				Name:  "shutdown-timeout",
				Value: defaultShutdownTimeout,
				Usage: "maximum time to allow the node child to flush and stop before a forced kill",
			},
			&cli.DurationFlag{
				Name:  "health.probe-interval",
				Value: time.Second,
				Usage: "interval between liveness and mining readiness probes",
			},
			&cli.DurationFlag{
				Name:  "health.mining-readiness-timeout",
				Value: 0,
				Usage: "duration getTemplateHealth must report non-mineable or non-submit-ready before the worker forces a shutdown (0 disables mining readiness gate)",
			},
			&cli.DurationFlag{
				Name:  "health.mining-readiness-grace",
				Value: 2 * time.Minute,
				Usage: "startup grace period before mining readiness failures are restart-eligible",
			},
			&cli.DurationFlag{
				Name:  "status-log.syncing-interval",
				Value: 5 * time.Second,
				Usage: "interval between node status JSON log lines while native or EVM sync is active (0 disables active sync status logs)",
			},
			&cli.DurationFlag{
				Name:  "status-log.synced-interval",
				Value: 30 * time.Second,
				Usage: "interval between node status JSON log lines while native and EVM sync are both current (0 disables synced status logs)",
			},
			&cli.BoolFlag{
				Name:  "diagnostics.liveness-dump",
				Value: true,
				Usage: "before forcing child shutdown on sustained liveness failure, try GET /debug/pprof/goroutine?debug=2 on the child's profile server",
			},
			&cli.StringFlag{
				Name:  "diagnostics.liveness-dump-dir",
				Usage: "directory for goroutine dumps (default: <persist-root>/liveness-dumps)",
			},
			&cli.StringFlag{
				Name:  "diagnostics.pprof-base",
				Usage: "override base URL for pprof (e.g. http://127.0.0.1:6060) when --profile is only set inside node.conf",
			},
			&cli.DurationFlag{
				Name:  "ip-refresh-interval",
				Value: 1 * time.Hour,
				Usage: "how often to re-check the host's external IP and restart the node if it has changed (0 to disable)",
			},
			&cli.BoolFlag{
				Name:  "snapshot.enable",
				Usage: "enable hourly cold-db snapshot export managed by nodeworker",
			},
			&cli.DurationFlag{
				Name:  "snapshot.interval",
				Value: time.Hour,
				Usage: "interval between successful/attempted snapshot runs",
			},
			&cli.StringFlag{
				Name:  "snapshot.output-dir",
				Value: defaultSnapshotOutputDir,
				Usage: "directory where snapshot.bdsnap, its manifest, and latest.json are published",
			},
			&cli.StringFlag{
				Name:  "snapshot.datadir",
				Usage: "node network data directory to snapshot (default: <node --datadir>/<snapshot.network>)",
			},
			&cli.StringFlag{
				Name:  "snapshot.network",
				Value: defaultSnapshotNetwork,
				Usage: "network name for snapshot export (mainnet, testnet, devnet, privnet, mixnet)",
			},
			&cli.IntFlag{
				Name:  "snapshot.chunk-size",
				Usage: "snapshot chunk size in bytes (0 uses bdag default)",
			},
			&cli.BoolFlag{
				Name:  "snapshot.run-on-start",
				Usage: "attempt a snapshot immediately after nodeworker startup gates pass",
			},
			&cli.StringFlag{
				Name:  "control.socket",
				Usage: "path to the Unix socket for on-demand snapshot requests (default: <persist-root>/control.sock)",
			},
		},
		Action: run,
	}

	if err := app.RunContext(ctx, os.Args); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
