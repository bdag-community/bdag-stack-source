package version

import "testing"

func TestStringReturnsReleaseTag(t *testing.T) {
	old := ReleaseTag
	t.Cleanup(func() {
		ReleaseTag = old
	})

	ReleaseTag = "node-v1.0.9"
	if got := String(); got != "node-v1.0.9" {
		t.Fatalf("String() = %q, want %q", got, "node-v1.0.9")
	}
}
