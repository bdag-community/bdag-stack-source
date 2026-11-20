package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/database/evmchaindb"
	"github.com/BlockdagNetworkLabs/bdag/log"
)

type portableDatasetValidateOptions struct {
	DataDir              string
	ChainDB              string
	Chaindata            string
	SpecFile             string
	ArchivePath          string
	ArchiveSHA256        string
	ArchiveSizeBytes     uint64
	WriteSpecFile        string
	SpecVersion          string
	ExpectChainID        string
	ExpectGenesisHash    string
	ExpectEVMGenesisHash string
	ExpectNativeOrder    uint64
	ExpectNativeOrderSet bool
	ExpectNativeHash     string
	ExpectNativeRoot     string
	ExpectEVMNumber      uint64
	ExpectEVMNumberSet   bool
	ExpectEVMHash        string
	ExpectEVMRoot        string
	RequireCleanShutdown bool
	RequireEVMState      bool
	ScanBack             uint64
	PrintJSON            bool
}

type portableDatasetValidateReport struct {
	DataDir         string                     `json:"data_dir,omitempty"`
	ChainDB         string                     `json:"chain_db"`
	Chaindata       string                     `json:"chaindata"`
	SpecFile        string                     `json:"spec_file,omitempty"`
	WrittenSpecFile string                     `json:"written_spec_file,omitempty"`
	Portable        bool                       `json:"portable"`
	Reasons         []string                   `json:"reasons,omitempty"`
	Archive         *portableArchiveValidation `json:"archive,omitempty"`
	Native          *nativeReadonlyProbeResult `json:"native,omitempty"`
	EVM             *evmReadonlyProbeResult    `json:"evm,omitempty"`
}

type portableArchiveValidation struct {
	Path              string `json:"path"`
	ExpectedSHA256    string `json:"expected_sha256,omitempty"`
	ActualSHA256      string `json:"actual_sha256,omitempty"`
	ExpectedSizeBytes uint64 `json:"expected_size_bytes,omitempty"`
	ActualSizeBytes   uint64 `json:"actual_size_bytes,omitempty"`
	Passed            bool   `json:"passed"`
}

type portableDatasetValidationSpec struct {
	Version string `json:"version,omitempty"`
	Native  struct {
		Order     uint64 `json:"order"`
		Hash      string `json:"hash"`
		StateRoot string `json:"state_root"`
	} `json:"native"`
	EVM struct {
		Number uint64 `json:"number"`
		Hash   string `json:"hash"`
		Root   string `json:"root"`
	} `json:"evm"`
	Archive struct {
		Path      string `json:"path,omitempty"`
		SHA256    string `json:"sha256,omitempty"`
		SizeBytes uint64 `json:"size_bytes,omitempty"`
	} `json:"archive,omitempty"`
}

func runPortableDatasetValidate(opts portableDatasetValidateOptions) error {
	var err error
	opts, err = applyPortableDatasetValidationSpec(opts)
	if err != nil {
		return err
	}
	report, err := inspectPortableDataset(opts)
	if err != nil {
		return err
	}
	if report.Portable && opts.WriteSpecFile != "" {
		if err := writePortableDatasetValidationSpec(opts.WriteSpecFile, portableDatasetValidationSpecFromReport(report, opts.SpecVersion)); err != nil {
			return err
		}
		report.WrittenSpecFile = opts.WriteSpecFile
	}
	if opts.PrintJSON {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode portable dataset validation report: %w", err)
		}
		fmt.Println(string(encoded))
	}
	log.Info("Portable dataset validation",
		"portable", report.Portable,
		"nativeOrder", report.Native.MainTipOrder,
		"evmNumber", report.EVM.Number,
		"reasons", strings.Join(report.Reasons, "; "))
	if !report.Portable {
		return fmt.Errorf("portable dataset validation failed: %s", strings.Join(report.Reasons, "; "))
	}
	return nil
}

