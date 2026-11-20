package node

import (
	"os"
	"strings"
	"testing"
)

func TestFullNodeKeepsP2PBeforeBlockingChainRecovery(t *testing.T) {
	source, err := os.ReadFile("full.go")
	if err != nil {
		t.Fatalf("read full.go: %v", err)
	}
	text := string(source)
	if strings.Contains(text, "LowestPriority(qm.GetPeerServer())") {
		t.Fatal("DAG P2P must not be demoted behind BlockChain.Start; chain recovery can block peer startup")
	}
	if !strings.Contains(text, "BlockChain.Start may spend a long time repairing or replaying local EVM") {
		t.Fatal("missing startup-order rationale for keeping DAG P2P ahead of chain recovery")
	}
}
