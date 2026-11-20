#!/bin/sh
# Build transport binaries against the same glibc generation as our containers.
# The caller supplies a clean checkout and an output directory outside it.
set -eu

source_dir="$(cd "${1:?clean source directory required}" && pwd)"
mkdir -p "${2:?output directory required}"
output_dir="$(cd "$2" && pwd)"
case "$output_dir/" in
    "$source_dir/"*) echo 'candidate output must be outside the source checkout' >&2; exit 1 ;;
esac
candidate_sha="$(git -C "$source_dir" rev-parse HEAD)"
test "$candidate_sha" = "${CANDIDATE_SHA:-$candidate_sha}"
test -z "$(git -C "$source_dir" status --porcelain)"
git_dir="$(git -C "$source_dir" rev-parse --path-format=absolute --git-common-dir)"
builder_image='golang@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c'

build_container="bdag-candidate-build-$$"
trap 'docker rm -f "$build_container" >/dev/null 2>&1 || true' EXIT
docker create --name "$build_container" --platform linux/amd64 --init \
    --cap-drop ALL --security-opt no-new-privileges:true \
    --cpus "${CANDIDATE_BUILD_CPUS:-2}" --memory "${CANDIDATE_BUILD_MEMORY:-4g}" \
    --pids-limit 768 \
    --volume "$source_dir:$source_dir:ro" --volume "$git_dir:$git_dir:ro" \
    --volume "${CANDIDATE_MODULE_CACHE:-bdag-candidate-go1269-modules}:/go/pkg/mod" \
    --volume "${CANDIDATE_BUILD_CACHE:-bdag-candidate-go1269-build}:/root/.cache/go-build" \
    --workdir "$source_dir" \
    --env CANDIDATE_SHA="$candidate_sha" --env BUILDER_IMAGE="$builder_image" \
    --env GOTOOLCHAIN=local --env CGO_ENABLED=1 --env GOOS=linux --env GOARCH=amd64 \
    --env GIT_CONFIG_COUNT=1 --env GIT_CONFIG_KEY_0=safe.directory \
    --env GIT_CONFIG_VALUE_0="$source_dir" \
    "$builder_image" sh -ec '
        mkdir -p /out
        test "$(git rev-parse HEAD)" = "$CANDIDATE_SHA"
        test -z "$(git status --porcelain)"
        go mod verify
        release_tag="dev-$(printf %s "$CANDIDATE_SHA" | cut -c1-12)"
        for binary in bdag nodeworker bdag-cpuminer; do
            go build -p 2 -mod=readonly -trimpath -buildvcs=true \
                -ldflags "-X github.com/BlockdagNetworkLabs/bdag/version.ReleaseTag=$release_tag" \
                -o "/out/$binary" "./cmd/$binary"
            go version -m "/out/$binary" | grep -F "vcs.revision=$CANDIDATE_SHA"
            go version -m "/out/$binary" | grep -F "vcs.modified=false"
        done
        go version > /out/GO_VERSION
        printf "%s\n" "$BUILDER_IMAGE" > /out/BUILDER_IMAGE
        printf "%s\n" "$CANDIDATE_SHA" > /out/SOURCE_COMMIT
        (cd /out && sha256sum bdag nodeworker bdag-cpuminer > SHA256SUMS)
        test -z "$(git status --porcelain)"
    ' >/dev/null
docker start --attach "$build_container"
test "$(docker inspect --format '{{.State.ExitCode}}' "$build_container")" -eq 0
# Avoid writing as container root into a runner-owned bind mount. docker cp
# creates the finished artifacts as the invoking host user, without extra caps.
docker cp "$build_container:/out/." "$output_dir/"
