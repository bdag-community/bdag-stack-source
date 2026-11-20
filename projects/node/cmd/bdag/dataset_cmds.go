package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	evmbdag "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/dataset"
	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	bdagcommon "github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/version"
	ethrawdb "github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/urfave/cli/v2"
)

type datasetInspectionReport struct {
	Schema      string                         `json:"schema"`
	Portable    bool                           `json:"portable"`
	Diagnostics []dataset.Diagnostic           `json:"diagnostics,omitempty"`
	Semantic    *portableDatasetValidateReport `json:"semantic"`
}

type datasetManifestCreateReport struct {
	Schema              string `json:"schema"`
	DatasetRoot         string `json:"dataset_root"`
	ManifestPath        string `json:"manifest_path"`
	ManifestSHA256      string `json:"manifest_sha256"`
	DirectoryRootSHA256 string `json:"directory_root_sha256"`
	Files               int    `json:"files"`
	Bytes               uint64 `json:"bytes"`
}

type datasetVerificationReport struct {
	Schema         string                         `json:"schema"`
	Accepted       bool                           `json:"accepted"`
	ByteIntegrity  *dataset.Verification          `json:"byte_integrity,omitempty"`
	Semantic       *portableDatasetValidateReport `json:"semantic,omitempty"`
	Diagnostics    []dataset.Diagnostic           `json:"diagnostics,omitempty"`
	ReceiptPath    string                         `json:"receipt_path,omitempty"`
	ReceiptSHA256  string                         `json:"receipt_sha256,omitempty"`
	VerifierSHA256 string                         `json:"verifier_sha256,omitempty"`
}

func datasetCmd() *cli.Command {
	return &cli.Command{
		Name:        "dataset",
		Category:    "Dataset",
		Usage:       "inspect, identify, and verify owner-selected BlockDAG datasets",
		Description: "Dataset validity comes from exact bytes plus native/EVM semantic consistency. Paths, numeric UIDs, source nodes, and optional publisher signatures are not authorization.",
		Subcommands: []*cli.Command{
			{
				Name:   "inspect",
				Usage:  "inspect native and EVM database consistency without starting the node",
				Before: snapBefore,
				Flags: append(bdagcommon.LocationFlags(),
					&cli.StringFlag{Name: "chain-db", Usage: "native BdagChain directory; overrides --datadir"},
					&cli.StringFlag{Name: "chaindata", Usage: "EVM chaindata directory; overrides --datadir"},
					&cli.BoolFlag{Name: "require-clean-shutdown", Value: true, Usage: "require no applicable interrupted operation marker"},
					&cli.BoolFlag{Name: "require-evm-state", Value: true, Usage: "require the selected EVM state root"},
					&cli.Uint64Flag{Name: "scan-back", Usage: "also scan backward for the latest stateful EVM block"},
				),
				Action: runDatasetInspect,
			},
			{
				Name:  "copy",
				Usage: "copy a manifest-bound dataset without stopping or mutating the source",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "source", Usage: "source dataset directory", Required: true},
					&cli.StringFlag{Name: "destination", Usage: "candidate destination directory", Required: true},
					&cli.StringFlag{Name: "manifest", Usage: "source manifest; defaults to <source>/DATASET-MANIFEST.json"},
					&cli.StringFlag{Name: "expect-manifest-sha256", Usage: "optional owner-selected exact manifest SHA-256"},
				},
				Action: runDatasetCopy,
			},
			{
				Name:  "manifest",
				Usage: "create deterministic dataset manifests",
				Subcommands: []*cli.Command{
					{
						Name:   "create",
						Usage:  "hash every included regular file after native/EVM semantic inspection",
						Before: snapBefore,
						Flags: append(bdagcommon.LocationFlags(),
							&cli.StringFlag{Name: "output", Usage: "manifest output path; defaults to <datadir>/DATASET-MANIFEST.json"},
							&cli.StringFlag{Name: "source-uri", Usage: "optional informational source URI; never authorization"},
							&cli.StringFlag{Name: "source-node-identity", Usage: "optional informational source node identity; never authorization"},
							&cli.BoolFlag{Name: "include-current-checkpoint", Usage: "record the current six-field native/EVM boundary as the canonical checkpoint"},
						),
						Action: runDatasetManifestCreate,
					},
				},
			},
			{
				Name:   "verify",
				Usage:  "verify manifest bytes, every included file, and native/EVM chain semantics",
				Before: snapBefore,
				Flags: append(bdagcommon.LocationFlags(),
					&cli.StringFlag{Name: "manifest", Usage: "manifest path; defaults to <datadir>/DATASET-MANIFEST.json"},
					&cli.StringFlag{Name: "expect-manifest-sha256", Usage: "optional owner-selected exact manifest SHA-256"},
					&cli.StringFlag{Name: "receipt", Usage: "canonical semantic receipt output; defaults to <datadir>/DATASET-VERIFICATION-RECEIPT.json"},
					&cli.BoolFlag{Name: "require-clean-shutdown", Value: true, Usage: "require no applicable interrupted operation marker"},
				),
				Action: runDatasetVerify,
			},
		},
	}
}

