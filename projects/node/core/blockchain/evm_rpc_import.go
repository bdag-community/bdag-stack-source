package blockchain

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

type EVMRPCImportOptions struct {
	AnchorEVMNumber uint64
	TargetOrder     uint64
	RPCURL          string
	CommitEvery     uint64
	ProgressEvery   uint64
	RPCTimeout      time.Duration
	RPCRetryMax     int
	RPCRetryDelay   time.Duration
	RPCFetchDelay   time.Duration
}

type EVMRPCImportResult struct {
	AnchorOrder          uint64
	StartOrder           uint64
	TargetOrder          uint64
	MainTipOrder         uint64
	Processed            uint64
	Imported             uint64
	SkippedExisting      uint64
	SkippedInvalid       uint64
	SkippedNoAdvance     uint64
	Commits              uint64
	FinalEVMNumber       uint64
	FinalEVMHash         common.Hash
	FinalEVMRoot         common.Hash
	SafeCheckpointNumber uint64
	SafeCheckpointHash   common.Hash
	SafeCheckpointRoot   common.Hash
	SafeCheckpointKnown  bool
}

func (b *BlockChain) ImportEVMFromRPC(opts EVMRPCImportOptions) (*EVMRPCImportResult, error) {
	if b == nil || b.bd == nil || b.bdagChain == nil {
		return nil, fmt.Errorf("blockchain is not initialized")
	}
	if opts.RPCURL == "" {
		return nil, fmt.Errorf("rpc-url is required")
	}
	if opts.CommitEvery == 0 {
		opts.CommitEvery = 1_000
	}
	if opts.ProgressEvery == 0 {
		opts.ProgressEvery = 10_000
	}
	if opts.RPCTimeout == 0 {
		opts.RPCTimeout = 60 * time.Second
	}
	if opts.RPCRetryMax == 0 {
		opts.RPCRetryMax = 8
	}
	if opts.RPCRetryDelay == 0 {
		opts.RPCRetryDelay = 2 * time.Second
	}

	mainTip := b.bd.GetMainChainTip()
	if mainTip == nil || mainTip.GetState() == nil {
		return nil, fmt.Errorf("native main tip is unavailable")
	}
	mainTipOrder := uint64(mainTip.GetOrder())
	targetOrder := opts.TargetOrder
	if targetOrder == 0 {
		targetOrder = mainTipOrder
	}
	if targetOrder > mainTipOrder {
		return nil, fmt.Errorf("target order %d is beyond native main tip order %d", targetOrder, mainTipOrder)
	}

	anchor := b.GetBlockByNumber(opts.AnchorEVMNumber)
	if anchor == nil || anchor.GetState() == nil {
		return nil, fmt.Errorf("native anchor for EVM block %d is unavailable", opts.AnchorEVMNumber)
	}
	if anchor.GetState().GetEVMNumber() != opts.AnchorEVMNumber {
		return nil, fmt.Errorf("native anchor order %d records EVM number %d, want %d", anchor.GetOrder(), anchor.GetState().GetEVMNumber(), opts.AnchorEVMNumber)
	}
	anchorOrder := uint64(anchor.GetOrder())
	if anchorOrder > targetOrder {
		return nil, fmt.Errorf("anchor order %d is beyond target order %d", anchorOrder, targetOrder)
	}

	rpcClient, err := ethclient.Dial(opts.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc %s: %w", opts.RPCURL, err)
	}
	defer rpcClient.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-b.consensus.Interrupt():
			cancel()
		case <-ctx.Done():
		}
	}()

	b.ChainLock()
	defer b.ChainUnlock()
	b.evmChainLock.Lock()
	defer b.evmChainLock.Unlock()

	if err := b.bdagChain.SetHeadToExistingEVMState(anchor.GetState()); err != nil {
		return nil, fmt.Errorf("set EVM RPC import anchor order %d number %d without rewind: %w",
			anchorOrder, opts.AnchorEVMNumber, err)
	}
	cur := b.bdagChain.GetCurHeader()
	if cur == nil || cur.Number == nil {
		return nil, fmt.Errorf("EVM head is unavailable after anchoring")
	}
	if cur.Number.Uint64() != opts.AnchorEVMNumber ||
		cur.Hash() != anchor.GetState().GetEVMHash() ||
		cur.Root != anchor.GetState().GetEVMRoot() {
		return nil, fmt.Errorf("EVM anchor did not land on native anchor order %d: cur number=%d hash=%s root=%s, anchor number=%d hash=%s root=%s",
			anchorOrder,
			cur.Number.Uint64(), cur.Hash().String(), cur.Root.String(),
			opts.AnchorEVMNumber, anchor.GetState().GetEVMHash().String(), anchor.GetState().GetEVMRoot().String())
	}

	result := &EVMRPCImportResult{
		AnchorOrder:  anchorOrder,
		StartOrder:   anchorOrder + 1,
		TargetOrder:  targetOrder,
		MainTipOrder: mainTipOrder,
	}
	started := time.Now()
	lastProgress := started
	for order := result.StartOrder; order <= targetOrder; order++ {
		select {
		case <-b.consensus.Interrupt():
			return result, fmt.Errorf("EVM RPC import interrupted at order %d", order)
		default:
		}

		ib := b.bd.GetBlockByOrder(uint(order))
		if ib == nil {
			return result, fmt.Errorf("missing native block at order %d", order)
		}
		if !ib.IsOrdered() {
			if err := b.updateDefaultBlockState(ib); err != nil {
				return result, err
			}
			result.Processed++
			continue
		}
		blockNode := b.GetBlockNode(ib)
		if blockNode == nil || blockNode.GetBody() == nil {
			return result, fmt.Errorf("missing native block body at order %d hash=%s", order, ib.GetHash().String())
		}

		recState := ib.GetState()
		if recState == nil {
			return result, fmt.Errorf("missing native block state at order %d", order)
		}
		prev := b.bd.GetBlockByOrder(uint(order - 1))
		if prev == nil || prev.GetState() == nil {
			return result, fmt.Errorf("missing native parent state at order %d", order-1)
		}
		prevEVMNum := prev.GetState().GetEVMNumber()
		recEVMNum := recState.GetEVMNumber()

		var headerHash common.Hash
		var headerRoot common.Hash
		var headerNumber uint64
		shouldImport := !recState.GetStatus().KnownInvalid() && recEVMNum > prevEVMNum
		if recState.GetStatus().KnownInvalid() {
			result.SkippedInvalid++
		} else if shouldImport {
			head := b.bdagChain.GetCurHeader()
			if head == nil || head.Number == nil {
				return result, fmt.Errorf("EVM head is unavailable before import at order %d", order)
			}
			if head.Number.Uint64()+1 != recEVMNum {
				return result, fmt.Errorf("EVM import gap at order %d: head=%d recorded=%d prevEVM=%d",
					order, head.Number.Uint64(), recEVMNum, prevEVMNum)
			}
			if head.Number.Uint64() == recEVMNum && head.Hash() == recState.GetEVMHash() {
				result.SkippedExisting++
			} else {
				block, err := fetchCanonicalEVMBlock(ctx, rpcClient, recEVMNum, opts)
				if err != nil {
					return result, fmt.Errorf("fetch canonical EVM block %d at order %d: %w", recEVMNum, order, err)
				}
				if opts.RPCFetchDelay > 0 {
					select {
					case <-ctx.Done():
						return result, fmt.Errorf("EVM RPC import interrupted at order %d", order)
					case <-time.After(opts.RPCFetchDelay):
					}
				}
				if err := b.bdagChain.ImportCanonicalEVMBlock(block); err != nil {
					return result, fmt.Errorf("import canonical EVM block %d at order %d hash=%s: %w",
						recEVMNum, order, ib.GetHash().String(), err)
				}
				result.Imported++
			}
			head = b.bdagChain.GetCurHeader()
			if head == nil || head.Number == nil {
				return result, fmt.Errorf("EVM head is unavailable after import at order %d", order)
			}
			headerNumber = head.Number.Uint64()
			headerHash = head.Hash()
			headerRoot = head.Root
		} else {
			head := b.bdagChain.GetCurHeader()
			if head == nil || head.Number == nil {
				return result, fmt.Errorf("EVM head is unavailable at order %d", order)
			}
			if recEVMNum != head.Number.Uint64() {
				return result, fmt.Errorf("native order %d records EVM %d but head is %d (prevEVM=%d)",
					order, recEVMNum, head.Number.Uint64(), prevEVMNum)
			}
			headerNumber = head.Number.Uint64()
			headerHash = head.Hash()
			headerRoot = head.Root
			result.SkippedNoAdvance++
		}

		if err := b.updateBlockState(ib, blockNode.GetBody()); err != nil {
			return result, fmt.Errorf("update native state order %d hash=%s: %w", order, ib.GetHash().String(), err)
		}
		if !recState.GetStatus().KnownInvalid() {
			if ib.GetState().GetEVMNumber() != headerNumber ||
				ib.GetState().GetEVMHash() != headerHash ||
				ib.GetState().GetEVMRoot() != headerRoot {
				return result, fmt.Errorf("native state mismatch after import order %d: state number=%d hash=%s root=%s, header number=%d hash=%s root=%s",
					order,
					ib.GetState().GetEVMNumber(), ib.GetState().GetEVMHash().String(), ib.GetState().GetEVMRoot().String(),
					headerNumber, headerHash.String(), headerRoot.String())
			}
		}

		result.Processed++
		if result.Processed%opts.CommitEvery == 0 {
			if err := b.commitRecoveryState(); err != nil {
				return result, fmt.Errorf("commit at order %d: %w", order, err)
			}
			result.Commits++
		}
		if result.Processed == 1 || order == targetOrder || result.Processed%opts.ProgressEvery == 0 || time.Since(lastProgress) >= 30*time.Second {
			head := b.bdagChain.GetCurHeader()
			if head != nil && head.Number != nil {
				log.Info("EVM RPC import progress",
					"order", order,
					"targetOrder", targetOrder,
					"processed", result.Processed,
					"imported", result.Imported,
					"skippedExisting", result.SkippedExisting,
					"skippedInvalid", result.SkippedInvalid,
					"skippedNoAdvance", result.SkippedNoAdvance,
					"evmNumber", head.Number.Uint64(),
					"evmHash", head.Hash().String(),
					"elapsed", time.Since(started).Round(time.Second).String())
			}
			lastProgress = time.Now()
		}
	}

	if err := b.commitRecoveryState(); err != nil {
		return result, err
	}
	result.Commits++

	if targetOrder == mainTipOrder {
		if err := b.refreshBestStateForRecoveredTip(); err != nil {
			return result, err
		}
	} else {
		log.Warn("EVM RPC import ended before native main tip; best chain state was not refreshed", "targetOrder", targetOrder, "mainTipOrder", mainTipOrder)
	}

	head := b.bdagChain.GetCurHeader()
	if head == nil || head.Number == nil {
		return result, fmt.Errorf("EVM head is unavailable after import")
	}
	result.FinalEVMNumber = head.Number.Uint64()
	result.FinalEVMHash = head.Hash()
	result.FinalEVMRoot = head.Root
	result.SafeCheckpointNumber, result.SafeCheckpointHash, result.SafeCheckpointRoot, result.SafeCheckpointKnown = b.SafeEVMCheckpoint()
	return result, nil
}

