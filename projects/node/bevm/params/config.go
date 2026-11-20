package params

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	eparams "github.com/ethereum/go-ethereum/params"
)

// StakingV2ForkBlockEnv sets the devnet-only fork block at which every node
// installs the StakingContractV2 runtime behind the staking proxy
// (bevm/bdag/consensus/staking_v2_upgrade.go). Unset, "0" or "off" disables the
// fork. It is consensus-critical: every node of a devnet must share the value.
const StakingV2ForkBlockEnv = "STAKING_V2_FORK_BLOCK"

// BdagDevnetStakingV2Block is the parsed StakingV2ForkBlockEnv value (nil = no fork).
var BdagDevnetStakingV2Block = parseForkBlockEnv(os.Getenv(StakingV2ForkBlockEnv))

// parseForkBlockEnv turns an env value into a fork block. Invalid input panics at
// startup: a typo silently disabling the fork on one node would split consensus.
func parseForkBlockEnv(v string) *big.Int {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "0", "off", "none", "disabled":
		return nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		panic(fmt.Sprintf("%s=%q is not a positive decimal block number", StakingV2ForkBlockEnv, v))
	}
	return new(big.Int).SetUint64(n)
}

const (
	// MainnetFixedGasLimit is the required gas limit for all BEVM mainnet blocks.
	MainnetFixedGasLimit uint64 = 45_000_000

	// BdagPredeployMigrationBlockNumber repairs genesis predeploy bytecode at this
	// mainnet block.
	BdagPredeployMigrationBlockNumber uint64 = 11_836_362
)

func BdagPredeployMigrationBlock() *big.Int {
	return new(big.Int).SetUint64(BdagPredeployMigrationBlockNumber)
}

// The first official release receives a newly reviewed compiled schedule.
// An unscheduled preparation build cannot inherit the retired rehearsal.
var BdagTestnetGovernanceForkBlock, BdagTestnetGovernanceFork = mustTestnetGovernanceSchedule(testnetGovernanceScheduleJSON)

func IsMainnetChainID(chainID *big.Int) bool {
	if chainID == nil || BdagMainnetChainConfig.ChainID == nil {
		return false
	}
	return chainID.Cmp(BdagMainnetChainConfig.ChainID) == 0
}

var (
	// Chain IDs must match github.com/BlockdagNetworkLabs/go-ethereum params/config_bdag.go
	// (BdagMainnetChainConfig.ChainID etc.) or EIP-155 tx recovery fails with invalid sender during sync.
	BdagMainnetChainConfig = &eparams.ChainConfig{
		ChainID:                     big.NewInt(1404),
		BdagMainnetDAO:              BdagMainnetDAO,
		HomesteadBlock:              big.NewInt(0),
		DAOForkBlock:                big.NewInt(0),
		DAOForkSupport:              false,
		EIP150Block:                 big.NewInt(0),
		EIP155Block:                 big.NewInt(0),
		EIP158Block:                 big.NewInt(0),
		ByzantiumBlock:              big.NewInt(0),
		ConstantinopleBlock:         big.NewInt(0),
		PetersburgBlock:             big.NewInt(0),
		IstanbulBlock:               big.NewInt(0),
		MuirGlacierBlock:            big.NewInt(0),
		BerlinBlock:                 big.NewInt(0),
		LondonBlock:                 big.NewInt(0),
		BdagPredeployMigrationBlock: BdagPredeployMigrationBlock(),
		Ethash:                      new(eparams.EthashConfig),
	}

	BdagTestnetChainConfig = &eparams.ChainConfig{
		ChainID:                  big.NewInt(1043),
		HomesteadBlock:           big.NewInt(0),
		DAOForkBlock:             big.NewInt(0),
		DAOForkSupport:           false,
		EIP150Block:              big.NewInt(0),
		EIP155Block:              big.NewInt(0),
		EIP158Block:              big.NewInt(0),
		ByzantiumBlock:           big.NewInt(0),
		ConstantinopleBlock:      big.NewInt(0),
		PetersburgBlock:          big.NewInt(0),
		IstanbulBlock:            big.NewInt(0),
		MuirGlacierBlock:         big.NewInt(0),
		BerlinBlock:              big.NewInt(0),
		LondonBlock:              big.NewInt(0),
		BdagGovernanceForkBlock:  BdagTestnetGovernanceForkBlock,
		BdagGovernanceFork:       BdagTestnetGovernanceFork,
		BdagTestnetTimingBlock:   BdagTestnetTimingForkBlock,
		BdagTestnetTiming:        BdagTestnetTimingFork,
		BdagSeparateStakingBlock: BdagSeparateStakingForkBlock,
		BdagSeparateStaking:      BdagSeparateStakingFork,
		Ethash:                   new(eparams.EthashConfig),
	}

	// Devnet chain ID 8008; use local constant so we don't depend on go-ethereum fork exporting BdagDevnetChainConfig
	BdagDevnetChainConfig = &eparams.ChainConfig{
		ChainID:                 big.NewInt(8008),
		HomesteadBlock:          big.NewInt(0),
		DAOForkBlock:            big.NewInt(0),
		DAOForkSupport:          false,
		EIP150Block:             big.NewInt(0),
		EIP155Block:             big.NewInt(0),
		EIP158Block:             big.NewInt(0),
		ByzantiumBlock:          big.NewInt(0),
		ConstantinopleBlock:     big.NewInt(0),
		PetersburgBlock:         big.NewInt(0),
		IstanbulBlock:           big.NewInt(0),
		MuirGlacierBlock:        big.NewInt(0),
		BerlinBlock:             big.NewInt(0),
		LondonBlock:             big.NewInt(0),
		BdagStakingV2Block:      BdagDevnetStakingV2Block,
		BdagGovernanceForkBlock: BdagDevnetGovernanceForkBlock,
		BdagGovernanceFork:      BdagDevnetGovernanceFork,
		Ethash:                  new(eparams.EthashConfig),
	}

	BdagMixnetChainConfig = &eparams.ChainConfig{
		ChainID:             big.NewInt(1046),
		HomesteadBlock:      big.NewInt(0),
		DAOForkBlock:        big.NewInt(0),
		DAOForkSupport:      false,
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         nil,
		Ethash:              new(eparams.EthashConfig),
	}

	BdagPrivnetChainConfig = &eparams.ChainConfig{
		ChainID:             big.NewInt(1048),
		HomesteadBlock:      big.NewInt(0),
		DAOForkBlock:        big.NewInt(0),
		DAOForkSupport:      false,
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		Ethash:              new(eparams.EthashConfig),
	}
)
