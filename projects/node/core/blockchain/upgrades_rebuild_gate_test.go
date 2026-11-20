package blockchain

import "testing"

func TestUpgradeRebuildNeededAtLayer(t *testing.T) {
	tests := []struct {
		name        string
		layer       uint64
		definitions []upgradeRebuildDefinition
		want        bool
	}{
		{
			name:  "no definitions",
			layer: 100,
			want:  false,
		},
		{
			name:  "zero window ignored",
			layer: 100,
			definitions: []upgradeRebuildDefinition{
				{Name: "disabled", StartLayer: 10, WindowSize: 0},
			},
			want: false,
		},
		{
			name:  "before first voting layer",
			layer: 99,
			definitions: []upgradeRebuildDefinition{
				{Name: "future", StartLayer: 100, WindowSize: 10},
			},
			want: false,
		},
		{
			name:  "at first voting layer",
			layer: 100,
			definitions: []upgradeRebuildDefinition{
				{Name: "started", StartLayer: 100, WindowSize: 10},
			},
			want: true,
		},
		{
			name:  "any started upgrade requires rebuild",
			layer: 100,
			definitions: []upgradeRebuildDefinition{
				{Name: "future", StartLayer: 200, WindowSize: 10},
				{Name: "started", StartLayer: 75, WindowSize: 10},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upgradeRebuildNeededAtLayer(tt.layer, tt.definitions); got != tt.want {
				t.Fatalf("upgradeRebuildNeededAtLayer(%d) = %v, want %v", tt.layer, got, tt.want)
			}
		})
	}
}

func TestNextUpgradeRebuildStartLayer(t *testing.T) {
	next, ok := nextUpgradeRebuildStartLayer([]upgradeRebuildDefinition{
		{Name: "disabled", StartLayer: 1, WindowSize: 0},
		{Name: "second", StartLayer: 200, WindowSize: 10},
		{Name: "first", StartLayer: 100, WindowSize: 10},
	})
	if !ok {
		t.Fatalf("nextUpgradeRebuildStartLayer did not find a non-zero window")
	}
	if next != 100 {
		t.Fatalf("nextUpgradeRebuildStartLayer = %d, want 100", next)
	}

	if next, ok := nextUpgradeRebuildStartLayer([]upgradeRebuildDefinition{{Name: "disabled", WindowSize: 0}}); ok {
		t.Fatalf("nextUpgradeRebuildStartLayer found %d for disabled-only definitions", next)
	}
}

func TestUpgradeCompletedVotingWindows(t *testing.T) {
	definition := upgradeRebuildDefinition{
		Name:       "test",
		StartLayer: 100,
		WindowSize: 10,
	}

	tests := []struct {
		name  string
		layer uint64
		def   upgradeRebuildDefinition
		want  uint64
	}{
		{
			name:  "before start",
			layer: 99,
			def:   definition,
			want:  0,
		},
		{
			name:  "inside first incomplete window",
			layer: 108,
			def:   definition,
			want:  0,
		},
		{
			name:  "first window complete",
			layer: 109,
			def:   definition,
			want:  1,
		},
		{
			name:  "same checkpoint after first completion",
			layer: 110,
			def:   definition,
			want:  1,
		},
		{
			name:  "second window complete",
			layer: 119,
			def:   definition,
			want:  2,
		},
		{
			name:  "timeout caps future windows",
			layer: 149,
			def: upgradeRebuildDefinition{
				Name:         "test",
				StartLayer:   100,
				WindowSize:   10,
				TimeoutLayer: 115,
			},
			want: 2,
		},
		{
			name:  "window that starts at timeout is excluded",
			layer: 149,
			def: upgradeRebuildDefinition{
				Name:         "test",
				StartLayer:   100,
				WindowSize:   10,
				TimeoutLayer: 110,
			},
			want: 1,
		},
		{
			name:  "zero window disabled",
			layer: 149,
			def: upgradeRebuildDefinition{
				Name:       "test",
				StartLayer: 100,
				WindowSize: 0,
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upgradeCompletedVotingWindows(tt.layer, tt.def); got != tt.want {
				t.Fatalf("upgradeCompletedVotingWindows(%d) = %d, want %d", tt.layer, got, tt.want)
			}
		})
	}
}

func TestUpgradeRebuildCheckpointMatches(t *testing.T) {
	definitions := []upgradeRebuildDefinition{{
		Name:       "test",
		StartLayer: 100,
		WindowSize: 10,
	}}

	if !upgradeRebuildCheckpointMatches(109, 110, definitions) {
		t.Fatalf("expected adjacent layers after the same completed window to share a checkpoint")
	}
	if upgradeRebuildCheckpointMatches(108, 109, definitions) {
		t.Fatalf("expected completing a voting window to change the checkpoint")
	}
	if upgradeRebuildCheckpointMatches(119, 120, append(definitions, upgradeRebuildDefinition{
		Name:       "other",
		StartLayer: 115,
		WindowSize: 6,
	})) {
		t.Fatalf("expected any upgrade definition checkpoint change to invalidate the marker")
	}
}
