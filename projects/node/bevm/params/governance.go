package params

import (
	"encoding/json"
	"fmt"
	ethparams "github.com/ethereum/go-ethereum/params"
	"io"
	"math/big"
	"os"
	"strings"
)

const GovernanceForkBlockEnv = "GOVERNANCE_FORK_BLOCK"
const GovernanceForkConfigEnv = "GOVERNANCE_FORK_CONFIG_JSON"

var BdagDevnetGovernanceForkBlock = parseForkBlockEnv(os.Getenv(GovernanceForkBlockEnv))
var BdagDevnetGovernanceFork = parseGovernanceForkConfig(BdagDevnetGovernanceForkBlock, os.Getenv(GovernanceForkConfigEnv))

func parseGovernanceForkConfig(block *big.Int, raw string) *ethparams.BDAGGovernanceForkConfig {
	if block == nil {
		if strings.TrimSpace(raw) != "" {
			panic("GOVERNANCE_FORK_CONFIG_JSON supplied without an enabled GOVERNANCE_FORK_BLOCK")
		}
		return nil
	}
	var config *ethparams.BDAGGovernanceForkConfig
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&config); err != nil {
		panic(fmt.Sprintf("invalid %s: %v", GovernanceForkConfigEnv, err))
	}
	if err := dec.Decode(new(interface{})); err != io.EOF {
		panic("governance fork config must contain exactly one JSON object")
	}
	if err := config.Validate(); err != nil {
		panic(err)
	}
	return config
}
