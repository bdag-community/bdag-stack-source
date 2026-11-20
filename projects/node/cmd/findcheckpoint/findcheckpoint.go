package main

import (
	"fmt"
	"os"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
)

func main() {
	// Load configuration and parse command line.  This function also
	// initializes logging and configures it accordingly.
	cfg, _, err := LoadConfig()
	if err != nil {
		log.Error(err.Error())
		return
	}

	defer func() {
		if log.LogWrite() != nil {
			log.LogWrite().Close()
		}
	}()
	qcfg := cfg.ToBDAGConfig()
	// Load the block database.
	chaindb.CreateIfNoExist = false
	db, err := chaindb.Open(qcfg, system.InterruptListener())
	if err != nil {
		log.Error("load block database", "error", err)
		return
	}
	defer func() {
		// Ensure the database is sync'd and closed on shutdown.
		log.Info("Gracefully shutting down the database...")
		db.Close()
	}()
	cons := consensus.NewPure(qcfg, db)
	err = cons.Init()
	if err != nil {
		log.Error(err.Error())
		return
	}

	// find
	bc := cons.BlockChain().(*blockchain.BlockChain)
	if processIsCheckpoint(bc, cfg) {
		return
	}
	// Find checkpoint candidates.
	candidates, err := findCandidates(bc, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to identify candidates:", err)
		return
	}

	// No candidates.
	if len(candidates) == 0 {
		fmt.Println("No candidates found.")
		return
	}

	// Show the candidates.
	for i, checkpoint := range candidates {
		showCandidate(i+1, checkpoint, cfg)
	}
}

func findCandidates(chain *blockchain.BlockChain, cfg *Config) ([]*params.Checkpoint, error) {
	// Start with the latest block of the main chain.
	block := chain.Blockdag().GetMainChainTip()
	if block == nil {
		return nil, fmt.Errorf("Can't find main chain tip")
	}

	// Get the latest known checkpoint.
	latestCheckpoint := chain.LatestCheckpoint()
	if latestCheckpoint == nil {
		// Set the latest checkpoint to the genesis block if there isn't
		// already one.
		latestCheckpoint = &params.Checkpoint{
			Hash:  params.ActiveNetParams.GenesisHash,
			Layer: 0,
		}
	}

	// The latest known block must be at least the last known checkpoint
	// plus required checkpoint confirmations.
	checkpointConfirmations := uint64(blockchain.CheckpointConfirmations)
	requiredLayer := latestCheckpoint.Layer + checkpointConfirmations
	if uint64(block.GetLayer()) < requiredLayer {
		return nil, fmt.Errorf("the block database is only at layer "+
			"%d which is less than the latest checkpoint height "+
			"of %d plus required confirmations of %d",
			block.GetLayer(), latestCheckpoint.Layer,
			checkpointConfirmations)
	}

	// For the first checkpoint, the required layer is any block after the
	// genesis block, so long as the chain has at least the required number
	// of confirmations (which is enforced above).
	if len(params.ActiveNetParams.Checkpoints) == 0 {
		requiredLayer = 1
	}

	// Indeterminate progress setup.
	numBlocksToTest := uint64(block.GetLayer()) - requiredLayer
	progressInterval := (numBlocksToTest / 100) + 1 // min 1
	fmt.Print("Searching for candidates")
	defer fmt.Println()

	// Loop backwards through the chain to find checkpoint candidates.
	candidates := make([]*params.Checkpoint, 0, cfg.NumCandidates)
	numTested := uint64(0)
	preblock := block
	for len(candidates) < cfg.NumCandidates && uint64(block.GetLayer()) > requiredLayer {
		// Display progress.
		if numTested%progressInterval == 0 {
			fmt.Print(".")
		}
		// Determine if this block is a checkpoint candidate.
		isCandidate, err := chain.IsCheckpointCandidate(preblock, block)
		if err != nil {
			return nil, err
		}

		// All checks passed, so this node seems like a reasonable
		// checkpoint candidate.
		if isCandidate {
			checkpoint := params.Checkpoint{
				Layer: uint64(block.GetLayer()),
				Hash:  block.GetHash(),
			}
			candidates = append(candidates, &checkpoint)
		}

		if block.GetMainParent() == blockdag.MaxId {
			break
		}
		preblock = block
		block = chain.Blockdag().GetBlockById(block.GetMainParent())
		if block == nil {
			return nil, fmt.Errorf("Can't find block")
		}
		numTested++
	}
	return candidates, nil
}