func runDatasetCopy(ctx *cli.Context) error {
	source := strings.TrimSpace(ctx.String("source"))
	manifestPath := strings.TrimSpace(ctx.String("manifest"))
	if manifestPath == "" {
		manifestPath = filepath.Join(source, dataset.DefaultManifestFilename)
	}
	report, err := dataset.Copy(datasetCommandContext(ctx), source, ctx.String("destination"), manifestPath, ctx.String("expect-manifest-sha256"))
	if report != nil {
		if printErr := printDatasetJSON(report); printErr != nil && err == nil {
			return printErr
		}
	}
	return err
}

func activeDatasetChainIdentity() (dataset.ChainIdentity, error) {
	if params.ActiveNetParams == nil || params.ActiveNetParams.Params == nil || params.ActiveNetParams.GenesisHash == nil {
		return dataset.ChainIdentity{}, fmt.Errorf("active BlockDAG network identity is unavailable")
	}
	chainConfig := evmbdag.ChainConfig()
	if chainConfig == nil || chainConfig.ChainID == nil || !chainConfig.ChainID.IsUint64() {
		return dataset.ChainIdentity{}, fmt.Errorf("active EVM chain identity is unavailable")
	}
	evmGenesis := evmbdag.CurrentGenesis()
	if evmGenesis == nil {
		return dataset.ChainIdentity{}, fmt.Errorf("active EVM genesis identity is unavailable")
	}
	return dataset.ChainIdentity{
		ChainID:        chainConfig.ChainID.Uint64(),
		Network:        params.ActiveNetParams.Name,
		GenesisHash:    params.ActiveNetParams.GenesisHash.String(),
		EVMGenesisHash: evmGenesis.ToBlock().Hash().String(),
	}, nil
}

func portableOptionsForActiveDataset(dataDir, chainDB, chaindata string, requireClean bool) (portableDatasetValidateOptions, error) {
	identity, err := activeDatasetChainIdentity()
	if err != nil {
		return portableDatasetValidateOptions{}, err
	}
	return portableDatasetValidateOptions{
		DataDir:              dataDir,
		ChainDB:              chainDB,
		Chaindata:            chaindata,
		ExpectChainID:        strconv.FormatUint(identity.ChainID, 10),
		ExpectGenesisHash:    identity.GenesisHash,
		ExpectEVMGenesisHash: identity.EVMGenesisHash,
		RequireCleanShutdown: requireClean,
		RequireEVMState:      true,
	}, nil
}

func datasetCommandDataDir(ctx *cli.Context) string {
	if config.Cfg != nil && strings.TrimSpace(config.Cfg.DataDir) != "" {
		return config.Cfg.DataDir
	}
	if ctx != nil {
		return ctx.String("datadir")
	}
	return ""
}

