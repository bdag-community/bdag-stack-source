package blockchain

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	bevmbdag "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	bevmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func dumpProbeBlockBytes(blockNode *BlockNode) {
	dumpPath := os.Getenv("BDAG_PROBE_BLOCK_DUMP")
	if dumpPath == "" || blockNode == nil || blockNode.GetBody() == nil {
		return
	}
	bytes, err := blockNode.GetBody().Bytes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe dump failed to serialize block: %v\n", err)
		return
	}
	if err := os.WriteFile(dumpPath, bytes, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "probe dump failed to write %s: %v\n", dumpPath, err)
		return
	}
	fmt.Fprintf(os.Stderr, "probe dumped block bytes (%d) to %s\n", len(bytes), dumpPath)
}

func ProbeEVMStateCommitment(b *BlockChain, block *types.SerializedBlock) error {
	if block == nil {
		return fmt.Errorf("probe block is nil")
	}

	flags := BFNoPoWCheck
	if err := b.checkBlockSanity(block, b.timeSource, flags, b.params); err != nil {
		return fmt.Errorf("checkBlockSanity: %w", err)
	}

	newNode := NewBlockNode(block)
	virBlock := b.bd.CreateVirtualBlock(newNode)
	if virBlock == nil {
		return fmt.Errorf("CreateVirtualBlock returned nil")
	}

	mainParent := b.bd.GetBlockById(virBlock.GetMainParent())
	if mainParent == nil {
		return fmt.Errorf("main parent is nil for block %s", block.Hash())
	}
	virBlock.SetOrder(mainParent.GetOrder() + 1)
	virBlock.GetState().(*state.BlockState).SetDefault(mainParent.GetState().(*state.BlockState))
	if err := b.checkBlockContext(block, mainParent, flags); err != nil {
		return fmt.Errorf("checkBlockContext: %w", err)
	}

	native := block.Block()
	header := native.Header
	fmt.Println("=== Native header (peer block) ===")
	fmt.Printf("  hash:           %s\n", block.Hash())
	fmt.Printf("  order:          %d\n", virBlock.GetOrder())
	fmt.Printf("  layer:          %d\n", virBlock.GetLayer())
	fmt.Printf("  version:        %d\n", header.Version)
	fmt.Printf("  parentRoot:     %s\n", header.ParentRoot)
	fmt.Printf("  txRoot:         %s\n", header.TxRoot)
	fmt.Printf("  stateRoot:      %s  (peer commitment)\n", header.StateRoot)
	fmt.Printf("  powRoot:        %s\n", header.PowRoot)
	fmt.Printf("  difficulty:     %d\n", header.Difficulty)
	fmt.Printf("  timestamp:      %s (%d)\n", header.Timestamp.UTC().Format(time.RFC3339), header.Timestamp.Unix())
	fmt.Printf("  blockHeight:    %d\n", header.BlockHeight)
	fmt.Printf("  coinbase:       %s\n", header.CoinbaseAddress)
	fmt.Printf("  reward:         %d\n", header.Reward)
	fmt.Printf("  powType:        %d\n", header.Pow.GetPowType())
	fmt.Printf("  parents:        %d\n", len(native.Parents))
	for i, p := range native.Parents {
		fmt.Printf("    parent[%d]:    %s\n", i, p)
	}
	fmt.Printf("  transactions:   %d\n", len(native.Transactions))
	for i, tx := range native.Transactions {
		fmt.Printf("    tx[%d]:        %s\n", i, tx.Hash())
	}

	fmt.Println("\n=== Main parent anchor ===")
	parentState := mainParent.GetState()
	fmt.Printf("  hash:           %s\n", mainParent.GetHash())
	fmt.Printf("  order:          %d\n", mainParent.GetOrder())
	fmt.Printf("  evmNumber:      %d\n", parentState.GetEVMNumber())
	fmt.Printf("  evmHash:        %s\n", parentState.GetEVMHash())
	fmt.Printf("  evmRoot:        %s\n", parentState.GetEVMRoot())
	if parentState.Root() != nil {
		fmt.Printf("  nativeState:    %s\n", parentState.Root())
	}

	b.ChainLock()
	defer b.ChainUnlock()
	b.evmChainLock.Lock()
	defer b.evmChainLock.Unlock()

	blockCopy := cloneBlockForEVMStateCommitment(newNode.GetBody().Block())
	if err := b.bdagCheckConnectBlock(newNode, uint64(virBlock.GetLayer())); err != nil {
		fmt.Printf("\n=== buildBlock dry-run FAILED ===\n  err: %v\n", err)
	} else {
		evmBlock := newNode.GetbdagBlock()
		if evmBlock != nil && evmBlock.EvmBlock != nil {
			evmHeader := evmBlock.EvmBlock.Header()
			fmt.Println("\n=== EVM template from buildBlock ===")
			fmt.Printf("  number:         %d\n", evmHeader.Number.Uint64())
			fmt.Printf("  hash:           %s\n", evmHeader.Hash())
			fmt.Printf("  stateRoot:      %s\n", evmHeader.Root)
			fmt.Printf("  receiptRoot:    %s\n", evmHeader.ReceiptHash)
			fmt.Printf("  txRoot:         %s\n", evmHeader.TxHash)
			fmt.Printf("  time:           %d (%s)\n", evmHeader.Time, time.Unix(int64(evmHeader.Time), 0).UTC().Format(time.RFC3339))
			fmt.Printf("  coinbase:       %s\n", evmHeader.Coinbase)
			fmt.Printf("  gasLimit:       %d\n", evmHeader.GasLimit)
			fmt.Printf("  baseFee:        %v\n", evmHeader.BaseFee)
			fmt.Printf("  extra:          %x\n", evmHeader.Extra)
			fmt.Printf("  transactions:   %d\n", len(evmBlock.EvmBlock.Transactions()))

			fmt.Println("\n=== Reward/staking breakdown (pre-mutation reads at anchor root) ===")
			if anchorState, err := b.EVMChain().ETHChain().Ether().BlockChain().StateAt(parentState.GetEVMRoot()); err != nil {
				fmt.Printf("  failed to open anchor state: %v\n", err)
			} else {
				bd := bdagconsensus.ComputeRewardBreakdown(evmHeader, anchorState)
				fmt.Printf("  epoch:                      %d (activation=%d, legacyPath=%v)\n", bd.Epoch, bd.ActivationEpoch, bd.LegacyPath)
				fmt.Printf("  totalSubsidy:               %v\n", bd.TotalSubsidy)
				if !bd.LegacyPath {
					fmt.Printf("  minerSplitPct:              %v\n", bd.MinerSplitPct)
					fmt.Printf("  liquidReward:               %v\n", bd.LiquidReward)
					fmt.Printf("  stakersShare:               %v\n", bd.StakersShare)
					fmt.Printf("  autoStakeReward:            %v\n", bd.AutoStakeReward)
					fmt.Printf("  distributableStakerReward:  %v\n", bd.DistributableStakerReward)
					fmt.Printf("  poolTotalAssets:            %v\n", bd.PoolTotalAssets)
					fmt.Printf("  poolTotalShares:            %v\n", bd.PoolTotalShares)
				}
			}
		}
	}

	wantRoot, err := b.calculateAnchoredEVMStateCommitment(blockCopy, mainParent, uint64(virBlock.GetLayer()), mainParent.GetState())
	if err != nil {
		return fmt.Errorf("calculateAnchoredEVMStateCommitment: %w", err)
	}

	evmBlock := newNode.GetbdagBlock()
	var evmHeader *gethtypes.Header
	if evmBlock != nil {
		evmHeader = evmBlock.EvmBlock.Header()
	}

	peerCommitment := header.StateRoot
	fmt.Println("\n=== Commitment summary ===")
	fmt.Printf("  peer header StateRoot (got):  %s\n", peerCommitment)
	fmt.Printf("  local buildBlock (want):      %s\n", wantRoot)
	if peerCommitment == *wantRoot {
		fmt.Println("  MATCH")
	} else {
		fmt.Println("  MISMATCH")
	}

	if evmHeader != nil {
		printCommitmentFields(blockCopy, mainParent, evmHeader)
	}

	fmt.Println("\n=== Timestamp resolution hints ===")
	parentEVMTime := uint64(0)
	if client := b.EVMChain().GetEthClient(); client != nil {
		if parentHeader, err := client.HeaderByNumber(context.Background(), new(big.Int).SetUint64(parentState.GetEVMNumber())); err == nil && parentHeader != nil {
			parentEVMTime = parentHeader.Time
			fmt.Printf("  anchor EVM header time:       %d\n", parentEVMTime)
		}
	}
	fmt.Printf("  native timestamp (requested): %d\n", header.Timestamp.Unix())
	resolved := bevmbdag.ResolveEVMBlockTimestamp(parentEVMTime, header.Timestamp.Unix(), bevmparams.BdagMainnetChainConfig.ChainID)
	fmt.Printf("  ResolveEVMBlockTimestamp:     %d\n", resolved)
	if evmHeader != nil {
		fmt.Printf("  buildBlock EVM time:          %d\n", evmHeader.Time)
	}
	fmt.Printf("  parent EVM number (anchor):   %d\n", parentState.GetEVMNumber())

	return nil
}

