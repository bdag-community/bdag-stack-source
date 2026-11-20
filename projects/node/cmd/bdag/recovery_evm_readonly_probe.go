package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/database/evmchaindb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
)

type evmReadonlyProbeOptions struct {
	Chaindata       string
	ExpectChainID   string
	ExpectGenesis   string
	Number          uint64
	NumberSet       bool
	ExpectNumber    uint64
	ExpectNumberSet bool
	ExpectHash      string
	ExpectRoot      string
	RequireState    bool
	RequireReceipts bool
	ScanBack        uint64
	PrintJSON       bool
}

type evmReadonlyHeadPointer struct {
	Hash        string `json:"hash,omitempty"`
	Number      uint64 `json:"number,omitempty"`
	NumberKnown bool   `json:"number_known"`
}

type evmReadonlyProbeResult struct {
	Chaindata         string                 `json:"chaindata"`
	DatabaseFormat    string                 `json:"database_format,omitempty"`
	ChainID           string                 `json:"chain_id,omitempty"`
	GenesisHash       string                 `json:"genesis_hash,omitempty"`
	TargetSource      string                 `json:"target_source"`
	Number            uint64                 `json:"number"`
	CanonicalHash     string                 `json:"canonical_hash"`
	HeaderHash        string                 `json:"header_hash"`
	HeaderRoot        string                 `json:"header_root"`
	HasBody           bool                   `json:"has_body"`
	HasState          bool                   `json:"has_state"`
	TransactionCount  uint64                 `json:"transaction_count"`
	ReceiptCount      uint64                 `json:"receipt_count"`
	ReceiptsComplete  bool                   `json:"receipts_complete"`
	Ancients          uint64                 `json:"ancients"`
	HeadHeader        evmReadonlyHeadPointer `json:"head_header"`
	HeadBlock         evmReadonlyHeadPointer `json:"head_block"`
	HeadPointersAgree bool                   `json:"head_pointers_agree"`
	FreezerCompatible bool                   `json:"freezer_compatible"`
	PromotableEVM     bool                   `json:"promotable_evm"`
	BlockSelection    string                 `json:"block_selection"`
	ScanBack          uint64                 `json:"scan_back,omitempty"`
	LatestStateful    *evmReadonlyBlockState `json:"latest_stateful,omitempty"`
}

type evmReadonlyBlockState struct {
	Number     uint64 `json:"number"`
	Hash       string `json:"hash"`
	Root       string `json:"root"`
	HasBody    bool   `json:"has_body"`
	HasState   bool   `json:"has_state"`
	Distance   uint64 `json:"distance_from_target"`
	IsTarget   bool   `json:"is_target"`
	TargetRoot string `json:"target_root,omitempty"`
}

func runEVMReadonlyHeadProbe(opts evmReadonlyProbeOptions) error {
	db, chaindata, closeFn, err := openReadonlyEVMProbeDB(opts.Chaindata)
	if err != nil {
		return err
	}
	defer closeFn()

	result, err := inspectEVMReadonlyHead(db, chaindata, opts)
	if err != nil {
		return err
	}
	reasons := validateEVMReadonlyProbe(result, opts)
	result.PromotableEVM = len(reasons) == 0
	if opts.PrintJSON {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("encode read-only EVM probe: %w", err)
		}
		fmt.Println(string(encoded))
	}
	log.Info("Read-only EVM chaindata probe",
		"promotableEVM", result.PromotableEVM,
		"number", result.Number,
		"hash", result.HeaderHash,
		"root", result.HeaderRoot,
		"hasState", result.HasState,
		"headBlock", result.HeadBlock.Hash,
		"headHeader", result.HeadHeader.Hash)
	if len(reasons) > 0 {
		return fmt.Errorf("read-only EVM probe failed: %s", strings.Join(reasons, "; "))
	}
	return nil
}

