package upgrades

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRegisterUpgradeInvalid(t *testing.T) {
	cases := []struct {
		name string
		def  UpgradeDefinition
		want string
	}{
		{"zero bitmask", UpgradeDefinition{Name: "a", BitMask: 0, Threshold: 1, WindowSize: 1}, "bitmask must have exactly one bit set"},
		{"multi bitmask", UpgradeDefinition{Name: "b", BitMask: 3, Threshold: 1, WindowSize: 1}, "bitmask must have exactly one bit set"},
		{"zero window", UpgradeDefinition{Name: "c", BitMask: 1, Threshold: 1, WindowSize: 0}, "window size must be > 0"},
		{"low threshold", UpgradeDefinition{Name: "d", BitMask: 1, Threshold: 0, WindowSize: 1}, "threshold must be between 1 and the window size"},
		{"high threshold", UpgradeDefinition{Name: "e", BitMask: 1, Threshold: 2, WindowSize: 1}, "threshold must be between 1 and the window size"},
		{"timeout before start", UpgradeDefinition{Name: "f", BitMask: 1, Threshold: 1, WindowSize: 1, StartLayer: 10, TimeoutLayer: 5}, "start layer must be before timeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ReplaceUpgrades(map[string]*UpgradeDefinition{})
			err := RegisterUpgrade(&tc.def)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error %q, got %v", tc.want, err)
			}
		})
	}
}

func TestIsEnforcedAtLayer(t *testing.T) {
	ReplaceUpgrades(map[string]*UpgradeDefinition{})
	RegisterPersist(nil)
	if err := RegisterUpgrade(&UpgradeDefinition{Name: "enforce", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}

	if IsEnforcedAtLayer("missing", 100) {
		t.Fatal("unknown upgrade must not be enforced")
	}
	if IsEnforcedAtLayer("enforce", 100) {
		t.Fatal("upgrade without anchor must not be enforced")
	}

	if err := SetUpgradeLockedIn("enforce", 10); err != nil {
		t.Fatalf("lock in: %v", err)
	}
	if IsEnforcedAtLayer("enforce", 9) {
		t.Fatal("layer below anchor must not be enforced")
	}
	if !IsEnforcedAtLayer("enforce", 10) {
		t.Fatal("anchor layer must be enforced")
	}
	if !IsEnforcedAtLayer("enforce", 11) {
		t.Fatal("layer above anchor must be enforced")
	}

	// IsActive only flips on activation, not lock-in.
	if IsActive("enforce") {
		t.Fatal("locked-in upgrade must not report active yet")
	}
	if err := SetUpgradeActivated("enforce", 10); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !IsActive("enforce") {
		t.Fatal("activated upgrade must report active")
	}
}

func TestPersistCallbacks(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{"SetUpgradeLockedIn", func() error { return SetUpgradeLockedIn("test", 1) }},
		{"SetUpgradeActivated", func() error { return SetUpgradeActivated("test", 1) }},
	}

	for _, tc := range tests {
		t.Run(tc.name+"_NoDeadlock", func(t *testing.T) {
			ReplaceUpgrades(map[string]*UpgradeDefinition{})
			RegisterPersist(nil)
			if err := RegisterUpgrade(&UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
				t.Fatalf("register upgrade: %v", err)
			}
			orig := GetUpgrades()["test"]

			samePtr := make(chan bool, 1)
			done := make(chan struct{})
			RegisterPersist(func(name string, u *UpgradeDefinition) error {
				samePtr <- u == orig
				UpdateUpgrade(name, func(u *UpgradeDefinition) {})
				close(done)
				return nil
			})

			errCh := make(chan error, 1)
			go func() { errCh <- tc.call() }()

			select {
			case err := <-errCh:
				if err != nil {
					t.Fatalf("call returned error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatalf("%s deadlocked", tc.name)
			}

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("callback not executed")
			}

			if <-samePtr {
				t.Fatalf("callback received original pointer")
			}
		})

		t.Run(tc.name+"_Error", func(t *testing.T) {
			ReplaceUpgrades(map[string]*UpgradeDefinition{})
			RegisterPersist(nil)
			if err := RegisterUpgrade(&UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
				t.Fatalf("register upgrade: %v", err)
			}
			wantErr := errors.New("boom")
			RegisterPersist(func(string, *UpgradeDefinition) error { return wantErr })
			if err := tc.call(); !errors.Is(err, wantErr) {
				t.Fatalf("expected %v, got %v", wantErr, err)
			}
		})
	}
}

func TestGetSignalingBitsCutoff(t *testing.T) {
	ReplaceUpgrades(map[string]*UpgradeDefinition{})
	RegisterPersist(nil)
	if err := RegisterUpgrade(&UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	if bits := GetSignalingBits(0); bits&0x1 == 0 {
		t.Fatalf("expected signaling bit before lock-in")
	}
	if err := SetUpgradeLockedIn("test", 1); err != nil {
		t.Fatalf("lock in: %v", err)
	}
	if bits := GetSignalingBits(1); bits&0x1 != 0 {
		t.Fatalf("bit should be cleared after lock-in")
	}

	ReplaceUpgrades(map[string]*UpgradeDefinition{})
	if err := RegisterUpgrade(&UpgradeDefinition{Name: "timeout", BitMask: 0x1, Threshold: 1, WindowSize: 1, TimeoutLayer: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	if bits := GetSignalingBits(1); bits&0x1 != 0 {
		t.Fatalf("bit should not be signaled after timeout")
	}
}

func TestGetSignalingBitsStartAndTimeout(t *testing.T) {
	ReplaceUpgrades(map[string]*UpgradeDefinition{})
	RegisterPersist(nil)
	if err := RegisterUpgrade(&UpgradeDefinition{
		Name:         "start-timeout",
		BitMask:      0x1,
		Threshold:    1,
		WindowSize:   1,
		StartLayer:   5,
		TimeoutLayer: 10,
	}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	if bits := GetSignalingBits(4); bits != 0 {
		t.Fatalf("expected no signaling before start layer, got 0x%x", bits)
	}
	if bits := GetSignalingBits(5); bits&0x1 == 0 {
		t.Fatalf("expected signaling bit at start layer")
	}
	if bits := GetSignalingBits(9); bits&0x1 == 0 {
		t.Fatalf("expected signaling bit before timeout")
	}
	if bits := GetSignalingBits(10); bits != 0 {
		t.Fatalf("expected signaling to stop at timeout layer, got 0x%x", bits)
	}
}

func TestGetSignalingBitsActive(t *testing.T) {
	ReplaceUpgrades(map[string]*UpgradeDefinition{})
	RegisterPersist(nil)
	if err := RegisterUpgrade(&UpgradeDefinition{Name: "active", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	if err := SetUpgradeActivated("active", 5); err != nil {
		t.Fatalf("activate upgrade: %v", err)
	}
	if bits := GetSignalingBits(4); bits != 0 {
		t.Fatalf("expected no signaling once upgrade is active, got 0x%x", bits)
	}
	if bits := GetSignalingBits(5); bits != 0 {
		t.Fatalf("expected no signaling once upgrade is active, got 0x%x", bits)
	}
}
