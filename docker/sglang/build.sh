#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
platform="${PLATFORM:-linux/amd64}"
case "${platform}" in
    linux/amd64) arch=amd64 ;;
    linux/arm64) arch=arm64 ;;
    *) echo "Supported PLATFORM values: linux/amd64, linux/arm64" >&2; exit 64 ;;
esac
: "${BASE_IMAGE:=lmsysorg/sglang:v0.5.19-cu129-runtime}"
: "${IMAGE_TAG:=csglite-sglang:dev-cu129}"
: "${CSGHUB_LITE_VERSION:=}"
: "${DOCKERFILE:=docker/sglang/Dockerfile.cuda}"

build_root="${CSGHUB_LITE_BUILD_ROOT:-${HOME}/.csghub-lite/tmp/sglang-build}"
mkdir -p "${build_root}"
build_dir="$(mktemp -d "${build_root}/build.XXXXXX")"
trap 'rm -rf "${build_dir}"' EXIT
cd "${repo_root}"
# The production UI is gitignored. A source archive therefore contains no
# index.html/assets, and a plain Go build would silently embed an empty UI.
# Require the built+synced tree and tell the caller exactly how to produce it.
static_dir="internal/server/static"
if [ ! -f "${static_dir}/index.html" ] || [ -z "$(find "${static_dir}/assets" -type f -print -quit 2>/dev/null || true)" ]; then
    echo "Production UI is missing from ${static_dir}. Run 'make build-web' first (it builds web/dist and syncs it into internal/server/static), then re-run this script." >&2
    exit 66
fi
# Startup compares bundled/cached/public versions to keep an old public latest
# from replacing native SGLang support. An unversioned "dev" build cannot take
# part in that comparison, so never silently produce such a container.
build_version="${CSGHUB_LITE_VERSION#v}"
if [[ ! "${build_version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
    echo "Set CSGHUB_LITE_VERSION to a semantic build version (for example 1.0.6-sglang.1); startup latest protection requires a comparable bundled version." >&2
    exit 64
fi
version_without_metadata="${build_version%%+*}"
if [[ "${version_without_metadata}" == *-* ]]; then
    IFS=. read -r -a prerelease_identifiers <<< "${version_without_metadata#*-}"
    for identifier in "${prerelease_identifiers[@]}"; do
        if [[ "${identifier}" =~ ^[0-9]+$ && "${#identifier}" -gt 1 && "${identifier}" == 0* ]]; then
            echo "CSGHUB_LITE_VERSION must be a semantic build version; numeric prerelease identifiers cannot have leading zeros." >&2
            exit 64
        fi
    done
fi
CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath \
    -ldflags "-s -w -X main.version=${build_version}" \
    -o "${build_dir}/csghub-lite" ./cmd/csghub-lite
docker build --platform "${platform}" \
    --build-context "csglite-binary=${build_dir}" \
    --build-arg "BASE_IMAGE=${BASE_IMAGE}" \
    --build-arg "CSGHUB_LITE_VERSION=${CSGHUB_LITE_VERSION}" \
    -f "${DOCKERFILE}" -t "${IMAGE_TAG}" .
