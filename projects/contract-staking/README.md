# BlockDAG Contracts

This repository contains the smart contracts for the BlockDAG Network.

## Project Structure

- **Staking**: `contracts/staking/StakingPool.sol` (Deployed via Genesis)

## Getting Started

### 1. Installation
```bash
npm install
```

### 2. Configuration
Create a `.env` file (see `.env.example`):
```env
# Common
PRIVATE_KEY=0x...
AWAKENING_RPC_URL=https://rpc.awakening.network
AWAKENING_CHAIN_ID=54211

# Contract Addresses
STAKING_CONTRACT_ADDRESS=0x...
```

---

## Staking Operations

Scripts to interact with the Staking Contract.
**Required Env**: `STAKING_CONTRACT_ADDRESS`

### Stake
Stakes tokens to a specific pool.
```bash
AMOUNT=100 POOL_ADDRESS=0x... npm run stake
```

### Unstake
Unstakes tokens from a specific pool.
```bash
AMOUNT=100 POOL_ADDRESS=0x... npm run unstake
```

### Claim
Claims rewards and processes unbonding for a specific pool.
```bash
POOL_ADDRESS=0x... npm run claim
```

---


## Development

### Compile All Contracts
```bash
npm run compile
```

### Run Tests
```bash
npm test
```
