package snapshot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	ethrawdb "github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
)

// ancient export/import: go-ethereum's freezerdb does not expose ancient rows
// through NewIterator — only the KV half is visible. Snapshots that only
// streamed iterator keys therefore dropped frozen chain segments, leaving
// importers with KV state that expects a non-empty freezer (geth aborts with
// "ancient chain segments already extracted...").
//
// The BDAG chain database uses database/rawdb with tables headers/blocks/dagblocks.
// The embedded EVM chain uses geth rawdb with hashes/headers/bodies/receipts/diffs.
// Export/import must use the layout that matches each source.

var ancientSnapKeyPrefix = []byte{0xff, 'b', 'd', 'a', 'g', 's', 'n', 'a', 'p', 1}

func encodeAncientSnapKey(kind string, num uint64) ([]byte, error) {
	if len(kind) > 255 {
		return nil, fmt.Errorf("snapshot: ancient kind name too long")
	}
	b := make([]byte, 0, len(ancientSnapKeyPrefix)+1+len(kind)+8)
	b = append(b, ancientSnapKeyPrefix...)
	b = append(b, byte(len(kind)))
	b = append(b, kind...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], num)
	b = append(b, n[:]...)
	return b, nil
}

func decodeAncientSnapKey(key []byte) (kind string, num uint64, ok bool) {
	if !bytes.HasPrefix(key, ancientSnapKeyPrefix) {
		return "", 0, false
	}
	rest := key[len(ancientSnapKeyPrefix):]
	if len(rest) < 1+8 {
		return "", 0, false
	}
	kl := int(rest[0])
	if len(rest) < 1+kl+8 {
		return "", 0, false
	}
	kind = string(rest[1 : 1+kl])
	num = binary.BigEndian.Uint64(rest[1+kl:])
	return kind, num, true
}

var bdagChainFreezerExportOrder = []string{
	bdagrawdb.ChainFreezerHeaderTable,
	bdagrawdb.ChainFreezerBlockTable,
	bdagrawdb.ChainFreezerDAGBlockTable,
}

var evmChainFreezerExportOrder = []string{
	ethrawdb.ChainFreezerHashTable,
	ethrawdb.ChainFreezerHeaderTable,
	ethrawdb.ChainFreezerBodiesTable,
	ethrawdb.ChainFreezerReceiptTable,
	ethrawdb.ChainFreezerDifficultyTable,
}

// Pathdb state-history table names (must match go-ethereum core/rawdb ancient_scheme).
var evmStateHistoryExportOrder = []string{
	"history.meta",
	"account.index",
	"storage.index",
	"account.data",
	"storage.data",
}

func kindRankForSource(sourceName, kind string) int {
	switch sourceName {
	case SourceChain:
		switch kind {
		case bdagrawdb.ChainFreezerHeaderTable:
			return 0
		case bdagrawdb.ChainFreezerBlockTable:
			return 1
		case bdagrawdb.ChainFreezerDAGBlockTable:
			return 2
		default:
			return 99
		}
	case SourceEVM:
		switch kind {
		case ethrawdb.ChainFreezerHashTable:
			return 0
		case ethrawdb.ChainFreezerHeaderTable:
			return 1
		case ethrawdb.ChainFreezerBodiesTable:
			return 2
		case ethrawdb.ChainFreezerReceiptTable:
			return 3
		case ethrawdb.ChainFreezerDifficultyTable:
			return 4
		case "history.meta":
			return 5
		case "account.index":
			return 6
		case "storage.index":
			return 7
		case "account.data":
			return 8
		case "storage.data":
			return 9
		default:
			return 99
		}
	default:
		return 99
	}
}

type ancientReader interface {
	Ancients() (uint64, error)
	Ancient(kind string, number uint64) ([]byte, error)
}

// exportAncientTables streams frozen chain segments as synthetic KV records
// (see decodeAncientSnapKey). The source name selects BDAG vs geth freezer
// layout. It is a no-op when db has no ancients or does not support reads.
//
// For SourceEVM, when the pathdb state freezer has Tail()>0, synthetic state
// keys are omitted (useRawTar=true); the writer must append a raw state/
// tar.gz (format v4+).
func exportAncientTables(sourceName string, db ethdb.Database, emit func(key, val []byte) error) (useRawTar bool, err error) {
	switch sourceName {
	case SourceChain:
		return false, exportFreezerTables(db, bdagChainFreezerExportOrder, emit)
	case SourceEVM:
		if err := exportFreezerTables(db, evmChainFreezerExportOrder, emit); err != nil {
			return false, err
		}
		return exportEVMStateHistoryFreezer(db, emit)
	default:
		return false, nil
	}
}

