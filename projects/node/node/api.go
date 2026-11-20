// Copyright (c) 2013-2016 The btcsuite developers
// Copyright (c) 2017-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.
package node

import (
	"encoding/hex"
	js "encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	bdag "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/bevm/eth"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/marshal"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/rpc/api"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/version"
)

func (nf *BlockdagFull) apis() []api.API {
	return []api.API{
		{
			NameSpace: cmds.DefaultServiceNameSpace,
			Service:   NewPublicBlockChainAPI(nf),
			Public:    true,
		},
		{
			NameSpace: cmds.TestNameSpace,
			Service:   NewPrivateBlockChainAPI(nf),
			Public:    false,
		},
		{
			NameSpace: cmds.LogNameSpace,
			Service:   NewPrivateLogAPI(nf),
			Public:    false,
		},
	}
}

type PublicBlockChainAPI struct {
	node *BlockdagFull
}

// ComponentVersion is replaced for component packages with
// -X github.com/BlockdagNetworkLabs/bdag/node.ComponentVersion=<semver>.
// Source builds remain truthful instead of pretending to be published.
var ComponentVersion = "development"

func NewPublicBlockChainAPI(node *BlockdagFull) *PublicBlockChainAPI {
	return &PublicBlockChainAPI{node}
}

// GetCapabilities advertises the public, versioned component interfaces before
// a caller depends on them. It deliberately has no chain-state dependency, so
// upgrade tooling can probe a newly started node while it is still syncing.
func (api *PublicBlockChainAPI) GetCapabilities() (any, error) {
	return &json.ComponentCapabilitiesResult{
		Schema:           json.ComponentCapabilitiesSchema,
		Component:        json.CorechainComponent,
		ComponentVersion: ComponentVersion,
		SourceCommit:     version.SourceCommit,
		APIs: map[string]json.ComponentAPICapability{
			json.CorechainNativeAPI: {
				Current:         json.CorechainNativeAPIVersion,
				SupportedMajors: []int{1},
				Transports:      []string{"json-rpc-2.0/http", "json-rpc-2.0/websocket"},
				Contract:        json.CorechainContractPath,
			},
			json.CorechainEVMAPI: {
				Current:         json.CorechainEVMAPIVersion,
				SupportedMajors: []int{1},
				Transports:      []string{"ethereum-json-rpc-2.0/http", "ethereum-json-rpc-2.0/websocket"},
				Contract:        json.CorechainContractPath,
			},
		},
	}, nil
}

