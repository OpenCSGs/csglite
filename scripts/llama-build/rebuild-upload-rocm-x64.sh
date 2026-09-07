#!/usr/bin/env bash
# Rebuild the Ubuntu 22.04 ROCm x64 tarball and upload it to GitLab generic packages.
#
# Set LLAMA_BUILD_SKIP_UPLOAD=1 to build and verify without publishing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
source "${ROOT}/common.sh"

TAG="$(llama_build_tag "${1:-}")"
ROCM_SERIES="${LLAMA_BUILD_ROCM_SERIES}"
PLATFORM=linux/amd64
SRC="${LLAMA_BUILD_WORK_DIR}/src-rocm-amd64/llama.cpp"
OUT="${LLAMA_BUILD_OUT_DIR}"
TAR="llama-${TAG}-bin-ubuntu-rocm-${ROCM_SERIES}-x64.tar.gz"

llama_build_ensure_docker
llama_build_warn_emulated_amd64 "${PLATFORM}"
llama_build_ensure_rocm_image "${PLATFORM}"
llama_build_ensure_image "${LLAMA_BUILD_UBUNTU_IMAGE}" "${PLATFORM}"
llama_build_clone_source "${SRC}" "${TAG}"

rm -rf "${SRC}/build" "${OUT}/stage" "${OUT}/${TAR}"
mkdir -p "${OUT}"

docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${SRC}:/work/llama.cpp" \
  -v "${ROOT}/build-ubuntu22-rocm-x64.sh:/build.sh:ro" \
  -v "${OUT}:/out" \
  -e WORKDIR=/work \
  -e OUTDIR=/out \
  -e LLAMA_TAG="${TAG}" \
  -e ROCM_SERIES="${ROCM_SERIES}" \
  ${GPU_TARGETS:+-e GPU_TARGETS="${GPU_TARGETS}"} \
  "${LLAMA_BUILD_ROCM_IMAGE}" \
  bash /build.sh

# Regression guard for the failure this package exists to avoid: a plain 22.04
# userland must be able to load every packaged object. libamdhip64/librocblas
# are legitimately absent here; a GLIBC/GLIBCXX version error is not.
echo "=== verify glibc baseline on plain ${LLAMA_BUILD_UBUNTU_IMAGE} (${PLATFORM}) ==="
docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${OUT}/${TAR}:/pkg.tar.gz:ro" \
  "${LLAMA_BUILD_UBUNTU_IMAGE}" bash -lc '
    set -eu
    mkdir -p /tmp/p && tar -xzf /pkg.tar.gz -C /tmp/p
    bad=0
    for f in /tmp/p/bin/llama-server /tmp/p/lib/*.so*; do
      [ -f "$f" ] || continue
      out=$(LD_LIBRARY_PATH=/tmp/p/lib ldd "$f" 2>&1 || true)
      if printf "%s\n" "$out" | grep -q "version .GLIBC\|version .GLIBCXX"; then
        echo "GLIBC/GLIBCXX too new in $(basename "$f"):"
        printf "%s\n" "$out" | grep "version .GLIBC\|version .GLIBCXX"
        bad=1
      fi
    done
    [ "$bad" -eq 0 ] || exit 1
    echo "OK: no GLIBC/GLIBCXX version errors on 22.04"
  '

echo "=== verify llama-server --version in ${LLAMA_BUILD_ROCM_IMAGE} (${PLATFORM}) ==="
docker run --platform "${PLATFORM}" --pull=never --rm \
  -v "${OUT}/${TAR}:/pkg.tar.gz:ro" \
  "${LLAMA_BUILD_ROCM_IMAGE}" bash -lc \
  'mkdir -p /tmp/p && tar -xzf /pkg.tar.gz -C /tmp/p
   LD_LIBRARY_PATH=/tmp/p/lib:/opt/rocm/lib /tmp/p/bin/llama-server --version' || \
  echo "WARN: --version exited non-zero; no /dev/kfd in this container. Validate on an AMD GPU host."

if [ "${LLAMA_BUILD_SKIP_UPLOAD:-0}" = "1" ]; then
  echo "LLAMA_BUILD_SKIP_UPLOAD=1, not uploading. Artifact: ${OUT}/${TAR}"
  shasum -a 256 "${OUT}/${TAR}"
  exit 0
fi

llama_build_upload_tarball "${TAG}" "${OUT}/${TAR}"
