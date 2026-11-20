# Genesis Allocation Updater

This tool automates the injection of **UUPS Proxy Contracts** (Staking and Governance) into the `genesis_alloc.go` file for the BlockDAG Mainnet.

## Components

- **`main.go`**: The Go tool that reads compiled artifacts and updates the `alloc` map in `genesis_alloc.go`.
- **`prepare_genesis_contracts.sh`**: Helper script in `cmd/e2e_cli/` that compiles contracts and runs the Go tool.

## How to Run (Standard)

1. Navigate to `cmd/e2e_cli/`:
   ```bash
   cd cmd/e2e_cli
   ```
2. Run the preparation script:
   ```bash
   ./prepare_genesis_contracts.sh
   ```
   This will:
   - Compile contracts in `Contracts/`.
   - Update `bevm/bdag/genesis_alloc.go` with new bytecode and storage.

## How to Reset / Start Over

If you need to generate a *fresh* genesis (e.g., you changed the source code or want to reset the allocations):

1. **Revert** `genesis_alloc.go` to its original clean state:
   ```bash
   git checkout bevm/bdag/genesis_alloc.go
   ```
   *Warning: This will discard any manual changes to the file.*

2. **Re-run** the script:
   ```bash
   ./prepare_genesis_contracts.sh
   ```

## Configuration

### Proxy Owner
The "Owner" of the proxies (recorded in the Admin Slot `0xb531...`) is defined in `cmd/e2e_cli/prepare_genesis_contracts.sh`:
```bash
PROXY_OWNER="0x3655..."
```
Update this variable if you need to change the owner.

### Networks
By default, this tool **only updates Mainnet**.
To target other networks, edit the `NETWORKS` array in `prepare_genesis_contracts.sh`.

### Source of Contracts
- **Proxy**: `contracts/proxy/UUPSProxy.sol` (Minimal ERC1967 Proxy)
- **Staking**: `contracts/staking/StakingPlaceholder.sol` (Placeholder for Mainnet Launch)
- **Governance**: `contracts/governance/GovernanceV1.sol` (Real Implementation)

Note: The `StakingPool.sol` contract is **not** deployed at genesis; it will be deployed later and the proxy will be upgraded to point to it.

## Genesis Address Map & Usage

Because of the UUPS Proxy pattern, we must allocate **both** the Proxy ("the face") and the Implementation ("the brain") in genesis.

| Component | Address | Type | Status at Genesis | Post-Launch Action |
| :--- | :--- | :--- | :--- | :--- |
| **Staking Proxy** | `0x1000000000000000000000000000000000000001` | Proxy | Points to Placeholder | **None**. Wait for Hard Fork / Upgrade. |
| Staking Impl | `0x1000000000000000000000000000000000000011` | Logic | Placeholder Code | None. (Will be replaced later). |
| **Governance Proxy** | `0x1000000000000000000000000000000000000005` | Proxy | Points to GovernanceV1 | **MUST CALL `initialize()`**. |
| Governance Impl | `0x1000000000000000000000000000000000000015` | Logic | Real Governance Logic | None. |

### 🛑 Critical First Step: Initialize Governance
The Governance Proxy is live containing the real logic, but it is **uninitialized**. As soon as the chain launches, the Owner (`0x3655...`) must call:

```solidity
// Target Contract: Governance Proxy (0x...05)
function initialize(
    address _admin,           // 0x3655... (Owner)
    uint256 _votingPeriod,    // e.g. 17280 (3 days @ 15s blocks)
    uint256 _proposalThreshold,
    uint256 _quorumVotes,
    address _treasury,
    address _stakingToken     // 0x000...000 (if using native coin)
)
```
This activates the governance system.
