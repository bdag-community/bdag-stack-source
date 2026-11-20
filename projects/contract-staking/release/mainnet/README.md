# Mainnet local authority and platform preparation

These are **review records, not deployment authorization**. No command here connects a signer to Mainnet or broadcasts a transaction. The `mainnet-dao-stakingv2` core handoff pins this repository's exact commit.

## Reproduce locally

Use Node 24.19.0 and the committed lockfile. The rehearsal configuration is an in-process London EVM with chain ID 1404, public lab keys and synthetic time. It contains no external RPC/key configuration.

```sh
npm ci --ignore-scripts
npm test
npm run test:mainnet-migration
MAINNET_MULTISIG_OUTPUT=/private/new-review/safe.json npm run qualify:mainnet-multisig
MAINNET_PREPARATION_OUTPUT=/private/new-review/program npm run prepare:mainnet
```

Use new output paths; preserve earlier evidence. `prepare:mainnet` exports the 19-role native program, constructor/context records and matched public application environment templates with the expressly approved reviewer identities. **The output remains review-only; do not install it or change its status to pass a gate.** Actual compiled production profile and program in core remain `null`.

## Human wallet

`authority-inputs.json` contains the five public owners and threshold three. `multisig-local-qualification.json` records the untouched, pinned Safe **1.5.0** singleton/proxy/factory artifacts, atomic initialization calldata and a local configuration check with those actual public addresses. The local contract addresses are not approved production addresses.

All ten combinations of three lab signers execute; two signers, duplicates, outsiders, wrong chain/wallet, changed calldata and nonce replay reject. Signer replacement and failed-call recovery also pass. This proves the mechanism, **not control of the five people's keys**. No real owner key was loaded and no owner signature is included.

No setup delegatecall, module, guard, fallback handler or setup payment is enabled. The offline packet builder permits plain CALLs with no Safe refund payment. Current quorum can itself change signers, threshold or modules; it is not an immutable threshold. Chain-ID/wallet/nonce binding does not distinguish deliberately duplicated deployments on a same-ID fork: independently verify canonical genesis/history and the actual production deployment as well.

Before separately authorized production deployment/role binding:

1. Each person independently confirms their address/control/custody using a clearly labelled, expiring, unique verification message. Do not request keys or a recovery phrase. Record the verified result; address presence on RPC is insufficient.
2. Review the exact human owner/admin roles that actually require a wallet. V1/V2 remain Governor → StakingTimelock owned. Do not give Safe a new veto, upgrade bypass or extra approval over normal DAO decisions. Registry reviewers are Francois, Duncan and Pieter; Jeremy and Matt remain eligible to apply as developers.
3. Approve the deployment context, deployer/factory/singleton/nonce/address and initialization hash. Qualify the exact deployment on an isolated copy; independently re-read singleton/proxy code hashes, owners, threshold, initialized status and absence of modules/guard/fallback before any role grant. Recheck current restrictions/collisions.
4. For each later authorized action, prepare its complete `SafeTx` packet offline with `scripts/release/mainnet-multisig.cjs`, using the actual chain 1404, deployed wallet, current nonce and reviewed recipient/value/calldata. Signers verify the decoded action independently. `assembleSignatures` verifies allowed distinct recovered EOAs and orders them; it neither loads keys nor broadcasts. Never use the lab deployment addresses on Mainnet.
5. Rehearse loss of one/two signers, three-owner replacement, nonce reconciliation and backup custody. Fewer than three available owners cannot recover through a hidden administrator. Maintain independently stored signer backups without centralizing the five keys.

Upstream reference: https://github.com/safe-fndn/safe-smart-account/tree/v1.5.0. Documentation: https://docs.safe.global/advanced/smart-account-overview. Inclusion here is not an independent security audit or a claim that Safe's hosted transaction service supports BDAG.

## Production application profiles

The public `platform-profile.review.json` and `platform-environments.review.json` derive from the same native bundle. Staking, Development and API validate identical schema/history/clock/contract/code pins and profile SHA-256. Review/unsealed or wrong-network profiles reject. Wallet mutations add fresh canonical-history, runtime and both staking implementation checks; polling uses lighter history checks. Same chain ID alone is not acceptance.

Both addresses are indexed on 1404: Legacy from genesis with no cutoff for later exits, V2/credentials/governor/hub/reader from their real activation boundary. Unknown boundaries are intentionally `null` and the manifest path is an invalid placeholder. Final API/domain/database/auth/wallet-project configuration remains to be supplied; secrets never belong here. Empty `stakers`/`pools` seeds are not a complete voting census. Native/indexed canonical discovery and 64 confirmations remain required. Phase 2 funding stays off.

## Dependency review

Safe 1.5.0 adds one exact, integrity-pinned package with no transitive dependencies; existing package versions were not changed. `dependency-audit.json` retains the dated NPM advisory report: 80 inherited findings (3 critical, 20 high, 38 moderate, 19 low). These concern the pre-existing Node build/server dependency tree, not newly introduced Solidity instructions. This is not evidence that those dependencies are harmless.

Release review must triage/patch their actual exposure in a separate clean toolchain qualification. In particular, do not expose Hardhat or old contract utility HTTP services publicly, feed them untrusted files, or infer that this repository's Express utilities are production services. Build in isolation from pinned inputs. A blanket `npm audit fix --force`/Hardhat 3 or compiler upgrade can change artifacts and has not been performed. Any fixes must reproduce all frozen runtimes and requalify the sealed release. Safe's own upstream audit is not a review of our native installer, custody or application integration.
