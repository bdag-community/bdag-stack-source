package p2p

import "testing"

func TestSanitizeMetricNamePart(t *testing.T) {
	tests := map[string]string{
		"":              "unknown",
		"12D3KooW":      "12D3KooW",
		"peer/with:bad": "peer_with_bad",
		"peer.name-01":  "peer.name-01",
	}
	for input, want := range tests {
		if got := sanitizeMetricNamePart(input); got != want {
			t.Fatalf("sanitizeMetricNamePart(%q) = %q, want %q", input, got, want)
		}
	}
}
