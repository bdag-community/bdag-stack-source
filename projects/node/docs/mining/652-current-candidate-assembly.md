# Current Core candidate assembly (#652, #564, #49)

The October10 private qualified candidate d5505b8 at PR182 selectively adapted
upstream2b70d6 (three commits over a2c29ab) onto62aa552. It remained separate from
the newer reader/SDK/HTTP2/log-privacy/fee candidate6ab3bdd. Exact raw and draft
heads were independently verified before this integration. No whole branch merge.

The seven qualified paths apply exactly, without conflict. All other current
private source is preserved. Commit/source diffs and test source inventories
are retained in issue-bound evidence.

| Incoming behavior | Current disposition |
| --- | --- |
| Bounded tip lookup and immutable difficulty metadata | Already in current base; retained |
| Explicit pow_type with legacy numeric current_diff | Integrate exact prior qualified adaptation |
| Nil best-state / absent target / unknown algorithm / unconfigured denominator refusal | Integrate exact prior qualified adaptation |
| Detached persisted historical read without DAG insertion/linking | Integrate exact prior qualified adaptation |
| Existing cached metadata and ordinary loader semantics | Preserve; native cache and ancestry regressions required |
| Upgrade-history reconstruction using detached reader | Integrate; retained vote/header/marker behavior unchanged |
| GBT synchronous worker-lock callback | Required follow-up remains open: current base has no generation callback or derived serialized cache; copying only donor lock hook would introduce an incomplete contract |
| Current reader decoder, RPC recovery/authority, HTTP2 graph, SDK, diagnostic privacy and fee identity | Preserve exact current base |

The GBT generation failure/cache invalidation/lock ownership contract must be
reconstructed together before any callback adaptation. Its absence is not
claimed to retire required source behavior. #652 stays open; this assembly does
not complete the full current-Core integration or original A1-A3 programme.

Qualification must bind this combined source, full affected packages, focused
race tests, current affected miner/RPC/gateway contracts and actual binaries.
Prior draft test results cannot be relabelled onto this new assembly. Protected
review/accepted-source, full tuple/history/currentness/mining/network/security/
quality/release remain separate gates. No denied peer-renewal action.
