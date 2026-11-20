package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/dbnamespace"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/ethereum/go-ethereum/common"
	"github.com/schollz/progressbar/v3"
	"github.com/urfave/cli/v2"
)

func blockchainCmd() *cli.Command {
	var (
		outputPath string
		endPoint   string
		byID       bool
		inputPath  string
	)
	return &cli.Command{
		Name:        "blockchain",
		Aliases:     []string{"b"},
		Category:    "BlockChain",
		Usage:       "Block Chain",
		Description: "Block Chain",
		Subcommands: []*cli.Command{
			&cli.Command{
				Name:        "export",
				Aliases:     []string{"dump"},
				Usage:       "Write blockchain as a flat file of blocks for use with 'blockchain import', to the specified filename",
				Description: "Export all blocks from database",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Path to output data",
						Destination: &outputPath,
					},
					&cli.StringFlag{
						Name:        "endpoint",
						Aliases:     []string{"e"},
						Usage:       "End point for output data",
						Destination: &endPoint,
					},
					&cli.BoolFlag{
						Name:        "byid",
						Aliases:     []string{"i"},
						Usage:       "Export by block id",
						Destination: &byID,
					},
				},
				Action: func(ctx *cli.Context) error {
					cfg := config.Cfg
					defer func() {
						if log.LogWrite() != nil {
							log.LogWrite().Close()
						}
					}()
					interrupt := system.InterruptListener()
					log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version())
					log.Info("System info", "Home dir", cfg.HomeDir)
					if cfg.NoFileLogging {
						log.Info("File logging disabled")
					}
					db, err := chaindb.Open(cfg, interrupt)
					if err != nil {
						return err
					}
					defer db.Close()
					//
					cfg.InvalidTxIndex = false
					cfg.AddrIndex = false
					cons := consensus.New(cfg, db, interrupt, make(chan struct{}))
					err = cons.Init()
					if err != nil {
						log.Error(err.Error())
						return err
					}
					err = cons.BlockChain().Start()
					if err != nil {
						return err
					}
					defer func() {
						err = cons.BlockChain().Stop()
						if err != nil {
							log.Error(err.Error())
						}
					}()
					if len(outputPath) <= 0 {
						outputPath = cfg.HomeDir
					}
					return exportBlockChain(cons, outputPath, endPoint, byID)
				},
			},
			&cli.Command{
				Name:        "import",
				Aliases:     []string{"i"},
				Usage:       "Import all blocks from database",
				Description: "Import all blocks from database",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Path to input data",
						Destination: &inputPath,
					},
				},
				Action: func(ctx *cli.Context) error {
					cfg := config.Cfg
					defer func() {
						if log.LogWrite() != nil {
							log.LogWrite().Close()
						}
					}()
					interrupt := system.InterruptListener()
					log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version())
					log.Info("System info", "Home dir", cfg.HomeDir)
					if cfg.NoFileLogging {
						log.Info("File logging disabled")
					}
					db, err := chaindb.Open(cfg, interrupt)
					if err != nil {
						return err
					}
					defer db.Close()
					//
					cfg.InvalidTxIndex = false
					cfg.AddrIndex = false
					cons := consensus.New(cfg, db, interrupt, make(chan struct{}))
					err = cons.Init()
					if err != nil {
						log.Error(err.Error())
						return err
					}
					err = cons.BlockChain().Start()
					if err != nil {
						return err
					}
					defer func() {
						err = cons.BlockChain().Stop()
						if err != nil {
							log.Error(err.Error())
						}
					}()
					if len(inputPath) <= 0 {
						inputPath = cfg.HomeDir
					}
					return importBlockChain(cons, inputPath)
				},
			},
		},
	}
}

