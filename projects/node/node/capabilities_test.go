package node

import (
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
)

func TestGetCapabilitiesDoesNotRequireRunningNode(t *testing.T) {
	result, err := (&PublicBlockChainAPI{}).GetCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	capabilities, ok := result.(*corejson.ComponentCapabilitiesResult)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	if capabilities.Schema != corejson.ComponentCapabilitiesSchema || capabilities.Component != corejson.CorechainComponent {
		t.Fatalf("unexpected identity: %#v", capabilities)
	}
	if capabilities.ComponentVersion == "" {
		t.Fatal("component version must remain truthful and non-empty for source builds")
	}
	for _, name := range []string{corejson.CorechainNativeAPI, corejson.CorechainEVMAPI} {
		capability, ok := capabilities.APIs[name]
		if !ok || capability.Current == "" || len(capability.SupportedMajors) == 0 {
			t.Fatalf("missing versioned capability %q: %#v", name, capability)
		}
	}
}

func TestGetCapabilitiesMatchesProducerFixture(t *testing.T) {
	result, err := (&PublicBlockChainAPI{}).GetCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	capabilities := result.(*corejson.ComponentCapabilitiesResult)

	fixturePath := filepath.Join("..", "api", "fixtures", "get-capabilities-v1.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read producer fixture: %v", err)
	}
	var fixture corejson.ComponentCapabilitiesResult
	if err := stdjson.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("decode producer fixture: %v", err)
	}
	fixture.ComponentVersion = capabilities.ComponentVersion
	if !reflect.DeepEqual(capabilities, &fixture) {
		t.Fatalf("capabilities do not match producer fixture:\nactual: %#v\nfixture: %#v", capabilities, &fixture)
	}
}