func printCommitmentFields(block *types.Block, mainParent blockdag.IBlock, evmHeader *gethtypes.Header) {
	parents := make([]ethcommon.Hash, 0, len(block.Parents))
	for _, parent := range block.Parents {
		parents = append(parents, *parent)
	}
	txs := make([]ethcommon.Hash, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		txs = append(txs, tx.Hash())
	}
	data := evmStateCommitmentData{
		Domain:              []byte("BDAG:EVMTEMPLATE:v1"),
		Version:             block.Header.Version,
		ParentRoot:          block.Header.ParentRoot,
		TxRoot:              block.Header.TxRoot,
		Difficulty:          block.Header.Difficulty,
		Timestamp:           uint64(block.Header.Timestamp.Unix()),
		BlockHeight:         block.Header.BlockHeight,
		CoinbaseAddress:     block.Header.CoinbaseAddress,
		Reward:              block.Header.Reward,
		PowType:             uint8(block.Header.Pow.GetPowType()),
		Parents:             parents,
		Transactions:        txs,
		MainParentHash:      *mainParent.GetHash(),
		MainParentState:     *mainParent.GetState().Root(),
		AnchorEVMNumber:     mainParent.GetState().GetEVMNumber(),
		AnchorEVMHash:       mainParent.GetState().GetEVMHash(),
		AnchorEVMRoot:       mainParent.GetState().GetEVMRoot(),
		TemplateEVMNumber:   evmHeader.Number.Uint64(),
		TemplateEVMHash:     evmHeader.Hash(),
		TemplateEVMRoot:     evmHeader.Root,
		TemplateReceiptRoot: evmHeader.ReceiptHash,
	}

	fmt.Println("\n=== Commitment RLP field inputs (local buildBlock) ===")
	fmt.Printf("  domain:              %s\n", string(data.Domain))
	fmt.Printf("  version:             %d\n", data.Version)
	fmt.Printf("  parentRoot:          %s\n", data.ParentRoot)
	fmt.Printf("  txRoot:              %s\n", data.TxRoot)
	fmt.Printf("  difficulty:          %d\n", data.Difficulty)
	fmt.Printf("  timestamp:           %d\n", data.Timestamp)
	fmt.Printf("  blockHeight:         %d\n", data.BlockHeight)
	fmt.Printf("  coinbaseAddress:     %s\n", data.CoinbaseAddress)
	fmt.Printf("  reward:              %d\n", data.Reward)
	fmt.Printf("  powType:             %d\n", data.PowType)
	fmt.Printf("  parents[%d]:         %s\n", len(data.Parents), hashListPreview(data.Parents))
	fmt.Printf("  transactions[%d]:    %s\n", len(data.Transactions), hashListPreview(data.Transactions))
	fmt.Printf("  mainParentHash:      %s\n", data.MainParentHash)
	fmt.Printf("  mainParentState:     %s\n", data.MainParentState)
	fmt.Printf("  anchorEVMNumber:     %d\n", data.AnchorEVMNumber)
	fmt.Printf("  anchorEVMHash:       %s\n", data.AnchorEVMHash)
	fmt.Printf("  anchorEVMRoot:       %s\n", data.AnchorEVMRoot)
	fmt.Printf("  templateEVMNumber:   %d\n", data.TemplateEVMNumber)
	fmt.Printf("  templateEVMHash:     %s\n", data.TemplateEVMHash)
	fmt.Printf("  templateEVMRoot:     %s\n", data.TemplateEVMRoot)
	fmt.Printf("  templateReceiptRoot: %s\n", data.TemplateReceiptRoot)
}

func hashListPreview(hashes []ethcommon.Hash) string {
	if len(hashes) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(hashes))
	for _, h := range hashes {
		parts = append(parts, h.Hex())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func LoadProbeBlockFromHex(hexStr string) (*types.SerializedBlock, error) {
	hexStr = strings.TrimPrefix(strings.TrimPrefix(hexStr, "0x"), "0X")
	hexStr = strings.ReplaceAll(hexStr, "\n", "")
	hexStr = strings.ReplaceAll(hexStr, " ", "")
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("decode block hex: %w", err)
	}
	return types.NewBlockFromBytes(raw)
}

func LoadProbeBlockFromFile(path string) (*types.SerializedBlock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return types.NewBlockFromBytes(raw)
}
