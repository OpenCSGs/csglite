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
: "${CSGHUB_LITE_VERSION:=dev}"
: "${DOCKERFILE:=docker/sglang/Dockerfile}"

build_root="${CSGHUB_LITE_BUILD_ROOT:-${HOME}/.csghub-lite/tmp/sglang-build}"
mkdir -p "${build_root}"
build_dir="$(mktemp -d "${build_root}/build.XXXXXX")"
trap 'rm -rf "${build_dir}"' EXIT
cd "${repo_root}"
# Embed the repository's checked-in production UI, just like a plain Go build.
CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath \
    -ldflags "-s -w -X main.version=${CSGHUB_LITE_VERSION}" \
    -o "${build_dir}/csghub-lite" ./cmd/csghub-lite
docker build --platform "${platform}" \
    --build-context "csglite-binary=${build_dir}" \
    --build-arg "BASE_IMAGE=${BASE_IMAGE}" \
    --build-arg "CSGHUB_LITE_VERSION=${CSGHUB_LITE_VERSION}" \
    -f "${DOCKERFILE}" -t "${IMAGE_TAG}" .
