#!/bin/sh
set -eu

if make -s RELEASE_TAG= bdag-tag-check >/dev/null 2>&1; then
    echo "untagged builds unexpectedly passed the build-mode guard" >&2
    exit 1
fi

make -s RELEASE_TAG=dev bdag-tag-check
make -s RELEASE_TAG=node-testnet-candidate bdag-tag-check

echo "build-mode contract verified"
