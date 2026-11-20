package profiling

import "testing"

func TestProfileListenAddrHonorsConfiguredPort(t *testing.T) {
	if got, want := profileListenAddr("6060"), ":6060"; got != want {
		t.Fatalf("profileListenAddr = %q, want %q", got, want)
	}
	if got, want := profileListenAddr("6070"), ":6070"; got != want {
		t.Fatalf("profileListenAddr = %q, want %q", got, want)
	}
	if got, want := profileListenAddr("127.0.0.1:6060"), "127.0.0.1:6060"; got != want {
		t.Fatalf("profileListenAddr = %q, want %q", got, want)
	}
	if got, want := profileListenAddr("[::1]:6070"), "[::1]:6070"; got != want {
		t.Fatalf("profileListenAddr = %q, want %q", got, want)
	}
}
