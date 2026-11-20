package config

import "testing"

func TestValidateRPCAuthLimitedPair(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "primary only", cfg: Config{RPCUser: "admin", RPCPass: "secret"}},
		{name: "disabled", cfg: Config{}},
		{name: "primary incomplete", cfg: Config{RPCUser: "admin"}, wantErr: true},
		{name: "limited incomplete", cfg: Config{RPCUser: "admin", RPCPass: "secret", RPCLimitUser: "readonly"}, wantErr: true},
		{name: "limited needs primary", cfg: Config{RPCLimitUser: "readonly", RPCLimitPass: "secret"}, wantErr: true},
		{name: "limited distinct", cfg: Config{RPCUser: "admin", RPCPass: "secret", RPCLimitUser: "readonly", RPCLimitPass: "other"}},
		{name: "limited same", cfg: Config{RPCUser: "admin", RPCPass: "secret", RPCLimitUser: "admin", RPCLimitPass: "secret"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.cfg.ValidateRPCAuth(); (err != nil) != test.wantErr {
				t.Fatalf("ValidateRPCAuth error=%v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}
