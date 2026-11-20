# Smart Contracts

This directory manages the integration of Smart Contracts into the BlockDAG network genesis state.

## 1. Contract Sources

The active development of Smart Contracts happens in the **External Repository**:
*   **Repo**: https://github.com/BlockdagNetworkLabs/Contracts
*   **Workflow**: You clone that repo, compile it there, and point our tools to its `artifacts` folder.

**Note**: The files in `system/` (`ChainTally.sol`, etc.) are read-only interfaces for engine-managed data. They are stable and rarely change.

## 2. Compilation (External)

1.  Clone the repository:
    ```bash
    git clone https://github.com/BlockdagNetworkLabs/Contracts
    cd Contracts
    ```
2.  Install & Compile:
    ```bash
    npm install
    npx hardhat compile
    ```
    This generates the `artifacts/contracts` directory needed by our tool.

## 3. Genesis Update

We use the `cmd/update_genesis_alloc` tool to inject the compiled bytecode and initialize storage slots directly into `genesis_alloc.go`.

### Usage

Run this from the root of the `blockdag` repository:

```bash
go run cmd/update_genesis_alloc/main.go \
  --network <NETWORK_NAME> \
  --artifacts <PATH_TO_CONTRACTS_REPO>/artifacts/contracts \
  --multisig <MULTISIG_ADDRESS> \
  --admin <ADMIN_VAULT_ADDRESS>
```

### Example

```bash
go run cmd/update_genesis_alloc/main.go \
  --network privnet \
  --artifacts ../Contracts/artifacts/contracts \
  --multisig 0x1234567890123456789012345678901234567890 \
  --admin 0x0987654321098765432109876543210987654321
```

**What this does:**
1.  LOCATES the latest bytecode for Staking (and others) in your provided `artifacts` folder.
2.  UPDATES the `contracts` entry in `genesis_alloc.go`.
3.  INJECTS initialized storage (Slots 2, 4, etc.) for `multiSig`, `adminVault`, and splits.

## 4. System Contracts (Read-Only)

The `system/` contracts are read-only interfaces to data written directly by the Consensus Engine.

### `ChainTally.sol` (0x...002)
Exposes the consensus weight/score of the network.
-   **Slot 0 (`currentEpoch`)**: The current epoch number.
-   **Slot 1 (`totalLiquidity`)**: Total staked liquidity in the system.
-   **Slot 2 (`currentTally`)**: `mapping(address => uint256)`. Stores the current consensus weight/score for each validator/pool address.
-   **Slot 3 (`lastTally`)**: `mapping(address => uint256)`. Stores the weight/score from the previous epoch.
