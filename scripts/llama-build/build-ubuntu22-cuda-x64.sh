#!/usr/bin/env bash
# Run inside: nvidia/cuda:12.9.1-devel-ubuntu22.04 (linux/amd64). Keep image locally; do not prune.
set -euo pipefail

TAG="${LLAMA_TAG:-b10326}"
WORKDIR="${WORKDIR:-/work}"
OUTDIR="${OUTDIR:-/out}"

export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq \
  build-essential ninja-build libgomp1 git libssl-dev libcurl4-openssl-dev wget ca-certificates file

mkdir -p /tmp/cmake-install
cd /tmp/cmake-install
wget -q -O cmake.sh https://github.com/Kitware/CMake/releases/download/v4.2.0/cmake-4.2.0-linux-x86_64.sh
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

cmake -S . -B build -G Ninja \
  -DCMAKE_INSTALL_RPATH='$ORIGIN' \
  -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON \
  -DCMAKE_EXE_LINKER_FLAGS="-Wl,--allow-shlib-undefined" \
  -DCMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES:-80;86;89}" \
  -DCMAKE_DISABLE_FIND_PACKAGE_NCCL=ON \
  -DBUILD_SHARED_LIBS=ON \
  -DGGML_NATIVE=OFF \
  -DGGML_CPU_ALL_VARIANTS=ON \
  -DGGML_CUDA=ON \
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

if [ -f /usr/local/cuda/lib64/stubs/libcuda.so ]; then
  ln -sf /usr/local/cuda/lib64/stubs/libcuda.so /tmp/libcuda.so.1
  export LD_LIBRARY_PATH="${OUTDIR}/stage/lib:/tmp:${LD_LIBRARY_PATH:-}"
fi

echo "=== llama-server --version ==="
LD_LIBRARY_PATH="${OUTDIR}/stage/lib:${LD_LIBRARY_PATH:-}" "${OUTDIR}/stage/bin/llama-server" --version
file "${OUTDIR}/stage/bin/llama-server" "${OUTDIR}/stage/lib/libggml-cuda.so"
test -f "${OUTDIR}/stage/lib/libggml-cuda.so"

cd "${OUTDIR}/stage"
tar -czf "${OUTDIR}/llama-${TAG}-bin-ubuntu-cuda-x64.tar.gz" bin lib
ls -lh "${OUTDIR}/llama-${TAG}-bin-ubuntu-cuda-x64.tar.gz"
