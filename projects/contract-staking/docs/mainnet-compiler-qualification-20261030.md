# Scoped compiler configuration repair — 30 October 2026

The committed configuration omitted the per-source IR override described in `adr-versioned-proposal-timing.md`. A clean/default compile produced a 25843-byte `VersionedEpochGovernor`, exceeding the 24576-byte EIP-170 limit. The full baseline run had 157 passing / four failing tests; failures were the size gate, real versioned Governor construction and two runtime-bundle checks.

Restore Solidity 0.8.24, London, optimizer 200, **viaIR only for VersionedEpochGovernor.sol**. Keep all other sources on their existing compiler pipeline. Do not increase the EVM code-size limit, remove authority checks or alter historical artifacts.

With a clean lockfile installation and Node 24.19.0:

- Complete Hardhat suite: **177 passing**.
- Versioned Governor: **22622 bytes**, under EIP-170.
- Rebinding the ten versioned Testnet runtimes reproduces every runtime hash already embedded in qualified core `5a4cd42527704111ee244912774a0f9ee0b65ba8`.
- Historical allocation SHA-256 remains `304aeee52c451e383f1960bdc32d0415d1ad9bc3e0fe19dff95af197bc82d199`.
- Original EpochGovernor, GovernanceTimelock, GovernancePolicyModule and GovernanceHubV2 retain non-IR/London compilation.

The first isolated setup used a node_modules symlink; the existing integrity-checked London Bytes hook matches the project-local absolute dependency path, so symlink resolution bypassed the compatibility substitution and compilation failed on mcopy. A clean project-local `npm ci --ignore-scripts` fixed the setup; no dependency bytes or hook validation were weakened. This is a reproducibility lesson, not a contract runtime change.

This repair does not deploy contracts, modify the running Testnet, complete the distinct-address staking migration or qualify Mainnet. Evidence: `devnet-ops/evidence/20261030-mainnet-preparation/contracts-tests.log`, `contracts-green-clean.log`, `contract-runtime-reproduction.json`.
