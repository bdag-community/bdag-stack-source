package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// sendControlRequest dials a running nodeworker's control socket, sends
// req, and blocks for the response. Export/import of a large chain DB can
// take minutes; the only timeout is ctx (e.g. ctrl-C on the CLI side) —
// there is no artificial deadline on the read.
//
// The server streams zero or more log lines (see controlEnvelope/
// controlLineStreamer in control.go) before the final result; each is
// written to logOut as it arrives so a caller like bdagrig-console, which
// already relays this process's own stdout live to its UI, shows the same
// export/import progress an operator watching `docker logs` would see
// instead of blocking in silence. logOut may be nil to discard log lines
// and only wait for the final response (e.g. the "stop" action, which never
// streams any).
func sendControlRequest(ctx context.Context, socketPath string, req controlRequest, logOut io.Writer) (controlResponse, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return controlResponse{}, fmt.Errorf("connect to nodeworker control socket %s: %w (is nodeworker running?)", socketPath, err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return controlResponse{}, fmt.Errorf("send request: %w", err)
	}
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	dec := json.NewDecoder(conn)
	for {
		var env controlEnvelope
		if err := dec.Decode(&env); err != nil {
			if ctx.Err() != nil {
				return controlResponse{}, ctx.Err()
			}
			return controlResponse{}, fmt.Errorf("read response: %w", err)
		}
		if env.Result != nil {
			return *env.Result, nil
		}
		if logOut != nil && env.Log != "" {
			fmt.Fprintln(logOut, env.Log)
		}
	}
}