func showCandidate(candidateNum int, checkpoint *params.Checkpoint, cfg *Config) {
	if cfg.UseGoOutput {
		fmt.Printf("Candidate %d -- {%d, newShaHashFromStr(\"%v\")},\n",
			candidateNum, checkpoint.Layer, checkpoint.Hash)
		return
	}

	fmt.Printf("Candidate %d -- Layer: %d, Hash: %v\n", candidateNum,
		checkpoint.Layer, checkpoint.Hash)

}

// NewHashFromHex creates a new Hash from a hex string (with or without 0x prefix)
func NewHashFromHex(hexStr string) (*common.Hash, error) {
	// Remove 0x prefix if present
	if len(hexStr) >= 2 && hexStr[0] == '0' && (hexStr[1] == 'x' || hexStr[1] == 'X') {
		hexStr = hexStr[2:]
	}

	// Check length
	if len(hexStr) != common.HashLength*2 {
		return nil, fmt.Errorf("invalid hash hex string length %d, want %d", len(hexStr), common.HashLength*2)
	}

	// Convert from common.Hash
	evmHash := common.HexToHash(hexStr)

	// Convert to our Hash type
	var h common.Hash
	ehb := evmHash.Bytes()
	ReverseBytes(&ehb) // Maintain byte ordering consistency
	copy(h[:], ehb)

	return &h, nil
}
func ReverseBytes(bs *[]byte) *[]byte {
	length := len(*bs)
	for i := 0; i < length/2; i++ {
		index := length - 1 - i
		temp := (*bs)[index]
		(*bs)[index] = (*bs)[i]
		(*bs)[i] = temp
	}
	return bs
}
func processIsCheckpoint(chain *blockchain.BlockChain, cfg *Config) bool {
	if len(cfg.IsCheckPoint) == 0 {
		return false
	}
	blockhash, err := NewHashFromHex(cfg.IsCheckPoint)
	if err != nil {
		log.Error(err.Error())
		return true
	}
	block := chain.GetBlock(blockhash)
	if block == nil {
		log.Error(fmt.Sprintf("%s is not check point", blockhash.String()))
		return true
	}
	checkpoints := chain.Checkpoints()
	if checkpoints != nil {
		for _, checkpoint := range checkpoints {
			if blockhash.Cmp(*checkpoint.Hash) == 0 {
				log.Error(fmt.Sprintf("%s is check point", blockhash.String()))
				return true
			}
		}
	}
	latestCheckpoint := chain.LatestCheckpoint()
	if latestCheckpoint == nil {
		latestCheckpoint = &params.Checkpoint{
			Hash:  params.ActiveNetParams.GenesisHash,
			Layer: 0,
		}
	}

	checkpointConfirmations := uint64(blockchain.CheckpointConfirmations)
	requiredLayer := latestCheckpoint.Layer + checkpointConfirmations
	if len(params.ActiveNetParams.Checkpoints) == 0 {
		requiredLayer = 1
	}
	if uint64(block.GetLayer()) < requiredLayer {
		log.Error(fmt.Sprintf("%s is not check point", blockhash.String()))
		return true
	}

	var preblock blockdag.IBlock
	if block.HasChildren() {
		for k, v := range chain.Blockdag().GetChildren(block).GetMap() {
			if chain.Blockdag().IsOnMainChain(k) {
				preblock = v.(blockdag.IBlock)
				break
			}
		}
	}

	if preblock == nil {
		log.Error(fmt.Sprintf("%s is not check point", blockhash.String()))
		return true
	}
	isCandidate, err := chain.IsCheckpointCandidate(preblock, block)
	if err != nil {
		log.Error(fmt.Sprintf("%s is not check point", blockhash.String()))
		return true
	}

	if isCandidate {
		log.Error(fmt.Sprintf("%s is candidate check point", blockhash.String()))
	}
	return true
}
