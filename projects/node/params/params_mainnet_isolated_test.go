package params

import (
	"reflect"
	"testing"
)

func TestMainNetIsolatedMirrorsMainNetParams(t *testing.T) {
	if MainNetIsolatedParams.Name != "mainnet-isolated" {
		t.Fatalf("unexpected mainnet-isolated name: %q", MainNetIsolatedParams.Name)
	}
	if len(MainNetIsolatedParams.Bootstrap) != 0 {
		t.Fatalf("mainnet-isolated must not include public bootstrap peers: %v", MainNetIsolatedParams.Bootstrap)
	}

	mainnet := MainNetParams
	isolated := MainNetIsolatedParams
	isolated.Name = mainnet.Name
	isolated.Bootstrap = mainnet.Bootstrap

	if !reflect.DeepEqual(isolated, mainnet) {
		t.Fatal("mainnet-isolated params drifted from mainnet outside Name and Bootstrap")
	}
}

func TestMainNetIsolatedNetParamsMirrorMainNetPorts(t *testing.T) {
	if MainNetIsolatedParam.RpcPort != MainNetParam.RpcPort {
		t.Fatalf("mainnet-isolated RPC port = %s, want mainnet RPC port %s", MainNetIsolatedParam.RpcPort, MainNetParam.RpcPort)
	}
	if MainNetIsolatedParam.DefaultPort != MainNetParam.DefaultPort {
		t.Fatalf("mainnet-isolated P2P port = %s, want mainnet P2P port %s", MainNetIsolatedParam.DefaultPort, MainNetParam.DefaultPort)
	}
}