// GetChainIdentity returns only the immutable identities of the active native
// and embedded EVM chains. It deliberately does not inspect the blockchain,
// miner, template, peer, or synchronization state, so callers can use it
// while a node is still starting or syncing.
func (api *PublicBlockChainAPI) GetChainIdentity() (any, error) {
	active := params.ActiveNetParams
	if active == nil || active.Params == nil {
		return nil, fmt.Errorf("chain identity: active network parameters are unavailable")
	}

	// Names and protocol magic are a single authority. Mainnet-isolated keeps
	// its distinct operational name but intentionally shares mainnet identity.
	var expectedNetwork string
	var expectedNet protocol.Network
	var expectedChainID string
	switch active.Name {
	case "mainnet":
		expectedNetwork, expectedNet, expectedChainID = "mainnet", protocol.MainNet, "1404"
	case "mainnet-isolated":
		expectedNetwork, expectedNet, expectedChainID = "mainnet-isolated", protocol.MainNet, "1404"
	case "testnet":
		expectedNetwork, expectedNet, expectedChainID = "testnet", protocol.TestNet, "1043"
	case "privnet":
		expectedNetwork, expectedNet, expectedChainID = "privnet", protocol.PrivNet, "1048"
	case "mixnet":
		expectedNetwork, expectedNet, expectedChainID = "mixnet", protocol.MixNet, "1046"
	case "devnet":
		expectedNetwork, expectedNet, expectedChainID = "devnet", protocol.DevNet, "8008"
	default:
		return nil, fmt.Errorf("chain identity: unsupported active network %q", active.Name)
	}
	if active.Net != expectedNet {
		return nil, fmt.Errorf("chain identity: network %q has magic 0x%08x, want 0x%08x", active.Name, uint32(active.Net), uint32(expectedNet))
	}
	if active.GenesisHash == nil || active.GenesisBlock == nil {
		return nil, fmt.Errorf("chain identity: native genesis authority is unavailable")
	}
	if nativeBlockHash := active.GenesisBlock.Hash(); nativeBlockHash == nil || *nativeBlockHash != *active.GenesisHash {
		return nil, fmt.Errorf("chain identity: native genesis authorities disagree")
	}

	evmConfig := bdag.ChainConfig()
	if evmConfig == nil || evmConfig.ChainID == nil || evmConfig.ChainID.Sign() < 0 {
		return nil, fmt.Errorf("chain identity: EVM chain configuration is unavailable")
	}
	if evmConfig.ChainID.String() != expectedChainID {
		return nil, fmt.Errorf("chain identity: EVM chain ID %q is inconsistent with network %q", evmConfig.ChainID.String(), expectedNetwork)
	}
	evmGenesis := bdag.CurrentGenesis()
	if evmGenesis == nil || evmGenesis.Config == nil || evmGenesis.Config.ChainID == nil || evmGenesis.Config.ChainID.Cmp(evmConfig.ChainID) != 0 {
		return nil, fmt.Errorf("chain identity: EVM genesis authority is inconsistent")
	}
	evmGenesisBlock := evmGenesis.ToBlock()
	if evmGenesisBlock == nil {
		return nil, fmt.Errorf("chain identity: EVM genesis block is unavailable")
	}

	nativeMagic := fmt.Sprintf("0x%08x", uint32(active.Net))
	nativeGenesisHash := active.GenesisHash.String()
	evmChainID := evmConfig.ChainID.String()
	evmGenesisHash := evmGenesisBlock.Hash().String()
	if !validChainIdentityHash(nativeGenesisHash) || !validChainIdentityHash(evmGenesisHash) || !validChainIdentityMagic(nativeMagic) || evmChainID == "" {
		return nil, fmt.Errorf("chain identity: generated identity has invalid encoding")
	}

	return &json.ChainIdentityResult{
		Schema:             json.ChainIdentitySchema,
		Network:            expectedNetwork,
		NativeNetworkMagic: nativeMagic,
		NativeGenesisHash:  nativeGenesisHash,
		EVMChainID:         evmChainID,
		EVMGenesisHash:     evmGenesisHash,
	}, nil
}