func datasetCommandContext(ctx *cli.Context) context.Context {
	if ctx != nil && ctx.Context != nil {
		return ctx.Context
	}
	return context.Background()
}

func printDatasetJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func datasetVerifierVersion() string {
	value := strings.TrimSpace(version.String())
	if value == "" {
		return "development"
	}
	return value
}

func semanticDiagnostic(reason string) dataset.Diagnostic {
	lower := strings.ToLower(reason)
	classification := "semantic_chain_mismatch"
	switch {
	case strings.Contains(lower, "genesis") || strings.Contains(lower, "chain id"):
		classification = "network_genesis_mismatch"
	case strings.Contains(lower, "malformed marker"):
		classification = "malformed_marker"
	case strings.Contains(lower, "shutdown marker") || strings.Contains(lower, "interrupted operation"):
		classification = "applicable_interrupted_operation"
	case strings.Contains(lower, "native-recorded evm"):
		classification = "native_evm_boundary_mismatch"
	case strings.Contains(lower, "freezer"):
		classification = "freezer_ahead_of_head"
	case strings.Contains(lower, "state trie") || strings.Contains(lower, "stateful evm"):
		classification = "missing_state"
	case strings.Contains(lower, "receipt") || strings.Contains(lower, "body"):
		classification = "incomplete_copy"
	case strings.Contains(lower, "database format"):
		classification = "unsupported_database_format"
	}
	return dataset.Diagnostic{Classification: classification, Message: reason, RepairableCopy: true}
}

func runDatasetInspect(ctx *cli.Context) error {
	opts, err := portableOptionsForActiveDataset(datasetCommandDataDir(ctx), ctx.String("chain-db"), ctx.String("chaindata"), ctx.Bool("require-clean-shutdown"))
	if err != nil {
		return err
	}
	opts.RequireEVMState = ctx.Bool("require-evm-state")
	opts.ScanBack = ctx.Uint64("scan-back")
	report, err := inspectPortableDataset(opts)
	if err != nil {
		return err
	}
	result := datasetInspectionReport{Schema: "blockdag-dataset-inspection/v1", Portable: report.Portable, Semantic: report}
	for _, reason := range report.Reasons {
		result.Diagnostics = append(result.Diagnostics, semanticDiagnostic(reason))
	}
	if err := printDatasetJSON(result); err != nil {
		return err
	}
	if !report.Portable {
		return fmt.Errorf("dataset semantic inspection failed")
	}
	return nil
}

