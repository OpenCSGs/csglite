#!/usr/bin/env bash
# Run inside: rocm/dev-ubuntu-22.04:<rocm>-complete (linux/amd64). Keep image locally; do not prune.
#
# Upstream ggml-org builds its ubuntu-rocm assets on Ubuntu 24.04, so those
# tarballs need GLIBC_2.38 / GLIBCXX_3.4.32 and cannot run on 22.04 hosts such
# as docker/rocm/Dockerfile's ROCm image. This script produces the 22.04
# equivalent that scripts/install.sh expects at
# llama-<tag>-bin-ubuntu-rocm-<series>-x64.tar.gz.
set -euo pipefail

TAG="${LLAMA_TAG:-b10830}"
ROCM_SERIES="${ROCM_SERIES:-7.2}"
WORKDIR="${WORKDIR:-/work}"
OUTDIR="${OUTDIR:-/out}"
ROCM_PATH="${ROCM_PATH:-/opt/rocm}"
# Default gfx list matches the previously mirrored ROCm package
# (b9158, rocm-7.2). Override with GPU_TARGETS for a smaller/faster build.
GPU_TARGETS="${GPU_TARGETS:-gfx908;gfx942;gfx1030;gfx1100;gfx1101;gfx1102;gfx1150;gfx1151;gfx1200;gfx1201}"
# CMake >= 3.21 is required for the HIP language. Stay on 3.x here: CMake 4
# removed compatibility with cmake_minimum_required(<3.5), which some ROCm
# CMake config packages still declare.
CMAKE_VERSION="${CMAKE_VERSION:-3.31.6}"

export DEBIAN_FRONTEND=noninteractive
export PATH="${ROCM_PATH}/bin:${PATH}"

apt-get update -qq
apt-get install -y -qq \
  build-essential ninja-build libgomp1 git libssl-dev libcurl4-openssl-dev wget ca-certificates file

mkdir -p /tmp/cmake-install
cd /tmp/cmake-install
wget -q -O cmake.sh "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}-linux-x86_64.sh"
sh cmake.sh --prefix=/usr/local --skip-license
cd /
rm -rf /tmp/cmake-install

mkdir -p "${WORKDIR}"
cd "${WORKDIR}/llama.cpp"
if [ ! -d .git ]; then
  git clone --depth 1 --branch "${TAG}" https://github.com/ggml-org/llama.cpp.git .
else
  git fetch --depth 1 origin "refs/tags/${TAG}" 2>/dev/null || true
  git checkout "${TAG}" 2>/dev/null || true
fi

echo "=== ROCm toolchain ==="
hipconfig --version
echo "GPU_TARGETS=${GPU_TARGETS}"

cmake -S . -B build -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_RPATH='$ORIGIN' \
  -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON \
  -DCMAKE_PREFIX_PATH="${ROCM_PATH}" \
  -DCMAKE_HIP_COMPILER="$(hipconfig -l)/clang" \
  -DBUILD_SHARED_LIBS=ON \
  -DGGML_NATIVE=OFF \
  -DGGML_CPU_ALL_VARIANTS=ON \
  -DGGML_HIP=ON \
  -DHIP_PLATFORM=amd \
  -DGPU_TARGETS="${GPU_TARGETS}" \
  -DGGML_BACKEND_DL=ON \
  -DGGML_BACKEND_DIR:STRING=lib

cmake --build build --config Release -j "$(nproc)"

rm -rf "${OUTDIR}/stage"
mkdir -p "${OUTDIR}/stage/bin" "${OUTDIR}/stage/lib"
cp -a build/bin/llama-server "${OUTDIR}/stage/bin/"
shopt -s nullglob
for lib in build/bin/lib*.so*; do
  cp -a "${lib}" "${OUTDIR}/stage/lib/"
done
shopt -u nullglob

test -f "${OUTDIR}/stage/lib/libggml-hip.so"
file "${OUTDIR}/stage/bin/llama-server" "${OUTDIR}/stage/lib/libggml-hip.so"

# Hard check: every shared-library dependency must resolve. This is the part
# that catches a wrong-OS or wrong-ROCm build.
echo "=== ldd (must not report 'not found') ==="
missing=0
for target in "${OUTDIR}/stage/bin/llama-server" "${OUTDIR}"/stage/lib/*.so*; do
  [ -f "${target}" ] || continue
  if LD_LIBRARY_PATH="${OUTDIR}/stage/lib:${ROCM_PATH}/lib:${LD_LIBRARY_PATH:-}" \
      ldd "${target}" 2>/dev/null | grep -q 'not found'; then
    echo "MISSING deps in $(basename "${target}"):"
    LD_LIBRARY_PATH="${OUTDIR}/stage/lib:${ROCM_PATH}/lib:${LD_LIBRARY_PATH:-}" \
      ldd "${target}" | grep 'not found'
    missing=1
  fi
done
[ "${missing}" -eq 0 ] || { echo "unresolved shared library dependencies" >&2; exit 1; }

# Informational only: the build container has no /dev/kfd, so the HIP backend
# cannot initialise here. Final GPU validation must happen on an AMD host.
echo "=== llama-server --version (no GPU in build container) ==="
LD_LIBRARY_PATH="${OUTDIR}/stage/lib:${ROCM_PATH}/lib:${LD_LIBRARY_PATH:-}" \
  "${OUTDIR}/stage/bin/llama-server" --version || \
  echo "WARN: --version exited non-zero without a GPU; check the ldd output above."

cd "${OUTDIR}/stage"
tar -czf "${OUTDIR}/llama-${TAG}-bin-ubuntu-rocm-${ROCM_SERIES}-x64.tar.gz" bin lib
ls -lh "${OUTDIR}/llama-${TAG}-bin-ubuntu-rocm-${ROCM_SERIES}-x64.tar.gz"
