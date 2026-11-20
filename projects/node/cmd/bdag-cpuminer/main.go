package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func main() {
	cfg := configFromEnv()
	if _, err := powConfigForNetwork(cfg.network); err != nil {
		log.Fatal(err)
	}
	log.Printf("starting cpu miner id=%s rpc=%s pow=%s", cfg.minerID, cfg.rpcHost, pow.GetPowName(cfg.powType))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	nonceSeed := crc32.ChecksumIEEE([]byte(cfg.minerID))
	for round := uint32(0); ; round++ {
		select {
		case <-stop:
			log.Print("stopping cpu miner")
			return
		default:
		}

		c, err := rpcClient(cfg)
		if err != nil {
			log.Printf("rpc connect failed: %v", err)
			sleepOrStop(stop, cfg.retryDelay)
			continue
		}

		health, err := c.GetTemplateHealth()
		if err != nil {
			log.Printf("template health failed: %v", err)
			c.Disconnect()
			sleepOrStop(stop, cfg.retryDelay)
			continue
		}
		if !health.GetBlockTemplateReady {
			log.Printf("template not ready: %s %s", health.GetBlockTemplateReasonCode, health.GetBlockTemplateReason)
			c.Disconnect()
			sleepOrStop(stop, cfg.retryDelay)
			continue
		}
		if ready, reason := badMinerReady(c, cfg, health); !ready {
			log.Printf("bad miner waiting: %s", reason)
			c.Disconnect()
			sleepOrStop(stop, cfg.retryDelay)
			continue
		}

		tpl, err := requestMiningTemplate(c, byte(cfg.powType), cfg.payAddress(round))
		if err != nil {
			log.Printf("getBlockTemplate failed: %v", err)
			c.Disconnect()
			sleepOrStop(stop, cfg.retryDelay)
			continue
		}

		header, err := headerFromTemplate(tpl, cfg.powType, nonceSeed+round, cfg.network)
		if err != nil {
			log.Printf("template decode failed: %v", err)
			c.Disconnect()
			sleepOrStop(stop, cfg.retryDelay)
			continue
		}
		if cfg.corruptStateRoot {
			original := header.StateRoot
			corruptStateRoot(header)
			log.Printf("bad miner corrupting state root height=%d original=%s corrupt=%s", tpl.Height, original, header.StateRoot)
		}
		start := time.Now()
		solved := false
		for nonce := uint32(0); nonce < cfg.maxNonces; nonce++ {
			header.Pow.SetNonce(nonce)
			if header.Pow.FindSolver(header.BlockData(), header.BlockHash(), header.Difficulty) {
				submitSolvedBlock(c, cfg, tpl, header, nonce)
				solved = true
				break
			}
			if nonce != 0 && nonce%cfg.logEvery == 0 {
				log.Printf("searched %d nonces for height=%d version=0x%08x", nonce, tpl.Height, tpl.Version)
			}
		}
		c.Disconnect()
		if !solved {
			log.Printf("no solution in %d nonces for height=%d elapsed=%s", cfg.maxNonces, tpl.Height, time.Since(start).Round(time.Millisecond))
		}
		sleepOrStop(stop, cfg.pollDelay)
	}
}

type minerConfig struct {
	rpcHost      string
	rpcUser      string
	rpcPass      string
	minerID      string
	network      string
	powType      pow.PowType
	payAddresses []common.Address
	maxNonces    uint32
	logEvery     uint32
	retryDelay   time.Duration
	pollDelay    time.Duration

	corruptStateRoot    bool
	badMinerMinOrder    uint64
	badMinerMinProtocol uint32
}

