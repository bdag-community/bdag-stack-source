package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePortableDatasetReportAllowsMatchingNativeAndEVM(t *testing.T) {
	report := &portableDatasetValidateReport{
		Native: &nativeReadonlyProbeResult{
			BestStateMatchesMainTip:  true,
			OrderIndexMatchesMainTip: true,
			MainTipOrder:             100,
			MainTipHash:              "0xnative",
			MainTipStateRoot:         "0xstate",
			MainTipEVMNumber:         77,
			MainTipEVMHash:           "0xevm",
			MainTipEVMRoot:           "0xroot",
		},
		EVM: &evmReadonlyProbeResult{
			Number:            77,
			HeaderHash:        "0xevm",
			HeaderRoot:        "0xroot",
			HasState:          true,
			ReceiptsComplete:  true,
			HeadPointersAgree: true,
			FreezerCompatible: true,
			LatestStateful: &evmReadonlyBlockState{
				Number:   77,
				IsTarget: true,
				HasState: true,
			},
		},
	}

	reasons := validatePortableDatasetReport(report, portableDatasetValidateOptions{
		ExpectNativeOrder:    100,
		ExpectNativeOrderSet: true,
		ExpectNativeHash:     "0xnative",
		ExpectNativeRoot:     "0xstate",
		ExpectEVMNumber:      77,
		ExpectEVMNumberSet:   true,
		ExpectEVMHash:        "0xevm",
		ExpectEVMRoot:        "0xroot",
		RequireEVMState:      true,
		ScanBack:             100,
	})
	if len(reasons) != 0 {
		t.Fatalf("validation reasons = %v, want none", reasons)
	}
}

func TestValidatePortableDatasetReportBlocksDivergentOrStatelessEVM(t *testing.T) {
	report := &portableDatasetValidateReport{
		Native: &nativeReadonlyProbeResult{
			BestStateMatchesMainTip:  true,
			OrderIndexMatchesMainTip: true,
			MainTipOrder:             100,
			MainTipHash:              "0xnative",
			MainTipStateRoot:         "0xstate",
			MainTipEVMNumber:         77,
			MainTipEVMHash:           "0xnativeevm",
			MainTipEVMRoot:           "0xnativeroot",
		},
		EVM: &evmReadonlyProbeResult{
			Number:            77,
			HeaderHash:        "0xotherevm",
			HeaderRoot:        "0xotherroot",
			HasState:          false,
			HeadPointersAgree: true,
			FreezerCompatible: true,
			LatestStateful: &evmReadonlyBlockState{
				Number:   70,
				IsTarget: false,
				HasState: true,
			},
		},
	}

	reasons := strings.Join(validatePortableDatasetReport(report, portableDatasetValidateOptions{
		RequireEVMState: true,
		ScanBack:        100,
	}), "; ")
	for _, want := range []string{
		"state trie root",
		"native-recorded EVM hash",
		"native-recorded EVM root",
		"latest stateful EVM block 70 is behind selected target 77",
	} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("validation reasons %q missing %q", reasons, want)
		}
	}
}

func TestApplyPortableDatasetValidationSpecFillsMissingExpectations(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, []byte(`{
		"version":"v23",
		"native":{
			"order":14168041,
			"hash":"0xnative",
			"state_root":"0xstate"
		},
		"evm":{
			"number":13790823,
			"hash":"0xevm",
			"root":"0xroot"
		}
	}`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	opts, err := applyPortableDatasetValidationSpec(portableDatasetValidateOptions{
		SpecFile:      specPath,
		ExpectEVMHash: "0xoverride",
	})
	if err != nil {
		t.Fatalf("apply spec returned error: %v", err)
	}
	if !opts.ExpectNativeOrderSet || opts.ExpectNativeOrder != 14168041 {
		t.Fatalf("native order = %d set=%t, want 14168041 set", opts.ExpectNativeOrder, opts.ExpectNativeOrderSet)
	}
	if opts.ExpectNativeHash != "0xnative" || opts.ExpectNativeRoot != "0xstate" {
		t.Fatalf("native expectations = %s/%s, want spec values", opts.ExpectNativeHash, opts.ExpectNativeRoot)
	}
	if !opts.ExpectEVMNumberSet || opts.ExpectEVMNumber != 13790823 {
		t.Fatalf("EVM number = %d set=%t, want 13790823 set", opts.ExpectEVMNumber, opts.ExpectEVMNumberSet)
	}
	if opts.ExpectEVMHash != "0xoverride" {
		t.Fatalf("EVM hash = %s, want explicit override", opts.ExpectEVMHash)
	}
	if opts.ExpectEVMRoot != "0xroot" {
		t.Fatalf("EVM root = %s, want spec root", opts.ExpectEVMRoot)
	}
	if opts.ArchiveSHA256 != "" || opts.ArchiveSizeBytes != 0 {
		t.Fatalf("archive expectations = %s/%d, want empty because spec omitted archive", opts.ArchiveSHA256, opts.ArchiveSizeBytes)
	}
}

func TestValidatePortableArchiveChecksSHAAndSize(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "dataset.tar.zst")
	content := []byte("portable archive bytes")
	if err := os.WriteFile(archive, content, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(content))

	result, reasons := validatePortableArchive(archive, expected, uint64(len(content)), true)
	if len(reasons) != 0 {
		t.Fatalf("validation reasons = %v, want none", reasons)
	}
	if !result.Passed || result.ActualSHA256 != expected || result.ActualSizeBytes != uint64(len(content)) {
		t.Fatalf("archive result = %+v, want passed hash/size", result)
	}

	result, reasons = validatePortableArchive(archive, "0xwrong", uint64(len(content)+1), true)
	if len(reasons) == 0 {
		t.Fatal("bad archive expectations unexpectedly passed")
	}
	joined := strings.Join(reasons, "; ")
	for _, want := range []string{"archive size", "archive SHA256"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons %q missing %q", joined, want)
		}
	}
	if result.Passed {
		t.Fatalf("bad archive result passed: %+v", result)
	}
}

