package miner

import "github.com/BlockdagNetworkLabs/bdag/core/json"

type BlockChainChangeMsg struct {
}

type MempoolChangeMsg struct {
}

type gbtResponse struct {
	result any
	err    error
}

type GBTMiningMsg struct {
	request *json.TemplateRequest
	reply   chan *gbtResponse
}
