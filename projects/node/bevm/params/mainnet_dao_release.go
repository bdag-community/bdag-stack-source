package params

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	ethparams "github.com/ethereum/go-ethereum/params"
)

// No environment or RPC input may replace a compiled production profile.
// null keeps preparation builds non-activating until reviewed final sealing.
//
//go:embed mainnet-dao-release.json
var mainnetDAOReleaseJSON []byte

var BdagMainnetDAO = mustMainnetDAORelease(mainnetDAOReleaseJSON)

func ParseMainnetDAORelease(raw []byte) (*ethparams.BDAGMainnetDAOConfig, error) {
	var c *ethparams.BDAGMainnetDAOConfig
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	if err := d.Decode(new(interface{})); err != io.EOF {
		return nil, fmt.Errorf("Mainnet profile requires exactly one object or null")
	}
	if c != nil {
		if err := c.Validate(big.NewInt(1404)); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func mustMainnetDAORelease(raw []byte) *ethparams.BDAGMainnetDAOConfig {
	c, err := ParseMainnetDAORelease(raw)
	if err != nil {
		panic(err)
	}
	return c
}
