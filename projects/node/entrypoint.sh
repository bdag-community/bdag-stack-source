#!/bin/sh
set -e

BIN=/usr/local/bin/bdag               # legacy (from image)
[ -x /opt/bdag/bdag ] && BIN=/opt/bdag/bdag   # persisted upgrade wins

# Validate selected executable exists and is executable
if [ ! -x "$BIN" ]; then
    echo "FATAL: node binary not found or not executable: $BIN" >&2
    echo "Checked paths: /usr/local/bin/bdag, /opt/bdag/bdag" >&2
    exit 1
fi

# Check ELF architecture header matches expected platform
case "$(uname -m)" in
    x86_64|amd64)
        arch_check="ELF 64-bit LSB executable, x86-64";;
    aarch64|arm64)
        arch_check="ELF 64-bit LSB executable, ARM aarch64";;
    *)
        arch_check="";;
esac
if [ -n "$arch_check" ] && file "$BIN" 2>/dev/null | grep -q "$arch_check"; then
    : # architecture matches
elif [ -n "$arch_check" ]; then
    echo "WARNING: node binary architecture may not match host ($(uname -m)): $BIN" >&2
    file "$BIN" 2>/dev/null || true
fi

echo "Using node binary: $BIN"

exec nodeworker \
  --node-binary="$BIN" \
  --node-args="${NODE_ARGS:-}" \
  --rpc-url="${RPC_URL:-}" \
  --rollout-window="${ROLLOUT_WINDOW:-0s}" \
  --persist-root="${PERSIST_ROOT:-/opt/bdag}" \
  --health-min-peers="${HEALTH_MIN_PEERS:-0}" \
