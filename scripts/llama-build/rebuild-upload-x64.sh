#!/usr/bin/env bash
# Rebuild Ubuntu 22.04 CUDA x64 tarball and upload to GitLab generic packages.
#
# Set LLAMA_BUILD_SKIP_UPLOAD=1 to build and verify without publishing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
source "${ROOT}/common.sh"

TAG="$(llama_build_tag "${1:-}")"
PLATFORM=linux/amd64
SRC="${LLAMA_BUILD_WORK_DIR}/src-amd64/llama.cpp"
OUT="${LLAMA_BUILD_OUT_DIR}"
TAR="llama-${TAG}-bin-ubuntu-cuda-x64.tar.gz"

llama_build_ensure_docker
llama_build_ensure_cuda_image "${PLATFORM}"
llama_build_clone_source "${SRC}" "${TAG}"

rm -rf "${SRC}/build" "${OUT}/stage" "${OUT}/${TAR}"
mkdir -p "${OUT}"

docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${SRC}:/work/llama.cpp" \
  -v "${ROOT}/build-ubuntu22-cuda-x64.sh:/build.sh:ro" \
  -v "${OUT}:/out" \
  -e WORKDIR=/work \
  -e OUTDIR=/out \
  -e LLAMA_TAG="${TAG}" \
  "${LLAMA_BUILD_CUDA_IMAGE}" \
  bash /build.sh

echo "=== verify on Ubuntu 22.04 (${PLATFORM}) ==="
docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${OUT}/${TAR}:/pkg.tar.gz:ro" \
  "${LLAMA_BUILD_CUDA_IMAGE}" bash -lc \
  'mkdir -p /tmp/p && tar -xzf /pkg.tar.gz -C /tmp/p && LD_LIBRARY_PATH=/tmp/p/lib /tmp/p/bin/llama-server --version'

if [ "${LLAMA_BUILD_SKIP_UPLOAD:-0}" = "1" ]; then
  echo "LLAMA_BUILD_SKIP_UPLOAD=1, not uploading. Artifact: ${OUT}/${TAR}"
  shasum -a 256 "${OUT}/${TAR}"
  exit 0
fi

llama_build_upload_tarball "${TAG}" "${OUT}/${TAR}"