func runDatasetManifestCreate(ctx *cli.Context) error {
	opts, err := portableOptionsForActiveDataset(datasetCommandDataDir(ctx), "", "", true)
	if err != nil {
		return err
	}
	semantic, err := inspectPortableDataset(opts)
	if err != nil {
		return err
	}
	if !semantic.Portable {
		result := datasetInspectionReport{Schema: "blockdag-dataset-inspection/v1", Portable: false, Semantic: semantic}
		for _, reason := range semantic.Reasons {
			result.Diagnostics = append(result.Diagnostics, semanticDiagnostic(reason))
		}
		_ = printDatasetJSON(result)
		return fmt.Errorf("refusing to manifest a semantically invalid dataset")
	}
	identity, err := activeDatasetChainIdentity()
	if err != nil {
		return err
	}
	nativeFormat := bdagrawdb.PreexistingDatabase(semantic.ChainDB)
	evmFormat := ethrawdb.PreexistingDatabase(semantic.Chaindata)
	manifest := dataset.Manifest{
		Chain: identity,
		Native: dataset.NativeState{
			Order: semantic.Native.MainTipOrder, Height: semantic.Native.MainTipHeight,
			Hash: semantic.Native.MainTipHash, StateRoot: semantic.Native.MainTipStateRoot,
			DatabaseFormat: nativeFormat,
		},
		EVM: dataset.EVMState{
			Number: semantic.EVM.Number, Hash: semantic.EVM.HeaderHash,
			StateRoot: semantic.EVM.HeaderRoot, DatabaseFormat: evmFormat,
		},
		Source: dataset.SourceInfo{SoftwareVersion: datasetVerifierVersion(), URI: ctx.String("source-uri"), NodeIdentity: ctx.String("source-node-identity")},
	}
	if ctx.Bool("include-current-checkpoint") {
		manifest.CanonicalCheckpoint = &dataset.Boundary{Native: manifest.Native, EVM: manifest.EVM}
	}
	manifested, err := dataset.Create(datasetCommandContext(ctx), semantic.DataDir, manifest)
	if err != nil {
		return err
	}
	output := strings.TrimSpace(ctx.String("output"))
	if output == "" {
		output = filepath.Join(semantic.DataDir, dataset.DefaultManifestFilename)
	}
	manifestHash, err := dataset.WriteFile(output, manifested)
	if err != nil {
		return err
	}
	var bytes uint64
	for _, file := range manifested.Files {
		bytes += file.Size
	}
	return printDatasetJSON(datasetManifestCreateReport{
		Schema: "blockdag-dataset-manifest-create/v1", DatasetRoot: semantic.DataDir,
		ManifestPath: output, ManifestSHA256: manifestHash,
		DirectoryRootSHA256: manifested.DirectoryRootSHA256, Files: len(manifested.Files), Bytes: bytes,
	})
}

