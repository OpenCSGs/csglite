#!/usr/bin/env bash
# Run inside: nvidia/cuda:12.9.1-devel-ubuntu22.04 (linux/arm64). Keep image locally; do not prune.
set -euo pipefail

TAG="${LLAMA_TAG:-b10549}"
ROOT_NAME="llama-${TAG}-bin-ubuntu-cuda-arm64"
WORKDIR="${WORKDIR:-/work}"
OUTDIR="${OUTDIR:-/out}"

export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq \
  build-essential ninja-build libgomp1 git libssl-dev libcurl4-openssl-dev wget ca-certificates file \
  software-properties-common
add-apt-repository -y ppa:ubuntu-toolchain-r/test
apt-get update -qq
apt-get install -y -qq gcc-14 g++-14
export CC=gcc-14
export CXX=g++-14

mkdir -p /tmp/cmake-install
cd /tmp/cmake-install
wget -q -O cmake.sh https://github.com/Kitware/CMake/releases/download/v4.2.0/cmake-4.2.0-linux-aarch64.sh
sh cmake.sh --prefix=/usr/local --skip-license
cd /
rm -rf /tmp/cmake-install

mkdir -p "${WORKDIR}/llama.cpp"
cd "${WORKDIR}/llama.cpp"
if [ ! -d .git ]; then
  git clone --depth 1 --branch "${TAG}" https://github.com/ggml-org/llama.cpp.git .
else
  git fetch --depth 1 origin "refs/tags/${TAG}" 2>/dev/null || true
  git checkout "${TAG}" 2>/dev/null || true
fi

ln -sf /usr/local/cuda/lib64/stubs/libcuda.so /tmp/libcuda.so.1
export LD_LIBRARY_PATH="/tmp:${LD_LIBRARY_PATH:-}"
export LIBRARY_PATH="/usr/local/cuda/lib64/stubs:${LIBRARY_PATH:-}"
export LDFLAGS="-L/usr/local/cuda/lib64/stubs ${LDFLAGS:-}"

cmake -S . -B build -G Ninja \
  -DCMAKE_INSTALL_RPATH='$ORIGIN' \
  -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON \
  -DCMAKE_EXE_LINKER_FLAGS="-Wl,--allow-shlib-undefined" \
  -DCMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES:-87}" \
  -DCMAKE_DISABLE_FIND_PACKAGE_NCCL=ON \
  -DBUILD_SHARED_LIBS=ON \
  -DGGML_NATIVE=OFF \
  -DGGML_CPU_ALL_VARIANTS=ON \
  -DGGML_CUDA=ON \
  -DGGML_BACKEND_DL=ON

cmake --build build --config Release -j "$(nproc)"

STAGE="${OUTDIR}/stage/${ROOT_NAME}"
rm -rf "${OUTDIR}/stage"
mkdir -p "${STAGE}/bin" "${STAGE}/lib"

for bin in llama-bench llama-cli llama-embedding llama-quantize llama-server; do
  cp -a "build/bin/${bin}" "${STAGE}/bin/"
done
shopt -s nullglob
for lib in build/bin/lib*.so*; do
  cp -a "${lib}" "${STAGE}/lib/"
done
shopt -u nullglob

cp -a /usr/lib/aarch64-linux-gnu/libstdc++.so.6* "${STAGE}/lib/"
cp -a /usr/lib/aarch64-linux-gnu/libgcc_s.so.1* "${STAGE}/lib/"

echo "=== llama-server --version ==="
LD_LIBRARY_PATH="${STAGE}/lib:/tmp" "${STAGE}/bin/llama-server" --version
file "${STAGE}/bin/llama-server" "${STAGE}/lib/libggml-cuda.so"
test -f "${STAGE}/lib/libggml-cuda.so"

cd "${OUTDIR}/stage"
tar -czf "${OUTDIR}/${ROOT_NAME}.tar.gz" "${ROOT_NAME}"
ls -lh "${OUTDIR}/${ROOT_NAME}.tar.gz"
