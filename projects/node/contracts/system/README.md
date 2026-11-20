# System Contracts

The `system/` directory contains read-only Solidity interfaces for data managed directly by the BlockDAG Consensus Engine.

## Contracts

### ChainTally.sol
- **Address**: `0x1000000000000000000000000000000000000002`
- **Description**: Exposes the consensus weight/score of the network.
- **Key Storage Slots**:
    - **Slot 0 (`currentEpoch`)**: The current epoch number.
    - **Slot 1 (`totalLiquidity`)**: Total staked liquidity in the system.
    - **Slot 2 (`currentTally`)**: `mapping(address => uint256)`. Stores the current consensus weight/score for each validator/pool address.
    - **Slot 3 (`lastTally`)**: `mapping(address => uint256)`. Stores the weight/score from the previous epoch.

---

> [!NOTE]
> These contracts are read-only interfaces to the blockchain state. The values are updated by the engine at each epoch transition.
