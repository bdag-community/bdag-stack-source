#!/bin/sh
# The CPU miner has no help flag; an invalid network must exit before RPC/mining.
set -eu
candidate_bin="$(cd "${1:?candidate binary directory required}" && pwd)"
runtime_image='debian@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8'
for binary in bdag nodeworker bdag-cpuminer; do
    test -x "$candidate_bin/$binary"
done
docker run --rm --name "bdag-candidate-runtime-$$" --init \
    --platform linux/amd64 --network none --read-only \
    --cap-drop ALL --security-opt no-new-privileges:true --memory 256m \
    --pids-limit 64 --volume "$candidate_bin:/candidate:ro" \
    "$runtime_image" sh -ec '
        for binary in bdag nodeworker; do
            timeout 20 "/candidate/$binary" --help >/dev/null
        done
        if result="$(RPC_NETWORK=__runtime_probe_invalid__ timeout 20 /candidate/bdag-cpuminer 2>&1)"; then
            echo "CPU miner unexpectedly accepted the invalid probe network" >&2
            exit 1
        else
            status=$?
            test "$status" -eq 1
            printf "%s\n" "$result" | grep -F '\''unsupported miner network "__runtime_probe_invalid__"'\'' >/dev/null
        fi
    '
printf '%s\n' 'All candidate binaries execute on the pinned Debian 12 runtime'