func validChainIdentityMagic(value string) bool {
	if len(value) != 10 || value[:2] != "0x" || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func validChainIdentityHash(value string) bool {
	if len(value) != 66 || value[:2] != "0x" || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

// Return the node info
func (api *PublicBlockChainAPI) GetNodeInfo() (any, error) {
	bc := api.node.GetBlockChain()
	best, target, powType, err := nodeInfoSnapshot(bc)
	if err != nil {
		return nil, err
	}
	difficulty, err := nodeInfoDifficulty(target, powType, api.node.node.Params)
	if err != nil {
		return nil, err
	}
	ret := &json.InfoNodeResult{
		ID:               api.node.GetPeerServer().PeerID().String(),
		Version:          int32(1000000*version.Major + 10000*version.Minor + 100*version.Patch),
		BuildVersion:     version.String(),
		ProtocolVersion:  int32(protocol.ProtocolVersion),
		TotalSubsidy:     best.TotalSubsidy,
		TimeOffset:       int64(api.node.GetBlockChain().TimeSource().Offset().Seconds()),
		Connections:      int32(len(api.node.GetPeerServer().Peers().Active())),
		PowDiff:          difficulty,
		Network:          params.ActiveNetParams.Name,
		Confirmations:    blockdag.StableConfirmations,
		CoinbaseMaturity: int32(api.node.node.Params.CoinbaseMaturity),
		Modules:          []string{cmds.DefaultServiceNameSpace, cmds.MinerNameSpace, cmds.TestNameSpace, cmds.LogNameSpace, cmds.P2PNameSpace},
	}
	ret.GraphState = marshal.GetGraphStateResult(best.GraphState)
	ret.StateRoot = best.StateRoot.String()
	hostdns := api.node.GetPeerServer().HostDNS()
	if hostdns != nil {
		ret.DNS = hostdns.String()
	}
	if api.node.GetPeerServer().Node() != nil {
		ret.QNR = api.node.GetPeerServer().Node().String()
	}
	if len(api.node.GetPeerServer().HostAddress()) > 0 {
		ret.Addresss = api.node.GetPeerServer().HostAddress()
	}

	// soft forks
	ret.ConsensusDeployment = make(map[string]*json.ConsensusDeploymentDesc)
	ret.ConsensusDeployment["token"] = &json.ConsensusDeploymentDesc{Status: "active"}
	ret.ConsensusDeployment["bevm"] = &json.ConsensusDeploymentDesc{Status: "active"}

	return ret, nil
}

// GetArchiveStatus reports whether the node is running in archive mode for the
// embedded EVM environment.
func (api *PublicBlockChainAPI) GetArchiveStatus() (bool, error) {
	return api.node.node.Config.IsArchiveNode, nil
}

// getDifficultyRatio returns the proof-of-work difficulty as a multiple of the
// minimum difficulty using the passed bits field from the header of a block.
func getDifficultyRatio(target *big.Int, params *params.Params, powType pow.PowType) float64 {
	instance := pow.GetInstance(powType, 0, 0, []byte{})
	instance.SetParams(params.PowConfig)
	// The minimum difficulty is the max possible proof-of-work limit bits
	// converted back to a number.  Note this is not the same as the proof of
	// work limit directly because the block difficulty is encoded in a block
	// with the compact form which loses precision.
	base := instance.GetSafeDiff(0)
	var difficulty *big.Rat
	if powType == pow.BLAKE2BD || powType == pow.BDAGXKECCAKV1 ||
		powType == pow.BlockdagKECCAK256 ||
		powType == pow.X8R16 ||
		powType == pow.X16RV3 ||
		powType == pow.CRYPTONIGHT ||
		powType == pow.BLAKE2S ||
		powType == pow.SCRYPT {
		if target.Cmp(big.NewInt(0)) > 0 {
			difficulty = new(big.Rat).SetFrac(base, target)
		}
	} else {
		difficulty = new(big.Rat).SetFrac(target, base)
	}

	outString := difficulty.FloatString(8)
	diff, err := strconv.ParseFloat(outString, 64)
	if err != nil {
		log.Error(fmt.Sprintf("Cannot get difficulty: %v", err))
		return 0
	}
	return diff
}

// Return the RPC info
func (api *PublicBlockChainAPI) GetRpcInfo() (any, error) {
	rs := api.node.GetRpcServer().ReqStatus
	jrs := []*cmds.JsonRequestStatus{}
	for _, v := range rs {
		jrs = append(jrs, v.ToJson())
	}
	return jrs, nil
}

func (api *PublicBlockChainAPI) GetTimeInfo() (any, error) {
	return fmt.Sprintf("Now:%s offset:%s", roughtime.Now(), roughtime.Offset()), nil
}

func (api *PublicBlockChainAPI) GetSubsidy() (any, error) {
	best := api.node.GetBlockChain().BestSnapshot()
	sc := api.node.GetBlockChain().GetSubsidyCache()
	mainHeight := int64(best.GraphState.GetMainHeight())
	binfo := api.node.GetBlockChain().Blockdag().GetBlueInfo(api.node.GetBlockChain().Blockdag().GetMainChainTip())

	info := &json.SubsidyInfo{Mode: sc.GetMode(mainHeight), TotalSubsidy: best.TotalSubsidy, BaseSubsidy: params.ActiveNetParams.BaseSubsidy}

	if params.ActiveNetParams.TargetTotalSubsidy > 0 {
		info.TargetTotalSubsidy = params.ActiveNetParams.TargetTotalSubsidy
		info.LeftTotalSubsidy = info.TargetTotalSubsidy - int64(info.TotalSubsidy)
		if info.LeftTotalSubsidy < 0 {
			info.TargetTotalSubsidy = 0
		}
		totalTime := time.Duration(info.TargetTotalSubsidy / info.BaseSubsidy * int64(params.ActiveNetParams.TargetTimePerBlock))
		info.TotalTime = totalTime.Truncate(time.Second).String()

		firstMBlock := api.node.GetBlockChain().Blockdag().GetBlockByOrder(1)
		startTime := time.Unix(api.node.GetBlockChain().GetBlockNode(firstMBlock).GetTimestamp(), 0)
		leftTotalTime := totalTime - time.Since(startTime)
		if leftTotalTime < 0 {
			leftTotalTime = 0
		}
		info.LeftTotalTime = leftTotalTime.Truncate(time.Second).String()
	}
	info.NextSubsidy = sc.CalcBlockSubsidy(binfo, roughtime.Now().Unix())
	return info, nil
}

func (api *PublicBlockChainAPI) GetRpcModules() (any, error) {
	result := []json.KV{
		json.KV{Key: cmds.DefaultServiceNameSpace, Val: false},
		json.KV{Key: cmds.MinerNameSpace, Val: false},
		json.KV{Key: cmds.TestNameSpace, Val: false},
		json.KV{Key: cmds.LogNameSpace, Val: false},
		json.KV{Key: cmds.P2PNameSpace, Val: false},
	}

	for _, m := range api.node.node.Config.Modules {
		for i := 0; i < len(result); i++ {
			if result[i].Key == m {
				result[i].Val = true
			}
		}
	}
	return json.OrderedResult(result), nil
}

func (api *PublicBlockChainAPI) GetBDAGInfo() (any, error) {
	mdr := json.BDAGInfoResult{}
	md := api.node.GetBlockChain().Blockdag()
	mdr.Name = md.GetName()
	mdr.Total = md.GetBlockTotal()
	mdr.BlockCacheSize = md.GetBlockCacheSize()
	mdr.BlockCacheHeightSize = md.GetMinBlockCacheSize()
	mdr.BlockCacheRate = fmt.Sprintf("%.2f%%", float64(md.GetBlockCacheSize())/float64(mdr.Total)*100)
	mdr.BlockDataCacheSize = fmt.Sprintf("%d / %d", md.GetBlockDataCacheSize(), md.GetMinBlockDataCacheSize())
	mdr.AnticoneSize = md.GetInstance().(*blockdag.Phantom).AnticoneSize()
	return mdr, nil
}

func (api *PublicBlockChainAPI) GetDatabaseInfo() (any, error) {
	info, _ := api.node.db.GetInfo()
	ret := json.OrderedResult{
		json.KV{Key: "name", Val: api.node.db.Name()},
		json.KV{Key: "info", Val: info.String()},
		json.KV{Key: "engine", Val: api.node.db.DBEngine()},
		json.KV{Key: "snapshot", Val: api.node.db.SnapshotInfo()},
	}
	return ret, nil
}

func (api *PublicBlockChainAPI) GetChainInfo(lastCount int, start *int64, end *int64) (any, error) {
	if !api.node.GetPeerServer().IsNearlySynced() {
		return nil, fmt.Errorf("Busy, try again later")
	}
	count := blockdag.MinBlockPruneSize
	if lastCount > 1 {
		count = lastCount
	}
	md := api.node.GetBlockChain().Blockdag()
	var startBlock blockdag.IBlock
	var endBlock blockdag.IBlock
	var info *json.ChainInfoResult
	mainTip := md.GetMainChainTip()
	totalTxs := 0
	emptyBlocks := 0
	startOrder := uint(0)
	if start != nil {
		startOrder = uint(*start)
	}
	endOrder := uint(0)
	if end != nil {
		endOrder = uint(*end)
	}
	if startOrder > 0 {
		if endOrder > 0 {
			endBlock = md.GetBlockByOrder(endOrder)
			if endBlock == nil {
				return nil, fmt.Errorf("No end block by order:%d", endOrder)
			}
		} else {
			endOrder = mainTip.GetOrder()
		}
		if startOrder >= endOrder {
			return nil, fmt.Errorf("Invalid start block by order:%d", startOrder)
		}
		startBlock = md.GetBlockByOrder(startOrder)
		if startBlock == nil {
			return nil, fmt.Errorf("No start block by order:%d", startOrder)
		}

		info = &json.ChainInfoResult{Count: 0}
		var block blockdag.IBlock
		for i := startOrder + 1; i <= endOrder; i++ {
			block = md.GetBlockByOrder(i)
			if block == nil {
				return nil, fmt.Errorf("No block by order:%d", i)
			}
			blockNode := api.node.GetBlockChain().GetBlockNode(block)
			if blockNode == nil {
				return true, nil
			}
			info.Count++
			totalTxs += blockNode.GetPriority()
			if blockNode.GetPriority() <= 1 {
				emptyBlocks++
			}
			if endBlock != nil {
				continue
			}
			if info.Count >= uint64(count) {
				endBlock = block
				break
			}
		}
		if endBlock == nil {
			endBlock = block
		}
	} else {
		endBlock = mainTip
		info = &json.ChainInfoResult{Count: 0}
		var blockNode *blockchain.BlockNode
		err := md.Foreach(endBlock, uint(count), blockdag.All, func(block blockdag.IBlock) (bool, error) {
			if block.GetID() <= 0 {
				return true, nil
			}
			blockNode = api.node.GetBlockChain().GetBlockNode(block)
			if blockNode == nil {
				return true, nil
			}
			info.Count++
			totalTxs += blockNode.GetPriority()
			if blockNode.GetPriority() <= 1 {
				emptyBlocks++
			}
			startBlock = block
			return true, nil
		})
		if err != nil {
			return nil, err
		}
		if blockNode != nil {
			totalTxs -= blockNode.GetPriority()
			if totalTxs < 0 {
				totalTxs = 0
			}
			if blockNode.GetPriority() <= 1 {
				emptyBlocks--
			}
		}
		endNode := api.node.GetBlockChain().GetBlockNode(endBlock)
		if endNode == nil {
			return nil, fmt.Errorf("No block:%s", endBlock.GetHash().String())
		}
		totalTxs += endNode.GetPriority()
		if endNode.GetPriority() <= 1 {
			emptyBlocks++
		}
	}

	if info.Count <= 0 {
		return nil, fmt.Errorf("No blocks")
	}

	info.Start = fmt.Sprintf("%s (order:%d)", startBlock.GetHash().String(), startBlock.GetOrder())
	info.End = fmt.Sprintf("%s (order:%d)", endBlock.GetHash().String(), endBlock.GetOrder())
	startNode := api.node.GetBlockChain().GetBlockHeader(startBlock)
	if startNode == nil {
		return nil, fmt.Errorf("No block:%s", startBlock.GetHash().String())
	}
	endNode := api.node.GetBlockChain().GetBlockNode(endBlock)
	if endNode == nil {
		return nil, fmt.Errorf("No block:%s", endBlock.GetHash().String())
	}

	totalTime := endNode.GetTimestamp() - startNode.Timestamp.Unix()
	if totalTime < 0 {
		totalTime = 0
	}
	if totalTime <= 0 {
		return nil, fmt.Errorf("Time is too short")
	}
	info.BlocksPerSecond = float64(info.Count) / float64(totalTime)
	info.TxsPerSecond = float64(totalTxs) / float64(totalTime)

	totalHeight := int64(endBlock.GetHeight()) - int64(startBlock.GetHeight())
	if totalHeight < 0 {
		totalHeight = 0
	}
	if totalHeight > 0 {
		secondPerHeight := time.Duration(totalTime) * time.Second / time.Duration(totalHeight)
		info.SecondPerHeight = secondPerHeight.String()
		info.Concurrency = float64(info.Count) / float64(totalHeight)
	}
	info.EmptyBlockRate = fmt.Sprintf("%d%%", uint64(emptyBlocks*100)/info.Count)
	info.ProcessQueueSize = int32(api.node.GetBlockChain().ProcessQueueSize())
	return *info, nil
}

type PrivateBlockChainAPI struct {
	node *BlockdagFull
}

func NewPrivateBlockChainAPI(node *BlockdagFull) *PrivateBlockChainAPI {
	return &PrivateBlockChainAPI{node}
}

// Stop the node
func (api *PrivateBlockChainAPI) Stop() (any, error) {
	select {
	case api.node.GetRpcServer().RequestedProcessShutdown() <- struct{}{}:
	default:
	}
	return "Blockdag stopping.", nil
}

// SetRpcMaxClients
func (api *PrivateBlockChainAPI) SetRpcMaxClients(max int) (any, error) {
	if max <= 0 {
		err := fmt.Errorf("error:Must greater than 0 (cur max =%d)", api.node.node.Config.RPCMaxClients)
		return api.node.node.Config.RPCMaxClients, err
	}
	api.node.node.Config.RPCMaxClients = max
	return api.node.node.Config.RPCMaxClients, nil
}

func (api *PrivateBlockChainAPI) GetConfig() (any, error) {
	cs, err := js.Marshal(*api.node.node.Config)
	if err != nil {
		return nil, err
	}
	return string(cs), nil
}

type PrivateLogAPI struct {
	node *BlockdagFull
}

func NewPrivateLogAPI(node *BlockdagFull) *PrivateLogAPI {
	return &PrivateLogAPI{node}
}

// set log
func (api *PrivateLogAPI) SetLogLevel(level string) (any, error) {
	err := common.ParseAndSetDebugLevels(level)
	if err != nil {
		return nil, err
	}
	eth.InitLog(level, api.node.node.Config.DebugPrintOrigins)
	return level, nil
}
