package config

import "testing"

func TestUpdateArchiveModeFromEVMEnv(t *testing.T) {
	testCases := []struct {
		name     string
		env      string
		expected bool
	}{
		{
			name:     "empty env",
			env:      "",
			expected: false,
		},
		{
			name:     "archive with equals",
			env:      "--http --gcmode=archive",
			expected: true,
		},
		{
			name:     "archive with space",
			env:      "--ws --gcmode archive",
			expected: true,
		},
		{
			name:     "quoted archive",
			env:      "\"--gcmode=archive --http.port=123\"",
			expected: true,
		},
		{
			name:     "non archive",
			env:      "--gcmode=full",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{EVMEnv: tc.env}
			cfg.UpdateArchiveModeFromEVMEnv()
			if cfg.IsArchiveNode != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, cfg.IsArchiveNode)
			}
		})
	}
}

func TestParseEVMRPC(t *testing.T) {
	testCases := []struct {
		name     string
		env      string
		wantHost string
		wantPort int
	}{
		{
			name:     "empty env uses defaults",
			env:      "",
			wantHost: DefaultEVMRPCAddr,
			wantPort: DefaultEVMRPCPort,
		},
		{
			name:     "equals flags",
			env:      "--http.addr=0.0.0.0 --http.port=18545",
			wantHost: "0.0.0.0",
			wantPort: 18545,
		},
		{
			name:     "space separated flags",
			env:      "--http.addr 0.0.0.0 --http.port 28545",
			wantHost: "0.0.0.0",
			wantPort: 28545,
		},
		{
			name:     "quoted env",
			env:      "\"--http.addr=0.0.0.0 --http.port=38545\"",
			wantHost: "0.0.0.0",
			wantPort: 38545,
		},
		{
			name:     "malformed port keeps default",
			env:      "--http.addr=0.0.0.0 --http.port=bad",
			wantHost: "0.0.0.0",
			wantPort: DefaultEVMRPCPort,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			host, port := ParseEVMRPC(tc.env)
			if host != tc.wantHost {
				t.Fatalf("expected host %q, got %q", tc.wantHost, host)
			}
			if port != tc.wantPort {
				t.Fatalf("expected port %d, got %d", tc.wantPort, port)
			}
		})
	}
}

func TestGatewayEVMAdvertisePort(t *testing.T) {
	testCases := []struct {
		name string
		cfg  Config
		want int
	}{
		{
			name: "top-level gateway port wins",
			cfg: Config{
				GatewayEvmPort: 18545,
				EVMEnv:         "--http.port=28545",
			},
			want: 18545,
		},
		{
			name: "falls back to evm env",
			cfg: Config{
				EVMEnv: "--http.port=28545",
			},
			want: 28545,
		},
		{
			name: "falls back to default",
			cfg:  Config{},
			want: DefaultEVMRPCPort,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.GatewayEVMAdvertisePort(); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}
