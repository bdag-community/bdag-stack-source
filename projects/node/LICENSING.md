# Licensing and provenance

The adopted project policy is GPL-3.0-or-later for the team's original work.
This does not relabel copied, vendored, generated, or
third-party material. File headers and the terms in `LICENSES/` and
`THIRD_PARTY_NOTICES` remain controlling for those components.

The combined node includes go-ethereum command code through
`cmd/bdag/commands.go` -> `bevm/cmd/cmds.go` -> `deps/go-ethereum/cmd/utils`.
Those files are GPL-3.0-or-later. Geth library material, including the copied
RLP and common packages, is LGPL-3.0-or-later. The full texts are preserved in
`LICENSES/GPL-3.0-or-later.txt` and `LICENSES/LGPL-3.0-or-later.txt`; `COPYING`
is the exact GPL text from `deps/go-ethereum/COPYING`.

This is a preparation branch. A separate snapshot tool may exclude Git history,
tracked binaries, chain data, generated executables, and release archives from
a source scan; those exclusions are listed in `licensing/provenance.json` and
do not clear the excluded material. Separate provenance and distribution
treatment is still required.

The historical inventory remains a record of items requiring review; it is
not a blanket clearance for unreviewed history or binary artifacts. The unused
`UNLICENSED` token example and its generated binding have been removed from this
candidate. The embedded CryptoNight fork now uses independently authored,
MIT-licensed sphlib implementations of JH and Grøstl instead of the earlier
Monero-derived ports. See `deps/cryptonight/NOTICE-BDAG` and the licenses and
copyright notices next to those sources. The replacement uses cgo and requires
a C compiler. The team confirmed ownership of its own original contributions.

See [`COPYING`](COPYING), [`LICENSES/`](LICENSES/),
[`THIRD_PARTY_NOTICES`](THIRD_PARTY_NOTICES),
[`REUSE.toml`](REUSE.toml), and [`licensing/provenance.json`](licensing/provenance.json).
