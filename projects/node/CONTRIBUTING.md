# Contributing to corechain

Discuss consensus-affecting changes before implementation. Keep each pull
request narrow, explain its user-visible and consensus effects, add focused
tests, and document any API compatibility or data-migration impact.

All changes require review and passing tests. Consensus, networking, storage,
mining-template, block-submission, cryptography, authentication, release, and
migration changes require an independent domain-owner review. Never replace a
failing invariant with a weaker assertion. Reproductions must use disposable
data unless canonical chain data is explicitly protected and read-only.

Public interfaces use semantic API majors. Breaking changes require a new major
and a supported overlap with the previous major. Published artifacts bind the
exact commit, component version, platform, byte size, and SHA-256 digest.

Contributions to BlockDAG-owned material are accepted under the project’s
GPL-3.0-or-later default when the contributor has the right to submit them.
Include existing copyright and licence notices for copied material, identify
the upstream source when known, and do not remove or replace inherited terms.
This project does not require copyright assignment; contributors retain their
rights subject to the licence grant for their contribution.