func (b *BlockChain) commitNativeRecoveryState() error {
	if err := b.bd.Commit(); err != nil {
		return err
	}
	flusher, ok := b.DB().(interface{ FlushRecoveryCheckpoint() error })
	if !ok {
		return fmt.Errorf("native database does not support durable recovery checkpoints")
	}
	return flusher.FlushRecoveryCheckpoint()
}

// commitRecoveryState persists EVM first. An interrupted checkpoint may leave
// EVM ahead of native metadata, which is recoverable by selecting an older
// exact native anchor. The reverse order could persist an unusable native tip.
func (b *BlockChain) commitRecoveryState() error {
	if err := b.bdagChain.CommitEVMHeadState(); err != nil {
		return fmt.Errorf("commit EVM recovery state: %w", err)
	}
	if err := b.commitNativeRecoveryState(); err != nil {
		return fmt.Errorf("commit native recovery state: %w", err)
	}
	return nil
}

func fetchCanonicalEVMBlock(ctx context.Context, rpcClient *ethclient.Client, number uint64, opts EVMRPCImportOptions) (*types.Block, error) {
	var lastErr error
	delay := opts.RPCRetryDelay
	for attempt := 1; attempt <= opts.RPCRetryMax; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		fetchCtx, fetchCancel := context.WithTimeout(ctx, opts.RPCTimeout)
		block, err := rpcClient.BlockByNumber(fetchCtx, new(big.Int).SetUint64(number))
		fetchCancel()
		if err == nil {
			if block == nil || block.Header() == nil {
				return nil, fmt.Errorf("canonical EVM block %d is unavailable from rpc", number)
			}
			if block.NumberU64() != number {
				return nil, fmt.Errorf("canonical EVM RPC returned block %d while fetching %d", block.NumberU64(), number)
			}
			return block, nil
		}
		lastErr = err
		if attempt == opts.RPCRetryMax {
			break
		}
		log.Warn("EVM RPC fetch failed; retrying",
			"evmNumber", number,
			"attempt", attempt,
			"maxAttempts", opts.RPCRetryMax,
			"retryIn", delay.String(),
			"err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
	return nil, lastErr
}
