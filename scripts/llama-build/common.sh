#!/usr/bin/env bash
# Shared helpers for Ubuntu 22.04 CUDA and ROCm llama.cpp mirror builds.
set -euo pipefail

LLAMA_BUILD_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LLAMA_BUILD_REPO_ROOT="$(cd "${LLAMA_BUILD_SCRIPT_DIR}/../.." && pwd)"
LLAMA_BUILD_WORK_DIR="${LLAMA_BUILD_WORK_DIR:-${LLAMA_BUILD_SCRIPT_DIR}/work}"
LLAMA_BUILD_OUT_DIR="${LLAMA_BUILD_OUT_DIR:-${LLAMA_BUILD_WORK_DIR}/out}"
LLAMA_BUILD_CUDA_IMAGE="${LLAMA_BUILD_CUDA_IMAGE:-nvidia/cuda:12.9.1-devel-ubuntu22.04}"
# Must stay on the same ROCm series and Ubuntu base as docker/rocm/Dockerfile's
# runtime image, otherwise the package cannot load there.
LLAMA_BUILD_ROCM_IMAGE="${LLAMA_BUILD_ROCM_IMAGE:-rocm/dev-ubuntu-22.04:7.2.2-complete}"
LLAMA_BUILD_ROCM_SERIES="${LLAMA_BUILD_ROCM_SERIES:-7.2}"
# Plain 22.04 userland, used to prove a package does not need GLIBC_2.38.
LLAMA_BUILD_UBUNTU_IMAGE="${LLAMA_BUILD_UBUNTU_IMAGE:-ubuntu:22.04}"
LLAMA_BUILD_GITLAB_PROJECT_ID="${LLAMA_BUILD_GITLAB_PROJECT_ID:-393}"
LLAMA_BUILD_GITLAB_API="${LLAMA_BUILD_GITLAB_API:-https://git-devops.opencsg.com/api/v4}"

llama_build_tag() {
  printf '%s\n' "${LLAMA_TAG:-${1:-b10830}}"
}

llama_build_ensure_docker() {
  if ! docker info >/dev/null 2>&1; then
    echo "Docker engine is not running. Start Docker Desktop and wait until it is ready." >&2
    exit 1
  fi
}

llama_build_ensure_image() {
  local image="$1"
  local platform="$2"
  if docker image inspect --platform "${platform}" "${image}" >/dev/null 2>&1; then
    echo "Reusing local ${image} (${platform})"
    return 0
  fi
  echo "Pulling ${image} (${platform})..."
  docker pull --platform "${platform}" "${image}"
}

llama_build_ensure_cuda_image() {
  llama_build_ensure_image "${LLAMA_BUILD_CUDA_IMAGE}" "$1"
}

llama_build_ensure_rocm_image() {
  llama_build_ensure_image "${LLAMA_BUILD_ROCM_IMAGE}" "$1"
}

# amd64 builds run under QEMU on Apple Silicon. A CUDA build is merely slow
# there; a multi-target ROCm build is slow enough to be worth calling out.
llama_build_warn_emulated_amd64() {
  local platform="$1"
  if [ "${platform}" = "linux/amd64" ] && [ "$(uname -m)" = "arm64" ]; then
    echo "WARNING: building linux/amd64 on an arm64 host runs under QEMU emulation."
    echo "         Expect a very long build. Prefer an x86_64 Linux builder,"
    echo "         or narrow GPU_TARGETS to the gfx architectures you actually ship."
  fi
}

llama_build_clone_source() {
  local dest="$1"
  local tag="$2"
  if [ -d "${dest}/.git" ]; then
    return 0
  fi
  mkdir -p "$(dirname "${dest}")"
  # shellcheck source=/dev/null
  source "${HOME}/.myshrc" 2>/dev/null || true
  git clone --depth 1 --branch "${tag}" https://github.com/ggml-org/llama.cpp.git "${dest}"
}

llama_build_load_gitlab_token() {
  unset https_proxy http_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY all_proxy 2>/dev/null || true
  if [ -z "${GITLAB_TOKEN:-}" ] && [ -f "${LLAMA_BUILD_REPO_ROOT}/local/secrets.env" ]; then
    # shellcheck source=/dev/null
    . "${LLAMA_BUILD_REPO_ROOT}/local/secrets.env"
  fi
  if [ -z "${GITLAB_TOKEN:-}" ]; then
    echo "GITLAB_TOKEN is not set (expected in local/secrets.env)." >&2
    return 1
  fi
}

llama_build_upload_tarball() {
  local tag="$1"
  local tarball="$2"
  local name
  name="$(basename "${tarball}")"
  llama_build_load_gitlab_token
  curl -fsS -o /tmp/gitlab-llama-upload.json -w "upload ${name}: HTTP %{http_code}\n" \
    --header "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
    --upload-file "${tarball}" \
    "${LLAMA_BUILD_GITLAB_API}/projects/${LLAMA_BUILD_GITLAB_PROJECT_ID}/packages/generic/llama-cpp/${tag}/${name}"
  shasum -a 256 "${tarball}"
}