func exportFreezerTables(db ethdb.Database, kinds []string, emit func(key, val []byte) error) error {
	ar, ok := db.(ancientReader)
	if !ok {
		return nil
	}
	return exportFreezerTablesFromReader(ar, kinds, emit)
}

func exportFreezerTablesFromReader(ar ancientReader, kinds []string, emit func(key, val []byte) error) error {
	n, err := ar.Ancients()
	if err != nil || n == 0 {
		return nil
	}
	tail := uint64(0)
	if tr, ok := any(ar).(interface{ Tail() (uint64, error) }); ok {
		tail, err = tr.Tail()
		if err != nil {
			return fmt.Errorf("snapshot: read ancient tail: %w", err)
		}
	}
	if tail >= n {
		return nil
	}
	// Ancient indices are global: after TruncateTail, the first stored row is at
	// index Tail(), not 0 (freezer retrieve rejects hidden indices).
	for i := tail; i < n; i++ {
		for _, kind := range kinds {
			blob, err := ar.Ancient(kind, i)
			if err != nil {
				return fmt.Errorf("snapshot: read ancient %s #%d: %w", kind, i, err)
			}
			key, err := encodeAncientSnapKey(kind, i)
			if err != nil {
				return err
			}
			if err := emit(key, blob); err != nil {
				return err
			}
		}
	}
	return nil
}

// exportEVMStateHistoryFreezer streams pathdb state histories from ancient/state
// as synthetic keys when Tail()==0. When Tail()>0, rows use global indices that
// cannot be appended starting at 0 on an empty importer freezer; it returns
// useRawTar=true and emits nothing so the snapshot writer can pack raw files.
func exportEVMStateHistoryFreezer(db ethdb.Database, emit func(key, val []byte) error) (useRawTar bool, err error) {
	ad, ok := db.(interface{ AncientDatadir() (string, error) })
	if !ok {
		return false, nil
	}
	ancientRoot, err := ad.AncientDatadir()
	if err != nil || ancientRoot == "" {
		return false, nil
	}
	// Hash-state databases keep trie nodes in KV storage and need not have
	// path-state history. Only skip a positively identified hash-state DB
	// with no history bytes; existing history must still be exported/checked.
	empty, err := emptyHashStateHistory(db, ancientRoot)
	if err != nil {
		return false, err
	}
	if empty {
		return false, nil
	}
	sf, err := ethrawdb.NewStateFreezer(ancientRoot, false, true)
	if err != nil {
		return false, fmt.Errorf("snapshot: open state history freezer: %w", err)
	}
	defer sf.Close()
	n, err := sf.Ancients()
	if err != nil || n == 0 {
		return false, nil
	}
	tail := uint64(0)
	if tr, ok := any(sf).(interface{ Tail() (uint64, error) }); ok {
		tail, err = tr.Tail()
		if err != nil {
			return false, fmt.Errorf("snapshot: read state ancient tail: %w", err)
		}
	}
	if tail > 0 {
		if tail >= n {
			return false, nil
		}
		return true, nil
	}
	return false, exportFreezerTablesFromReader(sf, evmStateHistoryExportOrder, emit)
}

func emptyHashStateHistory(db ethdb.Database, ancientRoot string) (bool, error) {
	if ethrawdb.ReadStateScheme(db) != ethrawdb.HashScheme {
		return false, nil
	}
	dir := filepath.Join(ancientRoot, ethrawdb.MerkleStateFreezerName)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("snapshot: inspect state history: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("snapshot: state history is not a regular directory: %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("snapshot: list state history: %w", err)
	}
	if len(entries) == 0 {
		return true, nil
	}
	if len(entries) != 1 || entries[0].Name() != "FLOCK" {
		return false, nil
	}
	lock, err := os.Lstat(filepath.Join(dir, "FLOCK"))
	if err != nil {
		return false, fmt.Errorf("snapshot: inspect state history lock: %w", err)
	}
	if !lock.Mode().IsRegular() {
		return false, fmt.Errorf("snapshot: state history lock is not a regular file: %s", dir)
	}
	return lock.Size() == 0, nil
}

// PartitionEVMAncientRecs splits synthetic ancient keys into chain-freezer vs state-freezer sets.
func PartitionEVMAncientRecs(recs []keyValue) (chain, state []keyValue) {
	for _, kv := range recs {
		kind, _, ok := decodeAncientSnapKey(kv.Key)
		if !ok {
			continue
		}
		r := kindRankForSource(SourceEVM, kind)
		switch {
		case r >= 0 && r <= 4:
			chain = append(chain, kv)
		case r >= 5 && r <= 9:
			state = append(state, kv)
		}
	}
	return chain, state
}

