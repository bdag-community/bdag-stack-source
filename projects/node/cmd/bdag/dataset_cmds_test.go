package main

import "testing"

func TestDatasetCommandExposesOnlyImplementedOwnerPortableSurface(t *testing.T) {
	command := datasetCmd()
	if command.Name != "dataset" {
		t.Fatalf("command name = %q", command.Name)
	}
	want := map[string]bool{"inspect": false, "manifest": false, "copy": false, "verify": false}
	for _, subcommand := range command.Subcommands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("dataset subcommand %q is missing", name)
		}
	}
	if diagnostic := semanticDiagnostic("native-recorded EVM hash mismatch"); diagnostic.Classification != "native_evm_boundary_mismatch" {
		t.Fatalf("classification = %q", diagnostic.Classification)
	}
	if diagnostic := semanticDiagnostic("EVM freezer count is ahead"); diagnostic.Classification != "freezer_ahead_of_head" {
		t.Fatalf("classification = %q", diagnostic.Classification)
	}
}
