package blockchain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestUpgradeHeaderScanUsesNonPopulatingReader(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "upgrades.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found, read := false, false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "rebuildUpgradeResultsFromHeaders" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "GetBlockById" {
				t.Error("historical scan populates live DAG cache")
			}
			if selector.Sel.Name == "ReadBlockById" {
				read = true
			}
			return true
		})
	}
	if !found || !read {
		t.Fatal("production upgrade rebuild has no non-populating ancestry read")
	}
}
