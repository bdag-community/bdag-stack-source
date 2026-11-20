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

// This candidate pins a separate timing schedule and never overwrites V3.
// Compiled distribution/notice times are latest-allowed delivery bounds, not
// evidence that anyone received the package. Missing a bound/window requires
// a new candidate. Building this source does not authorize mining/signalling.
//
//go:embed testnet-timing-schedule.json
var testnetTimingScheduleJSON []byte

var BdagTestnetTimingForkBlock, BdagTestnetTimingFork = mustTestnetTimingSchedule(testnetTimingScheduleJSON)

func mustTestnetTimingSchedule(raw []byte) (*big.Int, *eparams.BDAGTestnetTimingForkConfig) {
	var schedule *struct {
		EarliestEVMBlock uint64                               `json:"earliestEvmBlock"`
		Policy           *eparams.BDAGTestnetTimingForkConfig `json:"policy"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		panic(fmt.Sprintf("invalid testnet timing schedule: %v", err))
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		panic("timing schedule must contain exactly one object or null")
	}
	if schedule == nil {
		return nil, nil
	}
	if schedule.EarliestEVMBlock == 0 {
		panic("timing schedule requires a positive EVM boundary")
	}
	if err := schedule.Policy.Validate(big.NewInt(1043)); err != nil {
		panic(err)
	}
	return new(big.Int).SetUint64(schedule.EarliestEVMBlock), schedule.Policy
}