func inspectPortableDataset(opts portableDatasetValidateOptions) (*portableDatasetValidateReport, error) {
	chainDB, chaindata, datadir, err := resolvePortableDatasetPaths(opts)
	if err != nil {
		return nil, err
	}
	nativeDB, normalizedChainDB, nativeDataDir, closeNative, err := openReadonlyNativeProbeDB(chainDB)
	if err != nil {
		return nil, err
	}
	defer closeNative()
	native, err := inspectNativeReadonlyHead(nativeDB, normalizedChainDB, nativeDataDir)
	if err != nil {
		return nil, err
	}

	evmDB, normalizedChaindata, closeEVM, err := openReadonlyEVMProbeDB(chaindata)
	if err != nil {
		return nil, err
	}
	defer closeEVM()
	evmOpts := evmReadonlyProbeOptions{
		ExpectChainID:   opts.ExpectChainID,
		ExpectGenesis:   opts.ExpectEVMGenesisHash,
		ExpectNumber:    opts.ExpectEVMNumber,
		ExpectNumberSet: opts.ExpectEVMNumberSet,
		ExpectHash:      opts.ExpectEVMHash,
		ExpectRoot:      opts.ExpectEVMRoot,
		RequireState:    opts.RequireEVMState,
		RequireReceipts: true,
		ScanBack:        opts.ScanBack,
	}
	evm, err := inspectEVMReadonlyHead(evmDB, normalizedChaindata, evmOpts)
	if err != nil {
		return nil, err
	}

	report := &portableDatasetValidateReport{
		DataDir:   datadir,
		ChainDB:   normalizedChainDB,
		Chaindata: normalizedChaindata,
		SpecFile:  opts.SpecFile,
		Native:    native,
		EVM:       evm,
	}
	if opts.ArchivePath != "" {
		archive, archiveReasons := validatePortableArchive(opts.ArchivePath, opts.ArchiveSHA256, opts.ArchiveSizeBytes, opts.WriteSpecFile == "")
		report.Archive = archive
		report.Reasons = append(report.Reasons, archiveReasons...)
	}
	report.Reasons = append(report.Reasons, validatePortableDatasetReport(report, opts)...)
	report.Portable = len(report.Reasons) == 0
	native.DurableNative = len(validateNativeReadonlyProbe(native, nativeReadonlyOptionsFromPortable(opts))) == 0
	evm.PromotableEVM = len(validateEVMReadonlyProbe(evm, evmOpts)) == 0
	return report, nil
}

func applyPortableDatasetValidationSpec(opts portableDatasetValidateOptions) (portableDatasetValidateOptions, error) {
	if opts.SpecFile == "" {
		return opts, nil
	}
	data, err := os.ReadFile(opts.SpecFile)
	if err != nil {
		return opts, fmt.Errorf("read portable dataset validation spec %s: %w", opts.SpecFile, err)
	}
	var spec portableDatasetValidationSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return opts, fmt.Errorf("decode portable dataset validation spec %s: %w", opts.SpecFile, err)
	}
	if !opts.ExpectNativeOrderSet && spec.Native.Order != 0 {
		opts.ExpectNativeOrder = spec.Native.Order
		opts.ExpectNativeOrderSet = true
	}
	if opts.ExpectNativeHash == "" {
		opts.ExpectNativeHash = spec.Native.Hash
	}
	if opts.ExpectNativeRoot == "" {
		opts.ExpectNativeRoot = spec.Native.StateRoot
	}
	if !opts.ExpectEVMNumberSet && spec.EVM.Number != 0 {
		opts.ExpectEVMNumber = spec.EVM.Number
		opts.ExpectEVMNumberSet = true
	}
	if opts.ExpectEVMHash == "" {
		opts.ExpectEVMHash = spec.EVM.Hash
	}
	if opts.ExpectEVMRoot == "" {
		opts.ExpectEVMRoot = spec.EVM.Root
	}
	if opts.ArchiveSHA256 == "" {
		opts.ArchiveSHA256 = spec.Archive.SHA256
	}
	if opts.ArchiveSizeBytes == 0 {
		opts.ArchiveSizeBytes = spec.Archive.SizeBytes
	}
	return opts, nil
}

func portableDatasetValidationSpecFromReport(report *portableDatasetValidateReport, version string) portableDatasetValidationSpec {
	var spec portableDatasetValidationSpec
	spec.Version = strings.TrimSpace(version)
	if report == nil {
		return spec
	}
	if report.Native != nil {
		spec.Native.Order = report.Native.MainTipOrder
		spec.Native.Hash = report.Native.MainTipHash
		spec.Native.StateRoot = report.Native.MainTipStateRoot
	}
	if report.EVM != nil {
		spec.EVM.Number = report.EVM.Number
		spec.EVM.Hash = report.EVM.HeaderHash
		spec.EVM.Root = report.EVM.HeaderRoot
	}
	if report.Archive != nil {
		spec.Archive.Path = report.Archive.Path
		if report.Archive.ActualSHA256 != "" {
			spec.Archive.SHA256 = report.Archive.ActualSHA256
		} else {
			spec.Archive.SHA256 = report.Archive.ExpectedSHA256
		}
		if report.Archive.ActualSizeBytes != 0 {
			spec.Archive.SizeBytes = report.Archive.ActualSizeBytes
		} else {
			spec.Archive.SizeBytes = report.Archive.ExpectedSizeBytes
		}
	}
	return spec
}

func writePortableDatasetValidationSpec(path string, spec portableDatasetValidationSpec) error {
	encoded, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode portable dataset validation spec: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write portable dataset validation spec %s: %w", path, err)
	}
	return nil
}

