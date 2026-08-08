#!/usr/bin/env bash
# Shared helpers for Ubuntu 22.04 CUDA llama.cpp mirror builds.
set -euo pipefail

LLAMA_BUILD_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LLAMA_BUILD_REPO_ROOT="$(cd "${LLAMA_BUILD_SCRIPT_DIR}/../.." && pwd)"
LLAMA_BUILD_WORK_DIR="${LLAMA_BUILD_WORK_DIR:-${LLAMA_BUILD_SCRIPT_DIR}/work}"
LLAMA_BUILD_OUT_DIR="${LLAMA_BUILD_OUT_DIR:-${LLAMA_BUILD_WORK_DIR}/out}"
LLAMA_BUILD_CUDA_IMAGE="${LLAMA_BUILD_CUDA_IMAGE:-nvidia/cuda:12.9.1-devel-ubuntu22.04}"
LLAMA_BUILD_GITLAB_PROJECT_ID="${LLAMA_BUILD_GITLAB_PROJECT_ID:-393}"
LLAMA_BUILD_GITLAB_API="${LLAMA_BUILD_GITLAB_API:-https://git-devops.opencsg.com/api/v4}"

llama_build_tag() {
  printf '%s\n' "${LLAMA_TAG:-${1:-b10326}}"
}

llama_build_ensure_docker() {
  if ! docker info >/dev/null 2>&1; then
    echo "Docker engine is not running. Start Docker Desktop and wait until it is ready." >&2
    exit 1
  fi
}

llama_build_ensure_cuda_image() {
  local platform="$1"
  if docker image inspect --platform "${platform}" "${LLAMA_BUILD_CUDA_IMAGE}" >/dev/null 2>&1; then
    echo "Reusing local ${LLAMA_BUILD_CUDA_IMAGE} (${platform})"
    return 0
  fi
  echo "Pulling ${LLAMA_BUILD_CUDA_IMAGE} (${platform})..."
  docker pull --platform "${platform}" "${LLAMA_BUILD_CUDA_IMAGE}"
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
