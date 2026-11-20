package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

// snapshotControlCmd wires up `nodeworker snapshot export|import`, the
// operator-facing client for the on-demand control channel in control.go.
// It talks to an already-running nodeworker process over a local Unix
// socket; it does not itself start or supervise a child bdag process.
func snapshotControlCmd() *cli.Command {
	var (
		socketPath      string
		persistRootFlag string
		path            string
		chunkSize       int
		skipEVM         bool
	)
	socketFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "control.socket",
			Usage:       "path to the target nodeworker's control socket (default: derived from --persist-root)",
			Destination: &socketPath,
		},
		&cli.StringFlag{
			Name:        "persist-root",
			Value:       "/opt/bdag",
			Usage:       "must match the target nodeworker's --persist-root when --control.socket is not set",
			Destination: &persistRootFlag,
		},
	}
	return &cli.Command{
		Name:    "snapshot",
		Aliases: []string{"snap"},
		Usage:   "Trigger an on-demand chain-db snapshot export/import on a running, supervised node",
		Description: `Asks an already-running nodeworker process to pause the node it supervises,
run "bdag snap export" or "bdag snap import" against the now-idle data
directory, then resume the node -- the same Stop/export-or-import/Start
cycle nodeworker's automatic hourly snapshot loop uses, triggered on demand
instead of by timer.

This command is a client: it connects to the target nodeworker's control
socket and blocks until the operation finishes. It must be pointed at the
same --persist-root (or an explicit --control.socket) as the nodeworker
process it's talking to.`,
		Subcommands: []*cli.Command{
			{
				Name:  "export",
				Usage: "Pause the node and export a snapshot archive",
				Flags: append(append([]cli.Flag{}, socketFlags...),
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Output file path for the archive",
						Destination: &path,
						Required:    true,
					},
					&cli.IntFlag{
						Name:        "chunk-size",
						Aliases:     []string{"c"},
						Usage:       "Soft chunk size in bytes (0 uses the bdag default)",
						Destination: &chunkSize,
					},
					&cli.BoolFlag{
						Name:        "skip-evm",
						Usage:       "Do not include the EVM state DB in the snapshot (chain-only)",
						Destination: &skipEVM,
					},
				),
				Action: func(ctx *cli.Context) error {
					return runSnapshotControlClient(ctx, persistRootFlag, socketPath, controlRequest{
						Action:    "export",
						Path:      path,
						ChunkSize: chunkSize,
						SkipEVM:   skipEVM,
					})
				},
			},
			{
				Name:  "import",
				Usage: "Pause the node and import a snapshot archive into its (empty) data directory",
				Flags: append(append([]cli.Flag{}, socketFlags...),
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Input snapshot archive path",
						Destination: &path,
						Required:    true,
					},
				),
				Action: func(ctx *cli.Context) error {
					return runSnapshotControlClient(ctx, persistRootFlag, socketPath, controlRequest{
						Action: "import",
						Path:   path,
					})
				},
			},
		},
	}
}

func runSnapshotControlClient(ctx *cli.Context, persistRoot, explicitSocket string, req controlRequest) error {
	socketPath := controlSocketPath(persistRoot, explicitSocket)
	resp, err := sendControlRequest(ctx.Context, socketPath, req, os.Stdout)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	fmt.Printf("snapshot %s ok: tip=%s order=%d root=%s\n", req.Action, resp.TipHash, resp.TipOrder, resp.RootHash)
	return nil
}

// stopControlCmd wires up `nodeworker stop`, the operator-facing client for
// the on-demand "stop and stay stopped" control action in control.go. Like
// snapshotControlCmd, it's a client: it connects to an already-running
// nodeworker's control socket and blocks until the child process has been
// cleanly shut down (nodeworker's full internal shutdown budget, not an
// outer caller's timeout). Intended for callers -- like bdagrig, before
// tearing a container down -- that need a guarantee the chain database was
// closed cleanly, rather than racing that shutdown against something like
// Docker's own (much shorter) default stop grace period.
func stopControlCmd() *cli.Command {
	var (
		socketPath      string
		persistRootFlag string
	)
	return &cli.Command{
		Name:  "stop",
		Usage: "Cleanly stop the node a running nodeworker supervises, and leave it stopped",
		Description: `Asks an already-running nodeworker process to stop the node it supervises
(SIGTERM, nodeworker's own shutdown grace period, hard-kill only past
that) and leave it stopped -- unlike "snapshot export/import", there is
no restart afterward.

This command is a client: it connects to the target nodeworker's control
socket and blocks until the stop completes. It must be pointed at the
same --persist-root (or an explicit --control.socket) as the nodeworker
process it's talking to.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "control.socket",
				Usage:       "path to the target nodeworker's control socket (default: derived from --persist-root)",
				Destination: &socketPath,
			},
			&cli.StringFlag{
				Name:        "persist-root",
				Value:       "/opt/bdag",
				Usage:       "must match the target nodeworker's --persist-root when --control.socket is not set",
				Destination: &persistRootFlag,
			},
		},
		Action: func(ctx *cli.Context) error {
			path := controlSocketPath(persistRootFlag, socketPath)
			resp, err := sendControlRequest(ctx.Context, path, controlRequest{Action: "stop"}, os.Stdout)
			if err != nil {
				return err
			}
			if !resp.OK {
				return errors.New(resp.Error)
			}
			fmt.Println("node stopped")
			return nil
		},
	}
}
