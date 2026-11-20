package json

import (
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestChainIdentityFixtureAndOpenRPCContract(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("..", "..", "api", "fixtures", "get-chain-identity-v1.json"))
	if err != nil {
		t.Fatalf("read chain identity fixture: %v", err)
	}
	var fixture ChainIdentityResult
	if err := stdjson.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("decode chain identity fixture: %v", err)
	}
	if fixture.Schema != ChainIdentitySchema || fixture.Network != "mainnet" || fixture.NativeNetworkMagic != "0xb4c3dce8" || fixture.EVMChainID != "1404" {
		t.Fatalf("unexpected chain identity fixture: %#v", fixture)
	}

	openRPCBytes, err := os.ReadFile(filepath.Join("..", "..", "api", "openrpc.json"))
	if err != nil {
		t.Fatalf("read OpenRPC contract: %v", err)
	}
	var document struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Methods []struct {
			Name   string `json:"name"`
			Params []any  `json:"params"`
			Result struct {
				Schema map[string]any `json:"schema"`
			} `json:"result"`
		} `json:"methods"`
		Components struct {
			Schemas map[string]struct {
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := stdjson.Unmarshal(openRPCBytes, &document); err != nil {
		t.Fatalf("decode OpenRPC contract: %v", err)
	}
	if document.Info.Version != "1.2.0" {
		t.Fatalf("OpenRPC version = %q, want 1.2.0", document.Info.Version)
	}
	found := false
	for _, method := range document.Methods {
		if method.Name == "getChainIdentity" {
			found = true
			if len(method.Params) != 0 || method.Result.Schema["$ref"] != "#/components/schemas/ChainIdentityResult" {
				t.Fatalf("getChainIdentity contract is not zero-parameter strict result: %#v", method)
			}
		}
	}
	if !found {
		t.Fatal("OpenRPC contract does not register getChainIdentity")
	}
	schema, ok := document.Components.Schemas["ChainIdentityResult"]
	if !ok {
		t.Fatal("OpenRPC contract does not define ChainIdentityResult")
	}
	var raw struct {
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := stdjson.Unmarshal(openRPCBytes, &raw); err != nil {
		t.Fatal(err)
	}
	value, exists := raw.Components.Schemas["ChainIdentityResult"]["additionalProperties"]
	if !exists || value != false {
		t.Fatalf("ChainIdentityResult must set additionalProperties=false, got %v", value)
	}
	if len(schema.Required) != 6 {
		t.Fatalf("ChainIdentityResult required fields = %d, want 6", len(schema.Required))
	}
}
