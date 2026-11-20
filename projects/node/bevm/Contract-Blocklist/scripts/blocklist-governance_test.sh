#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/blocklist-governance.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cast_log="$tmp/cast-argv.log"
send_count="$tmp/send.count"
fake_bin="$tmp/bin"
mkdir -p "$fake_bin"

cat > "$fake_bin/cast" <<'FAKE_CAST'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1-}" == send && "${2-}" == --help ]]; then
    if [[ "${CAST_ACCOUNT_UNSUPPORTED-}" == 1 ]]; then
        printf '%s\n' 'cast send usage without account support'
    else
        printf '%s\n' 'cast send usage --account <account>'
    fi
    exit 0
fi
if [[ "${1-}" == wallet && "${2-}" == address && "${3-}" == --help ]]; then
    if [[ "${CAST_WALLET_ACCOUNT_UNSUPPORTED-}" == 1 ]]; then
        printf '%s\n' 'cast wallet address usage without account support'
    else
        printf '%s\n' 'cast wallet address usage --account <account>'
    fi
    exit 0
fi
printf '%s\n' "$*" >> "$CAST_ARGV_LOG"
if [[ "${1-}" == wallet && "${2-}" == address ]]; then
    if [[ "${CAST_WALLET_UNAVAILABLE-}" == 1 ]]; then
        echo 'signer unavailable' >&2
        exit 92
    fi
    [[ "${3-}" == --account && "${4-}" == owner-alias ]] || exit 93
    exit 0
fi
if [[ "${ETH_RPC_URL-}" != "https://rpc.test.invalid/marker" ]]; then
    echo "unexpected RPC environment" >&2
    exit 91
fi
if [[ "${1-}" == send ]]; then
    printf '%s\n' x >> "$CAST_SEND_COUNT"
    if [[ "${CAST_SEND_UNAVAILABLE-}" == 1 ]]; then
        echo 'signer unavailable' >&2
        exit 92
    fi
fi
exit 0
FAKE_CAST
chmod +x "$fake_bin/cast"

export PATH="$fake_bin:$PATH"
export CAST_ARGV_LOG="$cast_log"
export CAST_SEND_COUNT="$send_count"
export BLOCKLIST_RPC_URL='https://rpc.test.invalid/marker'
export BLOCKLIST_SIGNER_ACCOUNT='owner-alias'
unset BLOCKLIST_PRIVATE_KEY

run_ok() {
    "$script" "$@" >>"$tmp/stdout" 2>>"$tmp/stderr"
}

run_fail() {
    if "$script" "$@" >>"$tmp/stdout" 2>>"$tmp/stderr"; then
        echo "expected failure: $*" >&2
        exit 1
    fi
}

send_count_now() {
    if [[ -f "$send_count" ]]; then
        wc -l < "$send_count"
    else
        printf '0\n'
    fi
}

run_fail_no_send() {
    local before after
    before="$(send_count_now)"
    run_fail "$@"
    after="$(send_count_now)"
    [[ "$after" == "$before" ]] || { echo "rejection invoked cast send: $*" >&2; exit 1; }
}

assert_no_secrets() {
    if grep -E -q -- 'private-key|rpc-url|https://rpc.test.invalid/marker|RAW_SECRET_MARKER' "$cast_log" "$tmp/stdout" "$tmp/stderr"; then
        echo 'secret or forbidden RPC argument leaked' >&2
        exit 1
    fi
}

printf '["0x0000000000000000000000000000000000000001","0x0000000000000000000000000000000000000002"]\n' > "$tmp/addresses.json"
: > "$tmp/stdout"
: > "$tmp/stderr"

# Every mutating command in both contract command families.
run_ok blocklist block 0x0000000000000000000000000000000000000001
run_ok blocklist unblock 0x0000000000000000000000000000000000000001
run_ok blocklist block-batch -f "$tmp/addresses.json"
run_ok blocklist unblock-batch -f "$tmp/addresses.json"
run_ok blocklist transfer-ownership 0x0000000000000000000000000000000000000003
run_ok selective restrict 0x0000000000000000000000000000000000000001
run_ok selective unrestrict 0x0000000000000000000000000000000000000001
run_ok selective restrict-batch -f "$tmp/addresses.json"
run_ok selective unrestrict-batch -f "$tmp/addresses.json"
run_ok selective allow 0x0000000000000000000000000000000000000001 0x0000000000000000000000000000000000000002
run_ok selective disallow 0x0000000000000000000000000000000000000001 0x0000000000000000000000000000000000000002
run_ok selective allow-batch 0x0000000000000000000000000000000000000001 -f "$tmp/addresses.json"
run_ok selective disallow-batch 0x0000000000000000000000000000000000000001 -f "$tmp/addresses.json"
run_ok selective transfer-ownership 0x0000000000000000000000000000000000000003