func runDatasetVerify(ctx *cli.Context) error {
	dataDir := strings.TrimSpace(datasetCommandDataDir(ctx))
	manifestPath := strings.TrimSpace(ctx.String("manifest"))
	if manifestPath == "" {
		manifestPath = filepath.Join(dataDir, dataset.DefaultManifestFilename)
	}
	manifest, _, manifestHash, err := dataset.Load(manifestPath)
	if err != nil {
		return err
	}
	result := datasetVerificationReport{Schema: "blockdag-dataset-verification/v1"}
	identity, err := activeDatasetChainIdentity()
	if err != nil {
		return err
	}
	if manifest.Chain.ChainID != identity.ChainID || manifest.Chain.Network != identity.Network || !equalHex(manifest.Chain.GenesisHash, identity.GenesisHash) || !equalHex(manifest.Chain.EVMGenesisHash, identity.EVMGenesisHash) {
		result.Diagnostics = append(result.Diagnostics, dataset.Diagnostic{
			Classification: "network_genesis_mismatch", Message: "manifest chain identity differs from the selected BlockDAG network",
			Expected: fmt.Sprintf("%d:%s:%s:%s", identity.ChainID, identity.Network, identity.GenesisHash, identity.EVMGenesisHash),
			Actual:   fmt.Sprintf("%d:%s:%s:%s", manifest.Chain.ChainID, manifest.Chain.Network, manifest.Chain.GenesisHash, manifest.Chain.EVMGenesisHash), RepairableCopy: false,
		})
	}
	byteReport, err := dataset.Verify(datasetCommandContext(ctx), dataDir, manifestPath, ctx.String("expect-manifest-sha256"))
	if err != nil {
		return err
	}
	result.ByteIntegrity = byteReport
	result.Diagnostics = append(result.Diagnostics, byteReport.Diagnostics...)
	if byteReport.Valid && len(result.Diagnostics) == 0 {
		opts, err := portableOptionsForActiveDataset(dataDir, "", "", ctx.Bool("require-clean-shutdown"))
		if err != nil {
			return err
		}
		opts.ExpectNativeOrder, opts.ExpectNativeOrderSet = manifest.Native.Order, true
		opts.ExpectNativeHash, opts.ExpectNativeRoot = manifest.Native.Hash, manifest.Native.StateRoot
		opts.ExpectEVMNumber, opts.ExpectEVMNumberSet = manifest.EVM.Number, true
		opts.ExpectEVMHash, opts.ExpectEVMRoot = manifest.EVM.Hash, manifest.EVM.StateRoot
		semantic, inspectErr := inspectPortableDataset(opts)
		if inspectErr != nil {
			return inspectErr
		}
		result.Semantic = semantic
		for _, reason := range semantic.Reasons {
			result.Diagnostics = append(result.Diagnostics, semanticDiagnostic(reason))
		}
		if semantic.Native.MainTipHeight != manifest.Native.Height {
			result.Diagnostics = append(result.Diagnostics, semanticDiagnostic(fmt.Sprintf("native height %d differs from manifest %d", semantic.Native.MainTipHeight, manifest.Native.Height)))
		}
		if semantic.Native.DatabaseFormat != "" && semantic.Native.DatabaseFormat != manifest.Native.DatabaseFormat {
			result.Diagnostics = append(result.Diagnostics, semanticDiagnostic(fmt.Sprintf("native database format %s differs from manifest %s", semantic.Native.DatabaseFormat, manifest.Native.DatabaseFormat)))
		}
		if semantic.EVM.DatabaseFormat != "" && semantic.EVM.DatabaseFormat != manifest.EVM.DatabaseFormat {
			result.Diagnostics = append(result.Diagnostics, semanticDiagnostic(fmt.Sprintf("EVM database format %s differs from manifest %s", semantic.EVM.DatabaseFormat, manifest.EVM.DatabaseFormat)))
		}
		if manifest.CanonicalCheckpoint != nil && (manifest.CanonicalCheckpoint.Native != manifest.Native || manifest.CanonicalCheckpoint.EVM != manifest.EVM) {
			result.Diagnostics = append(result.Diagnostics, semanticDiagnostic("canonical checkpoint does not equal the schema v1 dataset boundary"))
		}
	}
	result.Accepted = result.ByteIntegrity != nil && result.ByteIntegrity.Valid && result.Semantic != nil && result.Semantic.Portable && len(result.Diagnostics) == 0
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve verifier executable: %w", err)
	}
	verifierHash, err := dataset.FileSHA256(datasetCommandContext(ctx), executable)
	if err != nil {
		return fmt.Errorf("hash verifier executable: %w", err)
	}
	result.VerifierSHA256 = verifierHash
	receipt := &dataset.Receipt{
		Schema: dataset.ReceiptSchema, DatasetKind: "directory", DatasetRoot: byteReport.DatasetRoot,
		ManifestSHA256: manifestHash, DirectoryRootSHA256: byteReport.DirectoryRootSHA256,
		Chain: manifest.Chain, Native: manifest.Native, EVM: manifest.EVM,
		Verifier:  dataset.VerifierIdentity{BinarySHA256: verifierHash, Version: datasetVerifierVersion()},
		FileCount: byteReport.FileCount, ByteCount: byteReport.ByteCount, Accepted: result.Accepted,
		Diagnostics: append([]dataset.Diagnostic(nil), result.Diagnostics...),
		VerifiedAt:  time.Now().UTC().Format(time.RFC3339Nano), ManifestSource: manifest.ProducerSignature,
	}
	receiptPath := strings.TrimSpace(ctx.String("receipt"))
	if receiptPath == "" {
		receiptPath = filepath.Join(dataDir, dataset.DefaultReceiptFilename)
	}
	receiptHash, err := dataset.WriteReceipt(receiptPath, receipt)
	if err != nil {
		return fmt.Errorf("write dataset verification receipt: %w", err)
	}
	result.ReceiptPath = receiptPath
	result.ReceiptSHA256 = receiptHash
	if err := printDatasetJSON(result); err != nil {
		return err
	}
	if !result.Accepted {
		return fmt.Errorf("dataset verification failed")
	}
	return nil
}
