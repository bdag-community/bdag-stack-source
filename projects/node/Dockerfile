# =========================
# Build stage (CGO + glibc)
# =========================
FROM golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS build

# Enable CGO (needed for scrypt C code)
ENV CGO_ENABLED=1

WORKDIR /bdag

# Install required packages for building
RUN apt-get update && apt-get install -y --no-install-recommends \
    make git gcc g++ libc6-dev

# Stage module metadata first to leverage Docker layer caching for dependencies.
# Copy root module files and the local go-ethereum replacement's module metadata.
COPY go.mod go.sum ./
RUN mkdir -p deps/go-ethereum && touch deps/go-ethereum/go.mod
COPY deps/go-ethereum/go.mod deps/go-ethereum/go.sum deps/go-ethereum/
# Download all dependencies (cached until go.mod/go.sum changes).
RUN go mod download

# Now copy the complete local replacement and remaining source.
COPY deps/ deps/
COPY . .

# Build an explicitly unqualified development candidate. Release artifacts still
# require an exact node-* tag through the Makefile release target.
RUN make dev

# =========================
# Runtime stage
# =========================
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /bdag

# Copy built binaries
COPY --from=build /bdag/build/bin/bdag       /usr/local/bin/bdag
COPY --from=build /bdag/build/bin/nodeworker /usr/local/bin/nodeworker
COPY --from=build /bdag/build/bin/bdag-cpuminer /usr/local/bin/bdag-cpuminer
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Prepare entrypoint
RUN sed -i "s/\r$//" /usr/local/bin/entrypoint.sh \
 && chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/bdag /usr/local/bin/nodeworker /usr/local/bin/bdag-cpuminer

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