// importAncientTables applies synthetic ancient records via ModifyAncients.
// sourceName must match the archive section (chain vs evm) so "headers" keys
// sort in the correct table order for that freezer.
func importAncientTables(sourceName string, aw ethdb.AncientWriter, recs []keyValue) error {
	if len(recs) == 0 {
		return nil
	}
	if aw == nil {
		return fmt.Errorf("snapshot: nil ancient writer")
	}
	type item struct {
		kind string
		num  uint64
		val  []byte
		rank int
	}
	items := make([]item, 0, len(recs))
	for _, kv := range recs {
		kind, num, ok := decodeAncientSnapKey(kv.Key)
		if !ok {
			continue
		}
		items = append(items, item{
			kind: kind,
			num:  num,
			val:  kv.Value,
			rank: kindRankForSource(sourceName, kind),
		})
	}
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].num != items[j].num {
			return items[i].num < items[j].num
		}
		return items[i].rank < items[j].rank
	})
	if err := importAncientEmptyFreezerGuard(aw, items[0].num); err != nil {
		return err
	}
	_, err := aw.ModifyAncients(func(op ethdb.AncientWriteOp) error {
		for _, it := range items {
			if err := op.AppendRaw(it.kind, it.num, it.val); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// ancientStreamImporter writes synthetic ancient records in bounded batches.
// Snapshot writers emit ancient records ordered by freezer item number, but a
// chunk boundary can split the records for a single item. Keep only the current
// item plus a bounded set of complete items in memory before flushing them.
type ancientStreamImporter struct {
	sourceName string
	aw         ethdb.AncientWriter
	maxBytes   int

	started      bool
	currentNum   uint64
	current      []keyValue
	currentBytes int

	pending      []keyValue
	pendingBytes int
}

func newAncientStreamImporter(sourceName string, aw ethdb.AncientWriter, maxBytes int) *ancientStreamImporter {
	if maxBytes <= 0 {
		maxBytes = 16 * 1024 * 1024
	}
	return &ancientStreamImporter{
		sourceName: sourceName,
		aw:         aw,
		maxBytes:   maxBytes,
	}
}

func (im *ancientStreamImporter) Add(kv keyValue) error {
	kind, num, ok := decodeAncientSnapKey(kv.Key)
	if !ok {
		return nil
	}
	if kindRankForSource(im.sourceName, kind) == 99 {
		return fmt.Errorf("snapshot: unknown ancient kind %q for source %q", kind, im.sourceName)
	}
	if !im.started {
		im.started = true
		im.currentNum = num
	} else if num < im.currentNum {
		return fmt.Errorf("snapshot: out-of-order ancient record %s #%d after #%d", kind, num, im.currentNum)
	} else if num > im.currentNum {
		im.queueCurrent()
		if im.pendingBytes >= im.maxBytes {
			if err := im.flushPending(); err != nil {
				return err
			}
		}
		im.currentNum = num
	}
	im.current = append(im.current, kv)
	im.currentBytes += len(kv.Key) + len(kv.Value)
	return nil
}

func (im *ancientStreamImporter) Finish() error {
	im.queueCurrent()
	return im.flushPending()
}

func (im *ancientStreamImporter) queueCurrent() {
	if len(im.current) == 0 {
		return
	}
	im.pending = append(im.pending, im.current...)
	im.pendingBytes += im.currentBytes
	im.current = nil
	im.currentBytes = 0
}

func (im *ancientStreamImporter) flushPending() error {
	if len(im.pending) == 0 {
		return nil
	}
	if err := importAncientTables(im.sourceName, im.aw, im.pending); err != nil {
		return err
	}
	im.pending = nil
	im.pendingBytes = 0
	return nil
}

// importAncientEmptyFreezerGuard rejects imports that would append starting at a
// non-zero ancient index into an empty freezer (AppendRaw must begin at 0).
// That situation arises when the export source had ancient tail truncation.
func importAncientEmptyFreezerGuard(aw ethdb.AncientWriter, minIndex uint64) error {
	if minIndex == 0 {
		return nil
	}
	type freezerHeadTail interface {
		Ancients() (uint64, error)
		Tail() (uint64, error)
	}
	ft, ok := aw.(freezerHeadTail)
	if !ok {
		return nil
	}
	n, err := ft.Ancients()
	if err != nil {
		return err
	}
	tail, err := ft.Tail()
	if err != nil {
		return err
	}
	if n == 0 && tail == 0 {
		return fmt.Errorf("snapshot: ancient segment starts at index %d but destination freezer is empty (source had ancient tail truncation); cannot import — repair or re-sync source so ancient Tail is 0, or copy raw ancient files", minIndex)
	}
	return nil
}
