#!/usr/bin/env bash
# Thin `cast` (Foundry) wrapper for owner operations on BlocklistGovernance.sol
# and SelectiveBlocklist.sol. See ../src/*.sol for contract semantics and
# ../../params/blocklist.go for the pinned mainnet addresses used as defaults.
#
# Required env:
#   BLOCKLIST_RPC_URL       - JSON-RPC endpoint
#   BLOCKLIST_SIGNER_ACCOUNT - Foundry account reference (only needed for writes)
#
# Optional env (override the mainnet defaults, e.g. to target a testnet deploy):
#   BLOCKLIST_CONTRACT_ADDR
#   SELECTIVE_BLOCKLIST_CONTRACT_ADDR
#
# Usage:
#   blocklist-governance.sh blocklist   block            <address>
#   blocklist-governance.sh blocklist   unblock          <address>
#   blocklist-governance.sh blocklist   block-batch      (-f|--file <path>)
#   blocklist-governance.sh blocklist   unblock-batch    (-f|--file <path>)
#   blocklist-governance.sh blocklist   transfer-ownership <newOwner>
#   blocklist-governance.sh blocklist   is-blocked       <address>
#   blocklist-governance.sh blocklist   owner
#
#   blocklist-governance.sh selective   restrict         <address>
#   blocklist-governance.sh selective   unrestrict       <address>
#   blocklist-governance.sh selective   restrict-batch   (-f|--file <path>)
#   blocklist-governance.sh selective   unrestrict-batch (-f|--file <path>)
#   blocklist-governance.sh selective   allow            <from> <to>
#   blocklist-governance.sh selective   disallow         <from> <to>
#   blocklist-governance.sh selective   allow-batch      <from> (-f|--file <path>)
#   blocklist-governance.sh selective   disallow-batch   <from> (-f|--file <path>)
#   blocklist-governance.sh selective   transfer-ownership <newOwner>
#   blocklist-governance.sh selective   is-restricted    <address>
#   blocklist-governance.sh selective   is-allowed       <from> <to>
#   blocklist-governance.sh selective   owner
#
# --file/-f points at a JSON file holding either a bare array of addresses
# (["0x..","0x.."]) or an object {"addresses": ["0x..","0x.."]}.

set -euo pipefail

BLOCKLIST_CONTRACT_ADDR="${BLOCKLIST_CONTRACT_ADDR:-0xe628505d5cB6F5F4f9a25Cd5c80Caa198cc645a0}"
SELECTIVE_BLOCKLIST_CONTRACT_ADDR="${SELECTIVE_BLOCKLIST_CONTRACT_ADDR:-0xc0a42A43A05e39C3a4165CA98440D516959f41D9}"

usage() {
    sed -n '2,42p' "$0"
    exit 1
}

require_rpc() {
    : "${BLOCKLIST_RPC_URL:?BLOCKLIST_RPC_URL must be set}"
}

require_key() {
	if [[ ${BLOCKLIST_PRIVATE_KEY+x} == x ]]; then
		echo "BLOCKLIST_PRIVATE_KEY is unsupported; use BLOCKLIST_SIGNER_ACCOUNT" >&2
		return 1
	fi
	require_rpc
	: "${BLOCKLIST_SIGNER_ACCOUNT:?BLOCKLIST_SIGNER_ACCOUNT must be set}"
	if ! cast send --help 2>&1 | grep -q -- '--account'; then
		echo "installed cast does not support account-based signing" >&2
		return 1
	fi
	if ! cast wallet address --help 2>&1 | grep -q -- '--account'; then
		echo "installed cast cannot resolve account references" >&2
		return 1
	fi
	if ! cast wallet address --account "$BLOCKLIST_SIGNER_ACCOUNT" >/dev/null 2>&1; then
		echo "configured signer account is unavailable" >&2
		return 1
	fi
}

# Reads --file/-f out of "$@", leaving remaining positional args in FILE_REMAINING_ARGS
# and the path (or empty) in FILE_PATH.
parse_file_flag() {
    FILE_PATH=""
    FILE_REMAINING_ARGS=()
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f|--file)
                FILE_PATH="${2:?missing path after $1}"
                shift 2
                ;;
            *)
                FILE_REMAINING_ARGS+=("$1")
                shift
                ;;
        esac
    done
}

addresses_from_file() {
    local path="$1"
    jq -er 'if type == "array" and all(.[]; type == "string") then .[] elif type == "object" and (.addresses | type == "array") and all(.addresses[]; type == "string") then .addresses[] else error("expected an address array") end' "$path"
}

send() {
	local addr="$1"; shift
	ETH_RPC_URL="$BLOCKLIST_RPC_URL" cast send "$addr" --account "$BLOCKLIST_SIGNER_ACCOUNT" "$@"
}

