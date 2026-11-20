package simulator

// import (
// 	"encoding/hex"
// 	"testing"

// 	"github.com/BlockdagNetworkLabs/bdag/config"
// 	"github.com/ethereum/go-ethereum/common"
// )

// func TestImportRawKey(t *testing.T) {
// 	node, err := StartMockNode(func(cfg *config.Config) error {
// 		cfg.DebugLevel = "trace"
// 		cfg.DebugPrintOrigins = true
// 		return nil
// 	})
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	defer node.Stop()

// 	pk, err := node.pb.Build()
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	pkHex := hex.EncodeToString(pk)
// 	z
// 	accountsM := accounts.([]map[string]any)
// 	ret := accountsM[1]["address"].(common.Address)
// 	if ret.Cmp(eaddr) != 0 {
// 		t.Fatalf("%s != %s", ret.String(), eaddr.String())
// 	}
// }
