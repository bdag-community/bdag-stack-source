# Separate Legacy and V2 staking rehearsal

This transition is restricted to the existing Community Testnet, chain 1043.
It does not authorize installation, signalling, activation or a Mainnet release.

- Retiring proxy: `0x08Bd519F611556dC148e7BfFE8d6f078F23a8EF7`.
- Fresh V2 proxy: `0x1000000000000000000000000000000000000020`.
- Fresh V2 implementation: `0x1000000000000000000000000000000000000021`.

The native transition leaves retiring code, storage, balances, pending exits and
deadlines untouched. Its withdrawal methods continue on chain 1043. The UI
offers withdrawals only there. Users claim to their wallet and make a separate
V2 deposit with a fresh bonding period. No principal or shares are copied.

The reader keeps its historical `clockSource()` identity to preserve old exits.
Its appended active-source and cutover-epoch words route future rewards to V2.
Credential receipts, confirmed delegation and historical reward weights remain;
unused old delegation signatures are invalidated by EIP-712 domain version 2.
New staking inherits the retiring DAO's account restrictions.

V2 is owned by StakingTimelock, controlled through the existing Governor. Normal
DAO voting, queue and execution stay unchanged. There is no extra multisig
approval. Separately required human-controlled production owner/admin wallets
must be 3-of-5; production signer identities are assigned in Mainnet preparation.

The independent `SeparateStakingV1` definition uses bit `0x40`, one 2,000-layer
window, a 1,500-layer threshold and 2,000-layer activation delay. A sealed policy
also requires at least 24 hours notice. Installation waits for both native and
time gates, then the first new reward epoch. Existing 30-minute epochs and the
two-second target, historical definitions and genesis identities are retained.
The schedule is unset in an unsealed development build.

The retiring Testnet proxy already implements V2-compatible interfaces. This
binding is not a certified adapter for genuine Mainnet V1. Mainnet V1 authority,
liability reconciliation, protection/adapter, weekly policy, restrictions and
production signing custody need separate qualification before chain 1404.

Tests include Solidity storage/withdrawal/source boundaries, native atomic
installation and reward routing, full isolated native signalling/restart and
an explicitly authenticated snapshot preflight. Opt-in historical replay and
physical/peer/operator acceptance are distinct evidence; none may be inferred
from a passing unit test. Do not weaken checkpoint validation or reset data.