func configFromEnv() minerConfig {
	network := env("RPC_NETWORK", params.MainNetIsolatedParams.Name)
	payAddresses, err := parseMiningPayAddresses(network, os.Getenv("MINING_PAY_ADDRESSES"))
	if err != nil {
		panic(err)
	}
	corruptStateRoot := envBool("BDAG_TEST_BAD_MINER_CORRUPT_STATE_ROOT", false)
	minProtocol := uint32(0)
	if corruptStateRoot {
		minProtocol = protocol.EVMCheckpointProtocolVersion
	}
	return minerConfig{
		rpcHost:             env("RPC_HOST", "127.0.0.1:8131"),
		rpcUser:             env("RPC_USER", "test"),
		rpcPass:             env("RPC_PASS", "test"),
		minerID:             env("MINER_ID", "cpu-miner"),
		network:             network,
		payAddresses:        payAddresses,
		powType:             pow.SCRYPT,
		maxNonces:           envUint32("MAX_NONCES", 2_000_000),
		logEvery:            envUint32("LOG_EVERY", 250_000),
		retryDelay:          envDuration("RETRY_DELAY", 3*time.Second),
		pollDelay:           envDuration("POLL_DELAY", time.Second),
		corruptStateRoot:    corruptStateRoot,
		badMinerMinOrder:    envUint64("BDAG_TEST_BAD_MINER_MIN_ORDER", 0),
		badMinerMinProtocol: envUint32("BDAG_TEST_BAD_MINER_MIN_PROTOCOL", minProtocol),
	}
}

func rpcClient(cfg minerConfig) (*client.Client, error) {
	return client.New(&client.ConnConfig{
		Host:         cfg.rpcHost,
		User:         cfg.rpcUser,
		Pass:         cfg.rpcPass,
		Params:       cfg.network,
		DisableTLS:   true,
		HTTPPostMode: true,
	}, nil)
}

func headerFromTemplate(tpl *corejson.GetBlockTemplateResult, powType pow.PowType, extraNonce uint32, network string) (*types.BlockHeader, error) {
	powConfig, err := powConfigForNetwork(network)
	if err != nil {
		return nil, err
	}
	nbits := tpl.PowDiffReference.NBits
	if nbits == "" {
		return nil, fmt.Errorf("template missing nbits")
	}
	diff, err := strconv.ParseUint(nbits, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("parse nbits %q: %w", nbits, err)
	}
	header := &types.BlockHeader{
		Version:         tpl.Version,
		ParentRoot:      common.HexToHash(tpl.PreviousHash),
		TxRoot:          common.HexToHash(tpl.TxRoot),
		StateRoot:       common.HexToHash(tpl.StateRoot),
		PowRoot:         common.HexToHash(tpl.PowRoot),
		Difficulty:      uint32(diff),
		Timestamp:       time.Unix(tpl.CurTime, 0),
		BlockHeight:     uint64(tpl.Height),
		CoinbaseAddress: tpl.CoinbaseAddress,
		Reward:          tpl.Reward,
		Pow:             pow.GetInstance(powType, extraNonce, 0, nil),
	}
	header.Pow.SetParams(powConfig)
	header.Pow.SetMainHeight(pow.MainHeight(header.BlockHeight))
	return header, nil
}

func badMinerReady(c *client.Client, cfg minerConfig, health *corejson.MiningTemplateHealthResult) (bool, string) {
	if !cfg.corruptStateRoot {
		return true, ""
	}
	if health == nil {
		return false, "template health unavailable"
	}
	if cfg.badMinerMinOrder > 0 && health.MainOrder < cfg.badMinerMinOrder {
		return false, fmt.Sprintf("main order %d below required %d", health.MainOrder, cfg.badMinerMinOrder)
	}
	if cfg.badMinerMinProtocol > 0 {
		peers, err := c.GetPeerInfo()
		if err != nil {
			return false, fmt.Sprintf("peer info unavailable: %v", err)
		}
		active := 0
		matching := 0
		for _, pe := range peers {
			if !pe.Active || !pe.State {
				continue
			}
			active++
			if pe.Protocol >= cfg.badMinerMinProtocol {
				matching++
			}
		}
		if matching == 0 {
			return false, fmt.Sprintf("no active peers at protocol >= %d (active=%d)", cfg.badMinerMinProtocol, active)
		}
	}
	return true, ""
}

func corruptStateRoot(header *types.BlockHeader) {
	header.StateRoot[0] ^= 0xff
}