func openReadonlyEVMProbeDB(chaindata string) (ethdb.Database, string, func(), error) {
	if chaindata == "" {
		cfg := config.Cfg
		if cfg == nil {
			return nil, "", nil, fmt.Errorf("config is unavailable")
		}
		db, err := evmchaindb.Open(cfg.DataDir, evmchaindb.Options{ReadOnly: true})
		if err != nil {
			return nil, "", nil, fmt.Errorf("open read-only EVM chaindata: %w", err)
		}
		return db, evmchaindb.Path(cfg.DataDir), func() { db.Close() }, nil
	}
	db, err := rawdb.Open(rawdb.OpenOptions{
		Directory:         chaindata,
		AncientsDirectory: filepath.Join(chaindata, evmchaindb.AncientName),
		Cache:             128,
		Handles:           512,
		ReadOnly:          true,
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("open read-only EVM chaindata %s: %w", chaindata, err)
	}
	return db, chaindata, func() { db.Close() }, nil
}

func inspectEVMReadonlyHead(db ethdb.Database, chaindata string, opts evmReadonlyProbeOptions) (*evmReadonlyProbeResult, error) {
	headHeader := readEVMHeadPointer(db, rawdb.ReadHeadHeaderHash(db))
	headBlock := readEVMHeadPointer(db, rawdb.ReadHeadBlockHash(db))
	number := opts.Number
	targetSource := "requested"
	if !opts.NumberSet {
		targetSource = "head_block"
		if !headBlock.NumberKnown {
			targetSource = "head_header"
			if !headHeader.NumberKnown {
				return nil, fmt.Errorf("persisted EVM head block/header number is unavailable")
			}
			number = headHeader.Number
		} else {
			number = headBlock.Number
		}
	}

	canonicalHash := rawdb.ReadCanonicalHash(db, number)
	if canonicalHash == (common.Hash{}) {
		return nil, fmt.Errorf("canonical EVM hash unavailable at block %d", number)
	}
	header := rawdb.ReadHeader(db, canonicalHash, number)
	if header == nil || header.Number == nil {
		return nil, fmt.Errorf("canonical EVM header unavailable at block %d hash=%s", number, canonicalHash.String())
	}
	if header.Number.Uint64() != number || header.Hash() != canonicalHash {
		return nil, fmt.Errorf("canonical EVM header mismatch at block %d: canonical=%s header.number=%d header.hash=%s",
			number, canonicalHash.String(), header.Number.Uint64(), header.Hash().String())
	}
	ancients, err := db.Ancients()
	if err != nil {
		// In-memory and explicitly freezer-less databases report this fixed
		// go-ethereum sentinel text. They have no freezer tail, so zero is the
		// only coherent count. Real on-disk chaindata opened above includes an
		// ancient store and any other error remains a hard inspection failure.
		if err.Error() != "this operation is not supported" {
			return nil, fmt.Errorf("read EVM freezer count: %w", err)
		}
		ancients = 0
	}
	body := rawdb.ReadBody(db, canonicalHash, number)
	transactionCount := 0
	if body != nil {
		transactionCount = len(body.Transactions)
	}
	receiptCount := len(rawdb.ReadRawReceipts(db, canonicalHash, number))
	result := &evmReadonlyProbeResult{
		Chaindata:        chaindata,
		DatabaseFormat:   rawdb.PreexistingDatabase(chaindata),
		TargetSource:     targetSource,
		Number:           number,
		CanonicalHash:    canonicalHash.String(),
		HeaderHash:       header.Hash().String(),
		HeaderRoot:       header.Root.String(),
		HasBody:          body != nil,
		HasState:         rawdb.HasLegacyTrieNode(db, header.Root),
		TransactionCount: uint64(transactionCount),
		ReceiptCount:     uint64(receiptCount),
		ReceiptsComplete: body != nil && receiptCount == transactionCount,
		Ancients:         ancients,
		HeadHeader:       headHeader,
		HeadBlock:        headBlock,
		BlockSelection:   fmt.Sprintf("%s:%d", targetSource, number),
		ScanBack:         opts.ScanBack,
	}
	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	if genesisHash != (common.Hash{}) {
		result.GenesisHash = genesisHash.String()
		if chainConfig := rawdb.ReadChainConfig(db, genesisHash); chainConfig != nil && chainConfig.ChainID != nil {
			result.ChainID = chainConfig.ChainID.String()
		}
	}
	headCanonicalHash := common.Hash{}
	if headBlock.NumberKnown {
		headCanonicalHash = rawdb.ReadCanonicalHash(db, headBlock.Number)
	}
	result.HeadPointersAgree = headHeader.NumberKnown && headBlock.NumberKnown &&
		headHeader.Number == headBlock.Number && equalHex(headHeader.Hash, headBlock.Hash) &&
		headCanonicalHash != (common.Hash{}) && equalHex(headBlock.Hash, headCanonicalHash.String())
	result.FreezerCompatible = result.HeadPointersAgree && (ancients == 0 || ancients-1 <= headBlock.Number)
	if opts.ScanBack > 0 {
		latest, err := findLatestStatefulEVMBlock(db, number, opts.ScanBack)
		if err != nil {
			return nil, err
		}
		if latest != nil {
			latest.TargetRoot = header.Root.String()
		}
		result.LatestStateful = latest
	}
	return result, nil
}

func readEVMHeadPointer(db ethdb.Database, hash common.Hash) evmReadonlyHeadPointer {
	if hash == (common.Hash{}) {
		return evmReadonlyHeadPointer{}
	}
	pointer := evmReadonlyHeadPointer{Hash: hash.String()}
	if number := rawdb.ReadHeaderNumber(db, hash); number != nil {
		pointer.Number = *number
		pointer.NumberKnown = true
	}
	return pointer
}

func validateEVMReadonlyProbe(result *evmReadonlyProbeResult, opts evmReadonlyProbeOptions) []string {
	var reasons []string
	if opts.ExpectChainID != "" && result.ChainID != strings.TrimSpace(opts.ExpectChainID) {
		reasons = append(reasons, fmt.Sprintf("EVM chain ID %s differs from expected %s", result.ChainID, opts.ExpectChainID))
	}
	if opts.ExpectGenesis != "" && !equalHex(result.GenesisHash, opts.ExpectGenesis) {
		reasons = append(reasons, fmt.Sprintf("EVM genesis hash %s differs from expected %s", result.GenesisHash, opts.ExpectGenesis))
	}
	if !result.HeadPointersAgree {
		reasons = append(reasons, "EVM canonical header and persisted head block/header pointers do not agree")
	}
	if !result.FreezerCompatible {
		reasons = append(reasons, fmt.Sprintf("EVM freezer count %d is ahead of persisted head %d", result.Ancients, result.Number))
	}
	if opts.RequireState && !result.HasState {
		reasons = append(reasons, fmt.Sprintf("state trie root %s is not present", result.HeaderRoot))
	}
	if opts.RequireReceipts && !result.ReceiptsComplete {
		reasons = append(reasons, fmt.Sprintf("receipt count %d differs from transaction count %d", result.ReceiptCount, result.TransactionCount))
	}
	if opts.ExpectNumberSet && result.Number != opts.ExpectNumber {
		reasons = append(reasons, fmt.Sprintf("EVM number %d differs from expected %d", result.Number, opts.ExpectNumber))
	}
	if opts.ExpectHash != "" && !equalHex(result.HeaderHash, opts.ExpectHash) {
		reasons = append(reasons, fmt.Sprintf("EVM hash %s differs from expected %s", result.HeaderHash, opts.ExpectHash))
	}
	if opts.ExpectRoot != "" && !equalHex(result.HeaderRoot, opts.ExpectRoot) {
		reasons = append(reasons, fmt.Sprintf("EVM root %s differs from expected %s", result.HeaderRoot, opts.ExpectRoot))
	}
	return reasons
}

func findLatestStatefulEVMBlock(db ethdb.Database, target uint64, scanBack uint64) (*evmReadonlyBlockState, error) {
	from := target
	if scanBack < from {
		from -= scanBack
	} else {
		from = 0
	}
	for number := target; ; number-- {
		hash := rawdb.ReadCanonicalHash(db, number)
		if hash != (common.Hash{}) {
			header := rawdb.ReadHeader(db, hash, number)
			if header == nil || header.Number == nil {
				return nil, fmt.Errorf("canonical EVM header unavailable at block %d hash=%s", number, hash.String())
			}
			hasState := rawdb.HasLegacyTrieNode(db, header.Root)
			if hasState {
				return &evmReadonlyBlockState{
					Number:   number,
					Hash:     header.Hash().String(),
					Root:     header.Root.String(),
					HasBody:  rawdb.HasBody(db, hash, number),
					HasState: true,
					Distance: target - number,
					IsTarget: number == target,
				}, nil
			}
		}
		if number == from || number == 0 {
			return nil, nil
		}
	}
}
