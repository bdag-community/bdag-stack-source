package common

import (
	"testing"

	"github.com/urfave/cli/v2"
)

func TestP2PStaticPeersOnlyFlagBindsRuntimeConfig(t *testing.T) {
	for _, flag := range Flags {
		if flag.Names()[0] != "p2pstaticpeersonly" {
			continue
		}
		boolFlag, ok := flag.(*cli.BoolFlag)
		if !ok {
			t.Fatalf("p2pstaticpeersonly flag has type %T, want *cli.BoolFlag", flag)
		}
		if boolFlag.Destination != &cfg.P2PStaticPeersOnly {
			t.Fatal("p2pstaticpeersonly flag is not bound to cfg.P2PStaticPeersOnly")
		}
		return
	}
	t.Fatal("p2pstaticpeersonly flag is not registered")
}

func TestP2PStaticPeersOnlyDefaultsOff(t *testing.T) {
	if DefaultConfig("").P2PStaticPeersOnly {
		t.Fatal("p2pstaticpeersonly must be disabled by default")
	}
}
