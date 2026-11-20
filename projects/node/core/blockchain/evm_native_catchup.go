package blockchain

import (
	"errors"
	"fmt"
	"time"
)

const (
	// evmNativeCatchupBatchOrdersDefault is used when config.EVMNativeCatchupBatchOrders
	// isn't set (see BlockChain.evmNativeCatchupBatchOrders in New()).
	evmNativeCatchupBatchOrdersDefault = uint64(512)
	evmNativeCatchupInterval           = 2 * time.Second
)

func (b *BlockChain) signalEVMNativeCatchup() {
	if b == nil || b.evmCatchupWake == nil {
		return
	}
	select {
	case b.evmCatchupWake <- struct{}{}:
	default:
	}
}

func (b *BlockChain) evmNativeCatchupLoop() {
	defer b.wg.Done()

	ticker := time.NewTicker(evmNativeCatchupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.quit:
			return
		case <-b.evmCatchupWake:
		case <-ticker.C:
		}

		if err := b.runEVMNativeCatchupBatch(); err != nil {
			if isEVMNativeCatchupRetryable(err) {
				log.Debug("EVM native catch-up deferred", "err", err)
				continue
			}
			log.Warn("EVM native catch-up failed", "err", err)
		}
	}
}

func isEVMNativeCatchupRetryable(err error) bool {
	return errors.Is(err, errEVMNativeRebuildBusy) ||
		errors.Is(err, errEVMNativeRebuildStaleAnchor) ||
		errors.Is(err, errEVMNativeCatchupUncommitted)
}

func (b *BlockChain) runEVMNativeCatchupBatch() error {
	if b == nil || b.bd == nil || b.bdagChain == nil {
		return nil
	}
	cur := b.bdagChain.GetCurHeader()
	if cur == nil || cur.Number == nil {
		return nil
	}
	currentNumber := cur.Number.Uint64()
	currentHash := cur.Hash()

	b.ChainRLock()
	mainTip := b.bd.GetMainChainTip()
	if mainTip == nil || mainTip.GetState() == nil {
		b.ChainRUnlock()
		return nil
	}
	mainTipOrder := uint64(mainTip.GetOrder())
	mainTipEVM := mainTip.GetState().GetEVMNumber()
	b.ChainRUnlock()

	if currentNumber >= mainTipEVM {
		return nil
	}
	anchor := b.GetBlockByNumber(currentNumber)
	if anchor == nil || anchor.GetState() == nil {
		return fmt.Errorf("native anchor for current EVM head is unavailable: evmNumber=%d evmHash=%s mainTipOrder=%d mainTipEVM=%d",
			currentNumber, currentHash.String(), mainTipOrder, mainTipEVM)
	}
	if anchor.GetState().GetEVMHash() != currentHash {
		return fmt.Errorf("native anchor for current EVM head has different hash: evmNumber=%d currentHash=%s nativeHash=%s nativeOrder=%d mainTipOrder=%d mainTipEVM=%d",
			currentNumber, currentHash.String(), anchor.GetState().GetEVMHash().String(), anchor.GetOrder(), mainTipOrder, mainTipEVM)
	}
	anchorOrder := uint64(anchor.GetOrder())
	if anchorOrder >= mainTipOrder {
		return nil
	}

	batchOrders := b.evmNativeCatchupBatchOrders
	if batchOrders == 0 {
		batchOrders = evmNativeCatchupBatchOrdersDefault
	}
	targetOrder := anchorOrder + batchOrders
	if targetOrder > mainTipOrder {
		targetOrder = mainTipOrder
	}
	if targetOrder <= anchorOrder {
		return nil
	}
	if !b.requiresEVMNativeStateCommitment(anchorOrder + 1) {
		return fmt.Errorf("%w: anchorOrder=%d nextOrder=%d targetOrder=%d mainTipOrder=%d",
			errEVMNativeCatchupUncommitted,
			anchorOrder,
			anchorOrder+1,
			targetOrder,
			mainTipOrder)
	}

	log.Info("EVM native catch-up starting",
		"anchorOrder", anchorOrder,
		"targetOrder", targetOrder,
		"mainTipOrder", mainTipOrder,
		"currentEVM", currentNumber,
		"targetEVM", mainTipEVM,
		"evmGap", mainTipEVM-currentNumber)
	result, err := b.RebuildEVMNativeState(EVMNativeRebuildOptions{
		AnchorEVMNumber:        currentNumber,
		TargetOrder:            targetOrder,
		CommitEvery:            batchOrders,
		ProgressEvery:          batchOrders,
		RequireCurrentAnchor:   true,
		RequireStateCommitment: true,
		SkipIfEVMBusy:          true,
	})
	if err != nil {
		if isEVMNativeCatchupRetryable(err) {
			return nil
		}
		return err
	}
	if result == nil || result.Processed == 0 {
		return nil
	}
	log.Info("EVM native catch-up batch complete",
		"anchorOrder", result.AnchorOrder,
		"targetOrder", result.TargetOrder,
		"mainTipOrder", result.MainTipOrder,
		"processed", result.Processed,
		"replayed", result.Replayed,
		"skippedInvalid", result.SkippedInvalid,
		"skippedNoAdvance", result.SkippedNoAdvance,
		"finalEVM", result.FinalEVMNumber,
		"finalEVMHash", result.FinalEVMHash.String())
	return nil
}
