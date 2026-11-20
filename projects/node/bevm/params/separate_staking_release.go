package params

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	eparams "github.com/ethereum/go-ethereum/params"
	"io"
	"math/big"
)

// No live schedule until the exact candidate is qualified and sealed.
//
//go:embed separate-staking-schedule.json
var separateStakingScheduleJSON []byte
var BdagSeparateStakingForkBlock, BdagSeparateStakingFork = mustSeparateStakingSchedule(separateStakingScheduleJSON)

func mustSeparateStakingSchedule(raw []byte) (*big.Int, *eparams.BDAGSeparateStakingForkConfig) {
	var schedule *struct {
		EarliestEVMBlock uint64                                 `json:"earliestEvmBlock"`
		Policy           *eparams.BDAGSeparateStakingForkConfig `json:"policy"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		panic(fmt.Sprintf("invalid separate staking schedule: %v", err))
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		panic("separate staking schedule requires exactly one object or null")
	}
	if schedule == nil {
		return nil, nil
	}
	if schedule.EarliestEVMBlock == 0 {
		panic("separate staking requires positive EVM floor")
	}
	if err := schedule.Policy.Validate(big.NewInt(1043)); err != nil {
		panic(err)
	}
	return new(big.Int).SetUint64(schedule.EarliestEVMBlock), schedule.Policy
}
