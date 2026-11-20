# Time-Based Halving Mechanism

This document explains the time-based halving implementation for the BlockDAG network.

## Overview

Unlike Bitcoin's block-height-based halving (which halves every `n` blocks), this implementation uses a **Time-Based Halving** schedule.

- **Halving Interval**: 7 Days (604,800 seconds)
- **Genesis Reference**: All calculations are relative to the Genesis Block timestamp.
- **Independence**: The halving schedule is independent of the network's hashrate or block production speed.

## Formula

The block subsidy is calculated purely based on the block's timestamp:

```go
elapsed_seconds = Block.Header.Timestamp - Genesis.Header.Timestamp
halving_era     = elapsed_seconds / 604800
subsidy         = BaseSubsidy >> halving_era
```

### Parameters
These are defined in `params/params_mainnet.go`:

| Parameter | Value | Description |
| :--- | :--- | :--- |
| `BaseSubsidy` | `260 BDAG` | Initial miner reward per block. |
| `HalvingInterval` | `604800` | Seconds in 7 days. |

## Behavior Analysis

### 1. Schedule Stability
The reward drops exactly every 7 days from the Genesis timestamp.
- **Day 0 - 7**: 260 BDAG
- **Day 7 - 14**: 130 BDAG
- **Day 14 - 21**: 65 BDAG
- ... and so on.

### 2. Block Production & Emission
Because the halving triggers on *time*, the *Total Supply* issued in a given week depends on the number of blocks mined.

- **Standard Rate (1 block/sec)**:
    - Weekly Emission: `604,800 blocks * Reward`.
- **High Rate (e.g., 4 blocks/sec)**:
    - Weekly Emission: `2,419,200 blocks * Reward`.
    - **Note**: This scenario acts as higher inflation. The Difficulty Adjustment Algorithm (DAA) is responsible for keeping the block rate close to the target (1 block/sec) to ensure predictable emission.

## Code References

- **Calculation**: `CalcBlockSubsidy` in [`core/blockchain/subsidy.go`](core/blockchain/subsidy.go)
- **Parameters**: `Params` struct in [`params/params.go`](params/params.go)
- **Validation**: `checkBlockSubsidy` in [`core/blockchain/validate.go`](core/blockchain/validate.go)
- **Unit Test**: [`core/blockchain/time_halving_test.go`](core/blockchain/time_halving_test.go)
