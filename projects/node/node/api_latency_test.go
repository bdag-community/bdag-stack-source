package node

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Guard the API wiring as well as the helper: a cheap helper is insufficient if
// GetNodeInfo reintroduces the history scan or holds the chain lock over P2P I/O.
func TestGetNodeInfoDoesNotScanHistoryOrHoldChainLock(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "api.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "GetNodeInfo" {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.SelectorExpr); ok {
				switch call.Sel.Name {
				case "GetCurrentPowDiff", "ChainRLock", "ChainLock":
					t.Errorf("GetNodeInfo must use bounded snapshot metadata, not %s", call.Sel.Name)
				}
			}
			return true
		})
		return false
	})
	if !found {
		t.Fatal("GetNodeInfo not found")
	}
}