func exportBlockChain(consensus model.Consensus, outputPath string, end string, byID bool) error {
	bc := consensus.BlockChain().(*blockchain.BlockChain)
	mainTip := bc.Blockdag().GetMainChainTip()
	if mainTip.GetOrder() <= 0 {
		return fmt.Errorf("No blocks in database")
	}
	outFilePath, err := GetIBDFilePath(outputPath)
	if err != nil {
		return err
	}

	outFile, err := os.OpenFile(outFilePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, os.ModePerm)
	if err != nil {
		return err
	}
	defer func() {
		if err := outFile.Close(); err != nil {
			log.Error(fmt.Sprintf("Failed to close file %s: %v", outFilePath, err))
		}
	}()

	var endPoint blockdag.IBlock
	endNum := uint(0)
	if byID {
		endNum = mainTip.GetID()
	} else {
		endNum = mainTip.GetOrder()
	}

	if len(end) > 0 {
		ephash, err := NewHashFromHex(end)
		if err != nil {
			return err
		}
		endPoint = bc.GetBlock(ephash)
		if endPoint != nil {
			if byID {
				if endNum > endPoint.GetID() {
					endNum = endPoint.GetID()
				}
			} else {
				if endNum > endPoint.GetOrder() {
					endNum = endPoint.GetOrder()
				}
			}

			log.Info(fmt.Sprintf("End point:%s order:%d id:%d", ephash.String(), endPoint.GetOrder(), endPoint.GetID()))
		} else {
			return fmt.Errorf("End point is error")
		}

	}
	logLvl := log.Glogger().GetVerbosity()
	bar := progressbar.Default(int64(endNum-1), "Export:")
	log.Glogger().Verbosity(log.LvlCrit)

	bhs := []*common.Hash{}
	var i uint
	for i = uint(1); i <= endNum; i++ {
		bar.Add(1)
		var blockHash *common.Hash
		if byID {
			ib := bc.Blockdag().GetBlockById(i)
			if ib != nil {
				blockHash = ib.GetHash()
			} else {
				blockHash = nil
			}
		} else {
			blockHash = bc.Blockdag().GetBlockHashByOrder(i)
		}

		if blockHash == nil {
			if byID {
				log.Trace(fmt.Sprintf("Skip block: Can't find block (%d)!", i))
				continue
			} else {
				return fmt.Errorf("Can't find block (%d)!", i)
			}
		}
		bhs = append(bhs, blockHash)
	}
	bar.Finish()
	bar.ChangeMax(len(bhs))
	bar.Set(0)
	var maxNum [4]byte
	dbnamespace.ByteOrder.PutUint32(maxNum[:], uint32(len(bhs)))
	_, err = outFile.Write(maxNum[:])
	if err != nil {
		return err
	}
	for _, blockHash := range bhs {
		bar.Add(1)
		block, err := bc.FetchBlockByHash(blockHash)
		if err != nil {
			return err
		}
		bytes, err := block.Bytes()
		if err != nil {
			return err
		}
		ibdb := &IBDBlock{length: uint32(len(bytes)), bytes: bytes}
		err = ibdb.Encode(outFile)
		if err != nil {
			return err
		}
		if bar != nil {
			bar.Add(1)
		}

		/*if endPoint != nil {
			if endPoint.GetHash().IsEqual(blockHash) {
				break
			}
		}*/
	}
	fmt.Println()
	log.Glogger().Verbosity(logLvl)

	log.Info(fmt.Sprintf("Finish export: blocks(%d)    ------>File:%s", len(bhs), outFilePath))
	return nil
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
func importBlockChain(consensus model.Consensus, inputPath string) error {
	bc := consensus.BlockChain().(*blockchain.BlockChain)
	mainTip := bc.Blockdag().GetMainChainTip()
	if mainTip.GetOrder() > 0 {
		return fmt.Errorf("Your database is not empty, please empty the database.")
	}
	inputFilePath, err := GetIBDFilePath(inputPath)
	if err != nil {
		return err
	}
	blocksBytes, err := util.ReadFile(inputFilePath)
	if err != nil {
		return err
	}
	offset := 0
	maxOrder := dbnamespace.ByteOrder.Uint32(blocksBytes[offset : offset+4])
	offset += 4

	logLvl := log.Glogger().GetVerbosity()
	bar := progressbar.Default(int64(maxOrder-1), "Import:")
	log.Glogger().Verbosity(log.LvlCrit)

	for i := uint32(1); i <= maxOrder; i++ {
		ibdb := &IBDBlock{}
		err := ibdb.Decode(blocksBytes[offset:])
		if err != nil {
			return err
		}
		offset += 4 + int(ibdb.length)

		err = bc.CheckBlockSanity(ibdb.blk, bc.TimeSource(), blockchain.BFFastAdd, params.ActiveNetParams.Params)
		if err != nil {
			return err
		}
		_, err = bc.FastAcceptBlock(ibdb.blk, blockchain.BFFastAdd)
		if err != nil {
			return err
		}
		if bar != nil {
			bar.Add(1)
		}
	}
	fmt.Println()
	log.Glogger().Verbosity(logLvl)

	mainTip = bc.Blockdag().GetMainChainTip()
	log.Info(fmt.Sprintf("Finish import: blocks(%d)    ------>File:%s", mainTip.GetOrder(), inputFilePath))
	log.Info(fmt.Sprintf("New Info:%s  mainOrder=%d tips=%d", mainTip.GetHash().String(), mainTip.GetOrder(), bc.Blockdag().GetTips().Size()))
	return nil
}

func GetIBDFilePath(path string) (string, error) {
	if len(path) <= 0 {
		return "", fmt.Errorf("Path error")
	}
	if len(path) >= 4 {
		if path[len(path)-4:] == ".ibd" {
			return path, nil
		}
	}
	const defaultFileName = "blocks.ibd"
	return strings.TrimRight(strings.TrimRight(path, "/"), "\\") + "/" + defaultFileName, nil
}

type IBDBlock struct {
	length uint32
	bytes  []byte
	blk    *types.SerializedBlock
}

func (b *IBDBlock) Encode(w io.Writer) error {
	var serializedLen [4]byte
	dbnamespace.ByteOrder.PutUint32(serializedLen[:], b.length)
	_, err := w.Write(serializedLen[:])
	if err != nil {
		return err
	}
	_, err = w.Write(b.bytes)
	return err
}

func (b *IBDBlock) Decode(bytes []byte) error {
	if len(bytes) < 4 {
		return io.ErrUnexpectedEOF
	}
	b.length = dbnamespace.ByteOrder.Uint32(bytes[:4])
	// Bound the body by the supplied slice length before converting or adding
	// its uint32 prefix. Capacity may include bytes outside the input record.
	if uint64(b.length) > uint64(len(bytes)-4) {
		return io.ErrUnexpectedEOF
	}

	block, err := types.NewBlockFromBytes(bytes[4 : 4+int(b.length)])
	if err != nil {
		return err
	}
	b.blk = block
	return nil
}
