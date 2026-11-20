package json

import (
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestSubmitBlockProducerFixtures(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		accepted  bool
		final     bool
		retryable bool
	}{
		{name: "submit-block-v2-accepted.json", code: SubmitBlockOutcomeAccepted.String(), accepted: true, final: true},
		{name: "submit-block-v2-already-accepted.json", code: SubmitBlockOutcomeAlreadyAccepted.String(), accepted: true, final: true},
		{name: "submit-block-v2-queued.json", code: SubmitBlockOutcomeQueued.String(), retryable: true},
		{name: "submit-block-v2-duplicate-pending.json", code: SubmitBlockOutcomeDuplicate.String(), retryable: true},
		{name: "submit-block-v2-rejected.json", code: SubmitBlockOutcomeAlreadyRejected.String(), final: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixtureBytes, err := os.ReadFile(componentFixturePath(test.name))
			if err != nil {
				t.Fatalf("read producer fixture: %v", err)
			}
			var result SubmitBlockResult
			if err := stdjson.Unmarshal(fixtureBytes, &result); err != nil {
				t.Fatalf("decode producer fixture: %v", err)
			}
			if result.Code != test.code || result.Accepted != test.accepted ||
				result.Final != test.final || result.Retryable != test.retryable {
				t.Fatalf("fixture = %#v, want code=%q accepted=%t final=%t retryable=%t",
					result, test.code, test.accepted, test.final, test.retryable)
			}
			if result.Final && result.Retryable {
				t.Fatal("fixture cannot be both final and retryable")
			}
			if result.Accepted && !result.Final {
				t.Fatal("accepted fixture must be final")
			}
			if result.BlockHash == "" {
				t.Fatal("fixture must identify the exact submitted block")
			}
		})
	}
}

func TestOpenRPCEnumeratesEverySubmitOutcomeCode(t *testing.T) {
	documentBytes, err := os.ReadFile(filepath.Join("..", "..", "api", "openrpc.json"))
	if err != nil {
		t.Fatalf("read OpenRPC contract: %v", err)
	}
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := stdjson.Unmarshal(documentBytes, &document); err != nil {
		t.Fatalf("decode OpenRPC contract: %v", err)
	}
	actual := append([]string(nil), document.Components.Schemas["SubmitBlockResult"].Properties["code"].Enum...)
	want := []string{
		SubmitBlockOutcomeAccepted.String(),
		SubmitBlockOutcomeAlreadyAccepted.String(),
		SubmitBlockOutcomeAlreadyRejected.String(),
		SubmitBlockOutcomeQueued.String(),
		SubmitBlockOutcomeRetryable.String(),
		SubmitBlockOutcomeDuplicate.String(),
		SubmitBlockOutcomeAlreadyKnown.String(),
		SubmitBlockOutcomeStaleParent.String(),
		SubmitBlockOutcomeTipOverdue.String(),
		SubmitBlockOutcomeExpired.String(),
		SubmitBlockOutcomeTemplateSeqUnknown.String(),
		SubmitBlockOutcomeInvalidPOW.String(),
		SubmitBlockOutcomeTxRootMismatch.String(),
		SubmitBlockOutcomeMissingTrie.String(),
		SubmitBlockOutcomeNonceTooLow.String(),
		SubmitBlockOutcomeNodeSyncing.String(),
		SubmitBlockOutcomeP2PNotCurrent.String(),
		SubmitBlockOutcomeMinerDisabled.String(),
		SubmitBlockOutcomeGovernanceRejected.String(),
		SubmitBlockOutcomeInternalError.String(),
	}
	sort.Strings(actual)
	sort.Strings(want)
	if len(actual) != len(want) {
		t.Fatalf("OpenRPC outcome count = %d, want %d: %v", len(actual), len(want), actual)
	}
	for i := range want {
		if actual[i] != want[i] {
			t.Fatalf("OpenRPC outcomes = %v, want %v", actual, want)
		}
	}
}

func componentFixturePath(name string) string {
	return filepath.Join("..", "..", "api", "fixtures", name)
}
