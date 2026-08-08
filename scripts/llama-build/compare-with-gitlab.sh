#!/usr/bin/env bash
# Compare a local tarball with the GitLab package (layout + Ubuntu 22.04 runtime).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
source "${ROOT}/common.sh"

ARCH="${1:?arch: x64 or arm64}"
TAG="$(llama_build_tag "${2:-}")"
OUT="${LLAMA_BUILD_OUT_DIR}"

case "${ARCH}" in
  x64)
    PLATFORM=linux/amd64
    TAR="llama-${TAG}-bin-ubuntu-cuda-x64.tar.gz"
  ;;
  arm64)
    PLATFORM=linux/arm64
    TAR="llama-${TAG}-bin-ubuntu-cuda-arm64.tar.gz"
  ;;
  *)
    echo "usage: $0 x64|arm64 [tag]" >&2
    exit 1
  ;;
esac

NEW="${OUT}/${TAR}"
REF="/tmp/llama-ref-${TAR}"

if [ ! -f "${NEW}" ]; then
  echo "missing local build: ${NEW}" >&2
  exit 1
fi

unset https_proxy http_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY all_proxy 2>/dev/null || true
curl -fsSL -o "${REF}" \
  "${LLAMA_BUILD_GITLAB_API}/projects/${LLAMA_BUILD_GITLAB_PROJECT_ID}/packages/generic/llama-cpp/${TAG}/${TAR}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT
mkdir -p "${WORKDIR}/ref" "${WORKDIR}/new"

tar -xzf "${REF}" -C "${WORKDIR}/ref"
tar -xzf "${NEW}" -C "${WORKDIR}/new"

echo "=== layout diff (ref vs new) ==="
diff -u \
  <(cd "${WORKDIR}/ref" && find . \( -type f -o -type l \) | sed 's|^\./||' | LC_ALL=C sort) \
  <(cd "${WORKDIR}/new" && find . \( -type f -o -type l \) | sed 's|^\./||' | LC_ALL=C sort) || true

llama_build_ensure_docker
llama_build_ensure_cuda_image "${PLATFORM}"

echo "=== ref version (Ubuntu 22.04) ==="
docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${REF}:/pkg.tar.gz:ro" \
  "${LLAMA_BUILD_CUDA_IMAGE}" bash -lc \
  'mkdir -p /tmp/p && tar -xzf /pkg.tar.gz -C /tmp/p
    server=$(find /tmp/p -type f -path "*/bin/llama-server" -print -quit)
    r=${server%/bin/llama-server}
    LD_LIBRARY_PATH=$r/lib $r/bin/llama-server --version' || true

echo "=== new version (Ubuntu 22.04) ==="
docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${NEW}:/pkg.tar.gz:ro" \
  "${LLAMA_BUILD_CUDA_IMAGE}" bash -lc \
  'mkdir -p /tmp/p && tar -xzf /pkg.tar.gz -C /tmp/p
    server=$(find /tmp/p -type f -path "*/bin/llama-server" -print -quit)
    r=${server%/bin/llama-server}
    LD_LIBRARY_PATH=$r/lib $r/bin/llama-server --version'

echo "=== sha256 ==="
shasum -a 256 "${REF}" "${NEW}"
