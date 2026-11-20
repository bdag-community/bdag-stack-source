# Blockdag Network (bdag)

This repository contains the implementation of the Blockdag Network node.

Licensing and source provenance are documented in
[`LICENSING.md`](LICENSING.md). The adopted default for material BlockDAG is
entitled to license is GPL-3.0-or-later; inherited terms remain in force.

## Source snapshot

This repository starts from the release review source at commit
`702ca73b90ebf63acf0121b7ed967e7baeaf0436`. It includes the node,
build tools, vendored dependencies, and test fixtures. Deployment credentials,
runtime data, generated executables, internal review files, and legacy CI
workflows are excluded from this snapshot.

## Makefile Usage

The project uses a `Makefile` to simplify building, dependency management, and cross-compilation.

### Prerequisites

- **Go**: Use the Go 1.26.9 toolchain pinned in `go.mod`.
- **CGO**: This project requires CGO. A C compiler (like `gcc` or `clang`) must be installed on your system.
- **Homebrew** (macOS only): Required for installing cross-compilation toolchains.

### Getting Started

1. **Install Go dependencies**:
   ```bash
   make deps
   ```

2. **Setup cross-compilers** (macOS only, required for `make release`):
   ```bash
   make setup-cross
   ```

### Building

- **Build for current platform**:
  ```bash
  make bdag
  ```
  The binary will be placed in `./build/bin/bdag`.

- **Build all local utilities**:
  ```bash
  make all
  ```
  This builds `bdag` and `nodeworker`.

### Release and Cross-Compilation

To build binaries for macOS (Intel & Apple Silicon), Linux (amd64), and Windows (amd64):

```bash
make release
```

*Note: FreeBSD is not supported for cross-compilation from macOS when CGO is enabled. To build for FreeBSD, it is recommended to build natively on a FreeBSD system.*

This will generate compressed archives (`.tar.gz` and `.zip`) and a checksum file in the root directory.

### Build Options

You can pass variables to `make` to customize the build:

- **Enable ZeroMQ support**:
  ```bash
  make bdag ZMQ=TRUE
  ```
  *(Requires ZeroMQ libraries installed on your system)*

- **Build with Debug symbols**:
  ```bash
  make bdag DEBUG=ON
  ```

### Cleaning Up

To remove all built binaries, object files, and release archives:

```bash
make clean
```

## Development

To build development versions of the binaries for all platforms:

```bash
make dev
```