func resolvePortableDatasetPaths(opts portableDatasetValidateOptions) (string, string, string, error) {
	datadir := opts.DataDir
	if datadir == "" && opts.ChainDB == "" && opts.Chaindata == "" {
		if config.Cfg == nil {
			return "", "", "", fmt.Errorf("datadir, chain-db, or chaindata is required when config is unavailable")
		}
		datadir = config.Cfg.DataDir
	}
	chainDB := opts.ChainDB
	if chainDB == "" && datadir != "" {
		chainDB = filepath.Join(datadir, chaindb.DBDirectoryName)
	}
	chaindata := opts.Chaindata
	if chaindata == "" && datadir != "" {
		chaindata = filepath.Join(datadir, evmchaindb.NodeName, evmchaindb.ChainDataName)
	}
	if chainDB == "" {
		return "", "", "", fmt.Errorf("chain-db is required when datadir is not provided")
	}
	if chaindata == "" {
		return "", "", "", fmt.Errorf("chaindata is required when datadir is not provided")
	}
	if datadir == "" {
		datadir = filepath.Dir(chainDB)
	}
	return chainDB, chaindata, datadir, nil
}

func validatePortableDatasetReport(report *portableDatasetValidateReport, opts portableDatasetValidateOptions) []string {
	var reasons []string
	reasons = append(reasons, validateNativeReadonlyProbe(report.Native, nativeReadonlyOptionsFromPortable(opts))...)
	evmOpts := evmReadonlyProbeOptions{
		ExpectChainID:   opts.ExpectChainID,
		ExpectGenesis:   opts.ExpectEVMGenesisHash,
		ExpectNumber:    opts.ExpectEVMNumber,
		ExpectNumberSet: opts.ExpectEVMNumberSet,
		ExpectHash:      opts.ExpectEVMHash,
		ExpectRoot:      opts.ExpectEVMRoot,
		RequireState:    opts.RequireEVMState,
		RequireReceipts: true,
	}
	reasons = append(reasons, validateEVMReadonlyProbe(report.EVM, evmOpts)...)
	if report.Native.MainTipEVMNumber != report.EVM.Number {
		reasons = append(reasons, fmt.Sprintf("native-recorded EVM number %d differs from EVM chaindata number %d", report.Native.MainTipEVMNumber, report.EVM.Number))
	}
	if !equalHex(report.Native.MainTipEVMHash, report.EVM.HeaderHash) {
		reasons = append(reasons, fmt.Sprintf("native-recorded EVM hash %s differs from EVM chaindata hash %s", report.Native.MainTipEVMHash, report.EVM.HeaderHash))
	}
	if !equalHex(report.Native.MainTipEVMRoot, report.EVM.HeaderRoot) {
		reasons = append(reasons, fmt.Sprintf("native-recorded EVM root %s differs from EVM chaindata root %s", report.Native.MainTipEVMRoot, report.EVM.HeaderRoot))
	}
	if opts.ScanBack > 0 && report.EVM.LatestStateful != nil && !report.EVM.LatestStateful.IsTarget {
		reasons = append(reasons, fmt.Sprintf("latest stateful EVM block %d is behind selected target %d", report.EVM.LatestStateful.Number, report.EVM.Number))
	}
	return reasons
}

func validatePortableArchive(path, expectedSHA string, expectedSize uint64, requireExpectations bool) (*portableArchiveValidation, []string) {
	result := &portableArchiveValidation{
		Path:              path,
		ExpectedSHA256:    strings.TrimSpace(expectedSHA),
		ExpectedSizeBytes: expectedSize,
	}
	var reasons []string
	info, err := os.Stat(path)
	if err != nil {
		reasons = append(reasons, fmt.Sprintf("archive %s is not readable: %v", path, err))
		return result, reasons
	}
	result.ActualSizeBytes = uint64(info.Size())
	if expectedSize > 0 && result.ActualSizeBytes != expectedSize {
		reasons = append(reasons, fmt.Sprintf("archive size %d differs from expected %d", result.ActualSizeBytes, expectedSize))
	}
	actual, err := sha256File(path)
	if err != nil {
		reasons = append(reasons, fmt.Sprintf("hash archive %s: %v", path, err))
		return result, reasons
	}
	result.ActualSHA256 = actual
	if result.ExpectedSHA256 == "" {
		if requireExpectations {
			reasons = append(reasons, "archive SHA256 expectation is required with archive-path")
		}
	} else if !strings.EqualFold(actual, result.ExpectedSHA256) {
		reasons = append(reasons, fmt.Sprintf("archive SHA256 %s differs from expected %s", actual, result.ExpectedSHA256))
	}
	result.Passed = len(reasons) == 0
	return result, reasons
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func nativeReadonlyOptionsFromPortable(opts portableDatasetValidateOptions) nativeReadonlyProbeOptions {
	return nativeReadonlyProbeOptions{
		ExpectGenesisHash:    opts.ExpectGenesisHash,
		ExpectOrder:          opts.ExpectNativeOrder,
		ExpectOrderSet:       opts.ExpectNativeOrderSet,
		ExpectHash:           opts.ExpectNativeHash,
		ExpectStateRoot:      opts.ExpectNativeRoot,
		ExpectEVMNumber:      opts.ExpectEVMNumber,
		ExpectEVMNumberSet:   opts.ExpectEVMNumberSet,
		ExpectEVMHash:        opts.ExpectEVMHash,
		ExpectEVMRoot:        opts.ExpectEVMRoot,
		RequireCleanShutdown: opts.RequireCleanShutdown,
	}
}