func submitSolvedBlock(c *client.Client, cfg minerConfig, tpl *corejson.GetBlockTemplateResult, header *types.BlockHeader, nonce uint32) {
	if !cfg.corruptStateRoot {
		res, err := c.SubmitBlockHeaderV2(header)
		if err != nil {
			log.Printf("submit failed nonce=%d hash=%s err=%v", nonce, header.BlockHash(), err)
			return
		}
		log.Printf("submit nonce=%d hash=%s accepted=%t code=%s message=%q", nonce, header.BlockHash(), res.Accepted, res.Code, res.Message)
		return
	}

	block, err := blockFromTemplate(tpl, header)
	if err != nil {
		log.Printf("build corrupt full block failed nonce=%d hash=%s err=%v", nonce, header.BlockHash(), err)
		return
	}
	blockBytes, err := types.NewBlock(block).Bytes()
	if err != nil {
		log.Printf("serialize corrupt full block failed nonce=%d hash=%s err=%v", nonce, header.BlockHash(), err)
		return
	}
	res, err := submitBlockRPC(cfg, hex.EncodeToString(blockBytes))
	if err != nil {
		log.Printf("submit corrupt full block failed nonce=%d hash=%s err=%v", nonce, header.BlockHash(), err)
		return
	}
	log.Printf("submit corrupt full block nonce=%d hash=%s accepted=%t code=%s message=%q", nonce, header.BlockHash(), res.Accepted, res.Code, res.Message)
}

func blockFromTemplate(tpl *corejson.GetBlockTemplateResult, header *types.BlockHeader) (*types.Block, error) {
	block := &types.Block{Header: *header}
	for _, parent := range tpl.Parents {
		if strings.TrimSpace(parent.Hash) == "" {
			return nil, fmt.Errorf("template parent missing hash")
		}
		parentHash := common.HexToHash(parent.Hash)
		if err := block.AddParent(&parentHash); err != nil {
			return nil, err
		}
	}
	for i, templateTx := range tpl.Transactions {
		raw, err := decodeHex(templateTx.Data)
		if err != nil {
			return nil, fmt.Errorf("decode transaction %d: %w", i, err)
		}
		var tx ethtypes.Transaction
		if err := tx.UnmarshalBinary(raw); err != nil {
			return nil, fmt.Errorf("decode transaction %d rlp: %w", i, err)
		}
		if err := block.AddTransaction(&tx); err != nil {
			return nil, err
		}
	}
	return block, nil
}

type submitBlockRPCResponse struct {
	Result *corejson.SubmitBlockResult `json:"result"`
	Error  *submitBlockRPCError        `json:"error"`
}

type submitBlockRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func submitBlockRPC(cfg minerConfig, hexBlock string) (*corejson.SubmitBlockResult, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "submitBlock",
		"params":  []string{hexBlock},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+cfg.rpcHost, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(cfg.rpcUser, cfg.rpcPass)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var decoded submitBlockRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if decoded.Error != nil {
		return decoded.Result, fmt.Errorf("json-rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if decoded.Result == nil {
		return nil, fmt.Errorf("json-rpc response missing result")
	}
	return decoded.Result, nil
}

func decodeHex(value string) ([]byte, error) {
	hexValue := strings.TrimSpace(value)
	hexValue = strings.TrimPrefix(strings.TrimPrefix(hexValue, "0x"), "0X")
	if len(hexValue)%2 != 0 {
		hexValue = "0" + hexValue
	}
	return hex.DecodeString(hexValue)
}

func sleepOrStop(stop <-chan os.Signal, d time.Duration) {
	select {
	case <-stop:
		os.Exit(0)
	case <-time.After(d):
	}
}

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		log.Printf("invalid %s=%q, using %t", name, v, fallback)
		return fallback
	}
}

func envUint32(name string, fallback uint32) uint32 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		log.Printf("invalid %s=%q, using %d", name, v, fallback)
		return fallback
	}
	return uint32(n)
}

func envUint64(name string, fallback uint64) uint64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		log.Printf("invalid %s=%q, using %d", name, v, fallback)
		return fallback
	}
	return n
}

func envDuration(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("invalid %s=%q, using %s", name, v, fallback)
		return fallback
	}
	return d
}