# Read-only calls do not require an account and remain cast calls.
unset BLOCKLIST_SIGNER_ACCOUNT
run_ok blocklist is-blocked 0x0000000000000000000000000000000000000001
run_ok blocklist owner
run_ok selective is-restricted 0x0000000000000000000000000000000000000001
run_ok selective is-allowed 0x0000000000000000000000000000000000000001 0x0000000000000000000000000000000000000002
run_ok selective owner
export BLOCKLIST_SIGNER_ACCOUNT='owner-alias'

write_count=14
actual_count="$(wc -l < "$send_count")"
[[ "$actual_count" -eq "$write_count" ]] || { echo "send count=$actual_count want=$write_count" >&2; exit 1; }
while IFS= read -r args; do
	[[ "$args" == send\ * ]] || continue
    [[ "$args" == *'--account owner-alias'* ]] || { echo "missing account: $args" >&2; exit 1; }
    [[ "$args" != *'--rpc-url'* && "$args" != *'--private-key'* ]] || { echo "forbidden signing/RPC flag: $args" >&2; exit 1; }
done < "$cast_log"
assert_no_secrets

# Legacy variable presence, including empty, is rejected before cast.
: > "$cast_log"
export BLOCKLIST_PRIVATE_KEY='RAW_SECRET_MARKER'
run_fail_no_send blocklist block 0x0000000000000000000000000000000000000001
[[ ! -s "$cast_log" ]] || { echo 'legacy key reached cast' >&2; exit 1; }
export BLOCKLIST_PRIVATE_KEY=''
run_fail_no_send selective restrict 0x0000000000000000000000000000000000000001
[[ ! -s "$cast_log" ]] || { echo 'empty legacy key reached cast' >&2; exit 1; }
unset BLOCKLIST_PRIVATE_KEY

# Missing account, unsupported cast, malformed batch, and unavailable signer
# all fail closed before a mutating cast send.
unset BLOCKLIST_SIGNER_ACCOUNT
run_fail_no_send blocklist block 0x0000000000000000000000000000000000000001
export BLOCKLIST_SIGNER_ACCOUNT='owner-alias'
export CAST_ACCOUNT_UNSUPPORTED=1
run_fail_no_send selective disallow 0x0000000000000000000000000000000000000001 0x0000000000000000000000000000000000000002
unset CAST_ACCOUNT_UNSUPPORTED
export CAST_WALLET_ACCOUNT_UNSUPPORTED=1
run_fail_no_send selective disallow 0x0000000000000000000000000000000000000001 0x0000000000000000000000000000000000000002
unset CAST_WALLET_ACCOUNT_UNSUPPORTED
export CAST_WALLET_UNAVAILABLE=1
run_fail_no_send selective disallow 0x0000000000000000000000000000000000000001 0x0000000000000000000000000000000000000002
unset CAST_WALLET_UNAVAILABLE
printf '{malformed\n' > "$tmp/bad.json"
run_fail_no_send blocklist block-batch -f "$tmp/bad.json"
run_fail_no_send selective allow-batch 0x0000000000000000000000000000000000000001 -f "$tmp/bad.json"
printf '[]\n' > "$tmp/empty.json"
run_fail_no_send blocklist block-batch -f "$tmp/empty.json"
printf '{"addresses":"wrong-shape"}\n' > "$tmp/wrong.json"
run_fail_no_send selective restrict-batch -f "$tmp/wrong.json"
export CAST_WALLET_UNAVAILABLE=1
run_fail_no_send blocklist unblock 0x0000000000000000000000000000000000000001
unset CAST_WALLET_UNAVAILABLE
export CAST_SEND_UNAVAILABLE=1
run_fail blocklist unblock 0x0000000000000000000000000000000000000001
unset CAST_SEND_UNAVAILABLE

assert_no_secrets
echo 'blocklist governance shell tests passed'