call() {
	local addr="$1"; shift
	ETH_RPC_URL="$BLOCKLIST_RPC_URL" cast call "$addr" "$@"
}

cmd_blocklist() {
    local action="${1:?missing action}"; shift || true
    local addr="$BLOCKLIST_CONTRACT_ADDR"

    case "$action" in
        block)
            require_key
            send "$addr" "setBlocked(address,bool)" "${1:?missing address}" true
            ;;
        unblock)
            require_key
            send "$addr" "setBlocked(address,bool)" "${1:?missing address}" false
            ;;
        block-batch|unblock-batch)
            require_key
            parse_file_flag "$@"
            : "${FILE_PATH:?block-batch/unblock-batch requires -f/--file <path>}"
            local flag=true
            [[ "$action" == "unblock-batch" ]] && flag=false
            local addresses
            addresses="$(addresses_from_file "$FILE_PATH")" || { echo "invalid address batch" >&2; return 1; }
            [[ -n "$addresses" ]] || { echo "address batch is empty" >&2; return 1; }
            mapfile -t addrs <<< "$addresses"
            send "$addr" "setBlockedBatch(address[],bool)" "[$(IFS=,; printf '%s' "${addrs[*]}")]" "$flag"
            ;;
        transfer-ownership)
            require_key
            send "$addr" "transferOwnership(address)" "${1:?missing new owner address}"
            ;;
        is-blocked)
            require_rpc
            call "$addr" "isBlocked(address)(bool)" "${1:?missing address}"
            ;;
        owner)
            require_rpc
            call "$addr" "owner()(address)"
            ;;
        *)
            echo "unknown blocklist action: $action" >&2
            usage
            ;;
    esac
}

cmd_selective() {
    local action="${1:?missing action}"; shift || true
    local addr="$SELECTIVE_BLOCKLIST_CONTRACT_ADDR"

    case "$action" in
        restrict)
            require_key
            send "$addr" "setRestricted(address,bool)" "${1:?missing address}" true
            ;;
        unrestrict)
            require_key
            send "$addr" "setRestricted(address,bool)" "${1:?missing address}" false
            ;;
        restrict-batch|unrestrict-batch)
            require_key
            parse_file_flag "$@"
            : "${FILE_PATH:?restrict-batch/unrestrict-batch requires -f/--file <path>}"
            local flag=true
            [[ "$action" == "unrestrict-batch" ]] && flag=false
            local addresses
            addresses="$(addresses_from_file "$FILE_PATH")" || { echo "invalid address batch" >&2; return 1; }
            [[ -n "$addresses" ]] || { echo "address batch is empty" >&2; return 1; }
            mapfile -t addrs <<< "$addresses"
            send "$addr" "setRestrictedBatch(address[],bool)" "[$(IFS=,; printf '%s' "${addrs[*]}")]" "$flag"
            ;;
        allow)
            require_key
            send "$addr" "setAllowedTo(address,address,bool)" "${1:?missing from}" "${2:?missing to}" true
            ;;
        disallow)
            require_key
            send "$addr" "setAllowedTo(address,address,bool)" "${1:?missing from}" "${2:?missing to}" false
            ;;
        allow-batch|disallow-batch)
            require_key
            local from="${1:?missing from address}"; shift
            parse_file_flag "$@"
            : "${FILE_PATH:?allow-batch/disallow-batch requires -f/--file <path>}"
            local flag=true
            [[ "$action" == "disallow-batch" ]] && flag=false
            local addresses
            addresses="$(addresses_from_file "$FILE_PATH")" || { echo "invalid address batch" >&2; return 1; }
            [[ -n "$addresses" ]] || { echo "address batch is empty" >&2; return 1; }
            mapfile -t tos <<< "$addresses"
            send "$addr" "setAllowedToBatch(address,address[],bool)" "$from" "[$(IFS=,; printf '%s' "${tos[*]}")]" "$flag"
            ;;
        transfer-ownership)
            require_key
            send "$addr" "transferOwnership(address)" "${1:?missing new owner address}"
            ;;
        is-restricted)
            require_rpc
            call "$addr" "isRestricted(address)(bool)" "${1:?missing address}"
            ;;
        is-allowed)
            require_rpc
            call "$addr" "isAllowedTo(address,address)(bool)" "${1:?missing from}" "${2:?missing to}"
            ;;
        owner)
            require_rpc
            call "$addr" "owner()(address)"
            ;;
        *)
            echo "unknown selective action: $action" >&2
            usage
            ;;
    esac
}

main() {
    [[ $# -ge 2 ]] || usage
    local contract="$1"; shift

    case "$contract" in
        blocklist) cmd_blocklist "$@" ;;
        selective) cmd_selective "$@" ;;
        *)
            echo "unknown contract: $contract (expected 'blocklist' or 'selective')" >&2
            usage
            ;;
    esac
}

main "$@"