func TestValidatePortableArchiveCanHashForSpecGeneration(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "dataset.tar.zst")
	content := []byte("portable archive bytes")
	if err := os.WriteFile(archive, content, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(content))

	result, reasons := validatePortableArchive(archive, "", 0, false)
	if len(reasons) != 0 {
		t.Fatalf("validation reasons = %v, want none while generating spec", reasons)
	}
	if !result.Passed || result.ActualSHA256 != expected || result.ActualSizeBytes != uint64(len(content)) {
		t.Fatalf("archive result = %+v, want passed with measured hash/size", result)
	}

	result, reasons = validatePortableArchive(archive, "", 0, true)
	if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, "; "), "archive SHA256 expectation") {
		t.Fatalf("reasons = %v, want missing SHA expectation", reasons)
	}
	if result.Passed {
		t.Fatalf("archive unexpectedly passed without required expectations: %+v", result)
	}
}

func TestPortableDatasetValidationSpecFromReport(t *testing.T) {
	report := &portableDatasetValidateReport{
		Archive: &portableArchiveValidation{
			Path:            "/tmp/dataset.tar.zst",
			ActualSHA256:    "abc123",
			ActualSizeBytes: 42,
		},
		Native: &nativeReadonlyProbeResult{
			MainTipOrder:     14168041,
			MainTipHash:      "0xnative",
			MainTipStateRoot: "0xstate",
		},
		EVM: &evmReadonlyProbeResult{
			Number:     13790823,
			HeaderHash: "0xevm",
			HeaderRoot: "0xroot",
		},
	}

	spec := portableDatasetValidationSpecFromReport(report, "v23")
	if spec.Version != "v23" ||
		spec.Native.Order != 14168041 ||
		spec.Native.Hash != "0xnative" ||
		spec.Native.StateRoot != "0xstate" ||
		spec.EVM.Number != 13790823 ||
		spec.EVM.Hash != "0xevm" ||
		spec.EVM.Root != "0xroot" ||
		spec.Archive.Path != "/tmp/dataset.tar.zst" ||
		spec.Archive.SHA256 != "abc123" ||
		spec.Archive.SizeBytes != 42 {
		t.Fatalf("spec = %+v, want report boundaries and archive details", spec)
	}
}

func TestWritePortableDatasetValidationSpec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "validation-spec.json")
	spec := portableDatasetValidationSpec{}
	spec.Version = "v23"
	spec.Native.Order = 1
	spec.Native.Hash = "0xnative"
	spec.Native.StateRoot = "0xstate"
	spec.EVM.Number = 2
	spec.EVM.Hash = "0xevm"
	spec.EVM.Root = "0xroot"

	if err := writePortableDatasetValidationSpec(path, spec); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	opts, err := applyPortableDatasetValidationSpec(portableDatasetValidateOptions{SpecFile: path})
	if err != nil {
		t.Fatalf("apply written spec: %v", err)
	}
	if !opts.ExpectNativeOrderSet || opts.ExpectNativeOrder != 1 ||
		!opts.ExpectEVMNumberSet || opts.ExpectEVMNumber != 2 ||
		opts.ExpectNativeHash != "0xnative" ||
		opts.ExpectNativeRoot != "0xstate" ||
		opts.ExpectEVMHash != "0xevm" ||
		opts.ExpectEVMRoot != "0xroot" {
		t.Fatalf("opts from written spec = %+v, want spec values", opts)
	}
}
