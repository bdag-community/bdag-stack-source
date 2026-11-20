package synch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
)

func TestLocalEVMRecoveryCannotScorePeerAsConsensusFailure(t *testing.T) {
	local := &blockchain.LocalEVMStateUnavailableError{Cause: blockchain.RuleError{ErrorCode: blockchain.ErrEVMStateGuard, Description: "local state probe"}}
	for _, err := range []error{local, fmt.Errorf("queued: %w", local)} {
		if !blockchain.IsLocalEVMStateUnavailable(err) || isEVMStateGuardProcessError(err) || isEVMCheckpointProcessError(err) || isDuplicateBlockProcessError(err) {
			t.Fatalf("local recovery contaminated peer consensus classification: %v", err)
		}
	}
	if !isEVMStateGuardProcessError(blockchain.RuleError{ErrorCode: blockchain.ErrEVMStateGuard}) {
		t.Fatal("real EVM commitment violation lost peer policy")
	}
}

// These wiring assertions complement the actual Phantom rollback tests. Both
// P2P entrypoints must return on local recovery before any consensus penalty.
func TestLocalEVMRecoveryEarlyReturnIsWiredInBothP2PPaths(t *testing.T) {
	for _, subject := range []struct{ file, function string }{{"peer_block_intake.go", "process"}, {"getblockdatas.go", "broadcastBlockHandler"}} {
		t.Run(subject.function, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), subject.file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var body *ast.BlockStmt
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == subject.function {
					body = fn.Body
				}
			}
			if body == nil {
				t.Fatal("missing entrypoint")
			}
			found := false
			var processPosition, guardPosition token.Pos
			var penaltyPositions []token.Pos
			ast.Inspect(body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					if method, ok := call.Fun.(*ast.SelectorExpr); ok {
						switch method.Sel.Name {
						case "ProcessBlock":
							processPosition = call.Pos()
						case "rejectPeerForEVMStateGuard", "rejectPeerForEVMCheckpoint", "IncrementBadResponses":
							penaltyPositions = append(penaltyPositions, call.Pos())
						}
					}
				}
				return true
			})
			ast.Inspect(body, func(node ast.Node) bool {
				branch, ok := node.(*ast.IfStmt)
				if !ok {
					return true
				}
				call, ok := branch.Cond.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "IsLocalEVMStateUnavailable" {
					return true
				}
				found = true
				guardPosition = branch.Pos()
				if len(branch.Body.List) == 0 {
					t.Fatal("empty local recovery branch")
				}
				if _, ok := branch.Body.List[len(branch.Body.List)-1].(*ast.ReturnStmt); !ok {
					t.Fatal("local recovery falls through into peer scoring")
				}
				ast.Inspect(branch.Body, func(inner ast.Node) bool {
					if method, ok := inner.(*ast.SelectorExpr); ok && (method.Sel.Name == "rejectPeerForEVMStateGuard" || method.Sel.Name == "rejectPeerForEVMCheckpoint" || method.Sel.Name == "IncrementBadResponses") {
						t.Fatal("local recovery branch penalizes peer")
					}
					return true
				})
				return false
			})
			if !found {
				t.Fatal("local recovery guard missing")
			}
			if processPosition == token.NoPos || guardPosition < processPosition {
				t.Fatal("local classification is not bound to ProcessBlock result")
			}
			for _, position := range penaltyPositions {
				if position > processPosition && position < guardPosition {
					t.Fatal("peer was penalized before local recovery classification")
				}
			}
		})
	}
}
