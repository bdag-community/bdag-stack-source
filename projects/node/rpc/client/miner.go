package client

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	j "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
)

type FutureGetBlockTemplateResult chan *response

func (r FutureGetBlockTemplateResult) Receive() (*j.GetBlockTemplateResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}
	var template j.GetBlockTemplateResult
	err = json.Unmarshal(res, &template)
	if err != nil {
		return nil, err
	}
	return &template, nil
}

func (c *Client) GetBlockTemplateAsync(capabilities []string, powType byte) FutureGetBlockTemplateResult {
	cmd := cmds.NewGetBlockTemplateCmd(capabilities, powType)
	return c.sendCmd(cmd)
}

func (c *Client) GetBlockTemplate(capabilities []string, powType byte) (*j.GetBlockTemplateResult, error) {
	return c.GetBlockTemplateAsync(capabilities, powType).Receive()
}

// GetBlockTemplateForAddress requests a real template paying the chosen address.
// The server already supports this optional third getBlockTemplate parameter.
func (c *Client) GetBlockTemplateForAddress(capabilities []string, powType byte, payAddress string) (*j.GetBlockTemplateResult, error) {
	cmd := cmds.NewGetBlockTemplateCmd(capabilities, powType)
	cmd.PayAddress = &payAddress
	return FutureGetBlockTemplateResult(c.sendCmd(cmd)).Receive()
}

type FutureGetTemplateHealthResult chan *response

func (r FutureGetTemplateHealthResult) Receive() (*j.MiningTemplateHealthResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}
	var health j.MiningTemplateHealthResult
	err = json.Unmarshal(res, &health)
	if err != nil {
		return nil, err
	}
	return &health, nil
}

func (c *Client) GetTemplateHealthAsync() FutureGetTemplateHealthResult {
	cmd := cmds.NewGetTemplateHealthCmd()
	return c.sendCmd(cmd)
}

func (c *Client) GetTemplateHealth() (*j.MiningTemplateHealthResult, error) {
	return c.GetTemplateHealthAsync().Receive()
}

type FutureSubmitBlockResult chan *response

func (r FutureSubmitBlockResult) Receive() (string, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return "", err
	}
	var result string
	err = json.Unmarshal(res, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

func (c *Client) SubmitBlockAsync(hexBlock string) FutureSubmitBlockResult {
	cmd := cmds.NewSubmitBlockCmd(hexBlock)
	return c.sendCmd(cmd)
}

func (c *Client) SubmitBlock(hexBlock string) (string, error) {
	return c.SubmitBlockAsync(hexBlock).Receive()
}

type FutureGenerateCmdResult chan *response

func (r FutureGenerateCmdResult) Receive() ([]string, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}
	var result []string
	err = json.Unmarshal(res, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GenerateAsync(numBlocks uint32, powType pow.PowType) FutureGenerateCmdResult {
	cmd := cmds.NewGenerateCmd(numBlocks, powType)
	return c.sendCmd(cmd)
}

func (c *Client) Generate(numBlocks uint32, powType pow.PowType) ([]string, error) {
	return c.GenerateAsync(numBlocks, powType).Receive()
}

type FutureGetRemoteGBTCmdResult chan *response

func (r FutureGetRemoteGBTCmdResult) Receive() (*types.BlockHeader, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}
	serialized, err := hex.DecodeString(strings.ReplaceAll(string(res), `"`, ""))
	if err != nil {
		fmt.Println(err.Error(), len(string(res)), string(res))
		return nil, err
	}
	var header types.BlockHeader
	err = header.Deserialize(bytes.NewReader(serialized))
	if err != nil {
		return nil, err
	}
	return &header, nil
}

func (c *Client) GetRemoteGBTAsync(powType pow.PowType) FutureGetRemoteGBTCmdResult {
	cmd := cmds.NewGetRemoteGBTCmd(powType)
	return c.sendCmd(cmd)
}

func (c *Client) GetRemoteGBT(powType pow.PowType) (*types.BlockHeader, error) {
	return c.GetRemoteGBTAsync(powType).Receive()
}

type FutureSubmitBlockHeaderResult chan *response

func (r FutureSubmitBlockHeaderResult) Receive() (*j.SubmitBlockResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}
	var result j.SubmitBlockResult
	err = json.Unmarshal(res, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SubmitBlockHeaderAsync(header *types.BlockHeader) FutureSubmitBlockHeaderResult {
	cmd := cmds.NewSubmitBlockHeaderCmd(header)
	return c.sendCmd(cmd)
}

func (c *Client) SubmitBlockHeader(header *types.BlockHeader) (*j.SubmitBlockResult, error) {
	return c.SubmitBlockHeaderAsync(header).Receive()
}

func (c *Client) SubmitBlockHeaderV2Async(header *types.BlockHeader) FutureSubmitBlockHeaderResult {
	cmd := cmds.NewSubmitBlockHeaderV2Cmd(header)
	return c.sendCmd(cmd)
}

func (c *Client) SubmitBlockHeaderV2(header *types.BlockHeader) (*j.SubmitBlockResult, error) {
	return c.SubmitBlockHeaderV2Async(header).Receive()
}
