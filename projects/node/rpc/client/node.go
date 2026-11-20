package client

import (
	"encoding/json"

	j "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
)

type FutureGetCapabilitiesResult chan *response

func (r FutureGetCapabilitiesResult) Receive() (*j.ComponentCapabilitiesResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}

	var capabilities j.ComponentCapabilitiesResult
	if err := json.Unmarshal(res, &capabilities); err != nil {
		return nil, err
	}
	return &capabilities, nil
}

func (c *Client) GetCapabilitiesAsync() FutureGetCapabilitiesResult {
	return c.sendCmd(cmds.NewGetCapabilitiesCmd())
}

func (c *Client) GetCapabilities() (*j.ComponentCapabilitiesResult, error) {
	return c.GetCapabilitiesAsync().Receive()
}

type FutureGetChainIdentityResult chan *response

func (r FutureGetChainIdentityResult) Receive() (*j.ChainIdentityResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}

	var identity j.ChainIdentityResult
	if err := json.Unmarshal(res, &identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

func (c *Client) GetChainIdentityAsync() FutureGetChainIdentityResult {
	return c.sendCmd(cmds.NewGetChainIdentityCmd())
}

func (c *Client) GetChainIdentity() (*j.ChainIdentityResult, error) {
	return c.GetChainIdentityAsync().Receive()
}

type FutureGetNodeInfoResult chan *response

func (r FutureGetNodeInfoResult) Receive() (*j.InfoNodeResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}

	// Unmarshal result as a getinfo result object.
	var infoRes j.InfoNodeResult
	err = json.Unmarshal(res, &infoRes)
	if err != nil {
		return nil, err
	}

	return &infoRes, nil
}

func (c *Client) GetNodeInfoAsync() FutureGetNodeInfoResult {
	cmd := cmds.NewGetNodeInfoCmd()
	return c.sendCmd(cmd)
}

func (c *Client) GetNodeInfo() (*j.InfoNodeResult, error) {
	return c.GetNodeInfoAsync().Receive()
}

type FutureGetPeerInfoResult chan *response

func (r FutureGetPeerInfoResult) Receive() ([]j.GetPeerInfoResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}

	// Unmarshal result as a getinfo result object.
	var infoRes []j.GetPeerInfoResult
	err = json.Unmarshal(res, &infoRes)
	if err != nil {
		return nil, err
	}

	return infoRes, nil
}

func (c *Client) GetPeerInfoAsync() FutureGetPeerInfoResult {
	cmd := cmds.NewGetPeerInfoCmd()
	return c.sendCmd(cmd)
}

func (c *Client) GetPeerInfo() ([]j.GetPeerInfoResult, error) {
	return c.GetPeerInfoAsync().Receive()
}

type FutureCheckAddressResult chan *response

func (r FutureCheckAddressResult) Receive() (bool, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return false, err
	}
	var result bool
	err = json.Unmarshal(res, &result)
	if err != nil {
		return false, err
	}

	return result, nil
}

func (c *Client) CheckAddressAsync(address string, network string) FutureCheckAddressResult {
	cmd := cmds.NewCheckAddressCmd(address, network)
	return c.sendCmd(cmd)
}

func (c *Client) CheckAddress(address string, network string) (bool, error) {
	return c.CheckAddressAsync(address, network).Receive()
}

type FutureGetRpcInfoResult chan *response

func (r FutureGetRpcInfoResult) Receive() (*cmds.JsonRequestStatus, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}

	var result cmds.JsonRequestStatus
	err = json.Unmarshal(res, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) GetRpcInfoAsync() FutureGetRpcInfoResult {
	cmd := cmds.NewGetRpcInfoCmd()
	return c.sendCmd(cmd)
}

func (c *Client) GetRpcInfo() (*cmds.JsonRequestStatus, error) {
	return c.GetRpcInfoAsync().Receive()
}

type FutureGetTimeInfoResult chan *response

func (r FutureGetTimeInfoResult) Receive() (string, error) {
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

func (c *Client) GetGetTimeInfoAsync() FutureGetTimeInfoResult {
	cmd := cmds.NewGetTimeInfoCmd()
	return c.sendCmd(cmd)
}

func (c *Client) GetTimeInfo() (string, error) {
	return c.GetGetTimeInfoAsync().Receive()
}

type FutureStopResult chan *response

func (r FutureStopResult) Receive() (string, error) {
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

func (c *Client) StopAsync() FutureStopResult {
	cmd := cmds.NewStopCmd()
	return c.sendCmd(cmd)
}

func (c *Client) Stop() (string, error) {
	return c.StopAsync().Receive()
}

type FutureBanlistResult chan *response

func (r FutureBanlistResult) Receive() (*j.GetBanlistResult, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return nil, err
	}

	var result j.GetBanlistResult
	err = json.Unmarshal(res, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) BanlistAsync() FutureBanlistResult {
	cmd := cmds.NewBanlistCmd()
	return c.sendCmd(cmd)
}

func (c *Client) Banlist() (*j.GetBanlistResult, error) {
	return c.BanlistAsync().Receive()
}

type FutureRemoveBanResult chan *response

func (r FutureRemoveBanResult) Receive() (bool, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return false, err
	}

	var result bool
	err = json.Unmarshal(res, &result)
	if err != nil {
		return false, err
	}

	return result, nil
}

func (c *Client) RemoveBanAsync(id string) FutureRemoveBanResult {
	cmd := cmds.NewRemoveBanCmd(id)
	return c.sendCmd(cmd)
}

func (c *Client) RemoveBan(id string) (bool, error) {
	return c.RemoveBanAsync(id).Receive()
}

type FutureSetRpcMaxClientsResult chan *response

func (r FutureSetRpcMaxClientsResult) Receive() (int, error) {
	res, err := receiveFuture(r)
	if err != nil {
		return 0, err
	}

	var result int
	err = json.Unmarshal(res, &result)
	if err != nil {
		return 0, err
	}

	return result, nil
}

func (c *Client) SetRpcMaxClientsAsync(max int) FutureSetRpcMaxClientsResult {
	cmd := cmds.NewSetRpcMaxClientsCmd(max)
	return c.sendCmd(cmd)
}

func (c *Client) SetRpcMaxClients(max int) (int, error) {
	return c.SetRpcMaxClientsAsync(max).Receive()
}

type FutureSetLogLevelResult chan *response

func (r FutureSetLogLevelResult) Receive() (string, error) {
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

func (c *Client) SetLogLevelAsync(level string) FutureSetLogLevelResult {
	cmd := cmds.NewSetLogLevelCmd(level)
	return c.sendCmd(cmd)
}

func (c *Client) SetLogLevel(level string) (string, error) {
	return c.SetLogLevelAsync(level).Receive()
}
