package gateway

import "testing"

// The native RPC client emits Blockdag_ followed by the lower-camel-case
// method. Those accepted aliases must use the same local node as bare methods.
func TestNativeAliasesPreserveLocalRoutingAndCapacity(t *testing.T) {
	for _, method := range []string{
		"getTemplateHealth", "getBlockTemplate", "submitBlockHeader", "submitBlockHeaderV2",
		"getPeerInfo", "getNetworkInfo", "getNodeInfo", "getBlockCount", "getMainChainHeight", "isCurrent",
	} {
		t.Run(method, func(t *testing.T) {
			bare := classifyGatewayRequest(method)
			alias := classifyGatewayRequest("Blockdag_" + method)
			if alias != bare {
				t.Fatalf("accepted alias Blockdag_%s has policy %+v; bare method has %+v", method, alias, bare)
			}
			if !alias.localOnly && !alias.localMining {
				t.Fatalf("node-specific alias Blockdag_%s may be relayed", method)
			}
		})
	}
}

func TestNativeMiningAliasesUseNativeReadiness(t *testing.T) {
	for _, method := range []string{"getTemplateHealth", "getBlockTemplate", "submitBlockHeader", "submitBlockHeaderV2"} {
		if requiresCertifiedEVMHead("Blockdag_" + method) {
			t.Fatalf("native mining alias %s incorrectly requires the EVM history dispatch gate", method)
		}
	}
	if isLocalMiningRPCMethod("Blockdag_unknownMethod") {
		t.Fatal("unknown aliases must not acquire native mining privileges")
	}
	if !requiresCertifiedEVMHead("Blockdag_unknownMethod") {
		t.Fatal("unknown aliases must retain the existing fail-closed EVM policy")
	}
}
