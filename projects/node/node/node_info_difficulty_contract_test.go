package node

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

func TestNodeInfoDifficultyUsesTipAlgorithm(t *testing.T) {
	for _, kind := range []pow.PowType{pow.BDAGXKECCAKV1, pow.BLAKE2S, pow.SCRYPT} {
		t.Run(pow.GetPowName(kind), func(t *testing.T) {
			network := &params.MainNetParams
			if kind == pow.BLAKE2S {
				network = &params.TestNetParams
			}
			instance := pow.GetInstance(kind, 0, 0, nil)
			instance.SetParams(network.PowConfig)
			bits := pow.BigToCompact(instance.GetSafeDiff(0))
			header := &types.BlockHeader{Pow: instance, Difficulty: bits}
			target, gotType, ok := currentHeaderDifficulty(header)
			if !ok {
				t.Fatal("expected configured tip difficulty")
			}
			got, err := nodeInfoDifficulty(target, gotType, network)
			if err != nil {
				t.Fatal(err)
			}
			want := getDifficultyRatio(pow.CompactToBig(bits), network, kind)
			if got.CurrentDiff != want || math.IsNaN(got.CurrentDiff) || math.IsInf(got.CurrentDiff, 0) || got.CurrentDiff <= 0 {
				t.Fatalf("difficulty=%v want=%v", got.CurrentDiff, want)
			}
			if got.PowType != pow.GetPowName(kind) {
				t.Fatalf("wrong algorithm: %s", got.PowType)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var legacy struct {
				CurrentDiff float64 `json:"current_diff"`
			}
			if err := json.Unmarshal(encoded, &legacy); err != nil || legacy.CurrentDiff != want {
				t.Fatalf("legacy numeric field incompatible: %s: %v", encoded, err)
			}
		})
	}
}

func TestNodeInfoDifficultyRejectsUnavailableMetadata(t *testing.T) {
	if got, err := nodeInfoDifficulty(nil, pow.SCRYPT, &params.MainNetParams); err == nil || got != nil {
		t.Fatalf("missing target accepted: %+v %v", got, err)
	}
	if got, err := nodeInfoDifficulty(pow.CompactToBig(0x1d00ffff), pow.PowType(255), &params.MainNetParams); err == nil || got != nil {
		t.Fatalf("unknown proof of work accepted: %+v %v", got, err)
	}
	if got, err := nodeInfoDifficulty(pow.CompactToBig(0x1d00ffff), pow.BLAKE2S, &params.MainNetParams); err == nil || got != nil {
		t.Fatalf("unconfigured denominator accepted: %+v %v", got, err)
	}
	if got, err := nodeInfoDifficulty(pow.CompactToBig(0x1d00ffff), pow.SCRYPT, nil); err == nil || got != nil {
		t.Fatalf("missing network accepted: %+v %v", got, err)
	}
}

func TestGetNodeInfoUsesBoundedDifficultyConversion(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "api.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found, boundedCall := false, false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "GetNodeInfo" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch value := n.(type) {
			case *ast.SelectorExpr:
				if value.Sel.Name == "GetCurrentPowDiff" || value.Sel.Name == "GetMainParent" {
					t.Errorf("GetNodeInfo must not scan chain history: %s", value.Sel.Name)
				}
			case *ast.Ident:
				if value.Name == "nodeInfoDifficulty" {
					boundedCall = true
				}
			}
			return true
		})
	}
	if !found || !boundedCall {
		t.Fatal("bounded production difficulty conversion missing")
	}
}
