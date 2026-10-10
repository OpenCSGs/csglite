#!/usr/bin/env bash
# Mirror official ggml-org/llama.cpp release assets to GitLab generic packages.
#
# Upstream's Linux CUDA and ROCm jobs run on Ubuntu 24.04, so those tarballs need
# GLIBC_2.38 and cannot start on the 22.04 runtime images csghub-lite ships.
# scripts/llama-build/rebuild-upload-*.sh builds those locally instead, and the
# companion Linux cudart archives are skipped with them. Everything else upstream
# publishes is mirrored verbatim under its official filename.
#
# Usage:
#   ./scripts/llama-build/mirror-official-assets.sh [tag] [--dry-run]
#
# Env:
#   LLAMA_MIRROR_EXPECTED=<n>  fail unless exactly <n> assets are mirrored
#   LLAMA_BUILD_SKIP_UPLOAD=1 download and verify without publishing
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
source "${ROOT}/common.sh"

TAG=""
DRY_RUN=0
for arg in "$@"; do
  case "${arg}" in
    --dry-run) DRY_RUN=1 ;;
    -*) echo "unknown option: ${arg}" >&2; exit 1 ;;
    *) TAG="${arg}" ;;
  esac
done
TAG="$(llama_build_tag "${TAG}")"

OUT="${LLAMA_BUILD_OUT_DIR}/official"
RELEASE_JSON="${OUT}/release-${TAG}.json"

command -v python3 >/dev/null 2>&1 || {
  echo "python3 is required to read the release metadata." >&2
  exit 1
}

# GitHub is an external site; the proxy in ~/.myshrc may be needed to reach it.
# Guard the file check: under `set -e`, bash 3.2 aborts on `source` of a missing
# file even with `|| true`.
if [ -f "${HOME}/.myshrc" ]; then
  # shellcheck source=/dev/null
  source "${HOME}/.myshrc"
fi

mkdir -p "${OUT}"

echo "=== release metadata: ggml-org/llama.cpp ${TAG} ==="
curl -fsSL -o "${RELEASE_JSON}" \
  "https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/${TAG}"

# Upstream Linux CUDA/ROCm builds and their cudart companions are 24.04 artifacts
# that this repository must not serve to 22.04 hosts.
python3 - "${RELEASE_JSON}" "${TAG}" >"${OUT}/mirror-list-${TAG}.txt" <<'PY'
import json
import re
import sys

release_path, tag = sys.argv[1], sys.argv[2]
with open(release_path, encoding="utf-8") as handle:
    release = json.load(handle)

skip_patterns = [
    re.compile(rf"^llama-{re.escape(tag)}-bin-ubuntu-cuda-[0-9.]+-(x64|arm64)\.tar\.gz$"),
    re.compile(rf"^llama-{re.escape(tag)}-bin-ubuntu-rocm-[0-9.]+-x64\.tar\.gz$"),
    re.compile(rf"^cudart-llama-{re.escape(tag)}-bin-ubuntu-cuda-[0-9.]+-(x64|arm64)\.tar\.gz$"),
]

mirror, skipped = [], []
for asset in release.get("assets", []):
    name = asset["name"]
    if any(pattern.match(name) for pattern in skip_patterns):
        skipped.append(name)
    else:
        mirror.append(name)

for name in sorted(mirror):
    print(name)
for name in sorted(skipped):
    print(f"# skipped (local 22.04 build covers this): {name}", file=sys.stderr)
PY

MIRROR_LIST="${OUT}/mirror-list-${TAG}.txt"
MIRROR_COUNT="$(grep -c . "${MIRROR_LIST}")"
UPSTREAM_COUNT="$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))["assets"]))' "${RELEASE_JSON}")"

echo "=== ${TAG}: upstream ${UPSTREAM_COUNT} assets, mirroring ${MIRROR_COUNT} ==="
cat "${MIRROR_LIST}"

if [ -n "${LLAMA_MIRROR_EXPECTED:-}" ] && [ "${MIRROR_COUNT}" != "${LLAMA_MIRROR_EXPECTED}" ]; then
  echo "expected ${LLAMA_MIRROR_EXPECTED} mirrored assets, found ${MIRROR_COUNT}." >&2
  exit 1
fi

if [ "${DRY_RUN}" = "1" ]; then
  echo "--dry-run, nothing downloaded or uploaded."
  exit 0
fi

while IFS= read -r name; do
  [ -n "${name}" ] || continue
  file="${OUT}/${name}"
  if [ ! -s "${file}" ]; then
    echo "downloading ${name}"
    # GitHub throttles long direct transfers to a crawl; --speed-limit aborts a
    # stalled connection so --retry can pick it up again. Export https_proxy (or
    # source ~/.myshrc) when the proxy is faster than the direct route.
    curl -fSL --retry 5 --retry-delay 5 --speed-limit 102400 --speed-time 30 \
      -o "${file}.part" \
      "https://github.com/ggml-org/llama.cpp/releases/download/${TAG}/${name}"
    mv "${file}.part" "${file}"
  else
    echo "reusing ${name}"
  fi
  case "${name}" in
    *.tar.gz) tar tzf "${file}" >/dev/null ;;
    *.zip) unzip -tq "${file}" >/dev/null ;;
  esac
done <"${MIRROR_LIST}"

if [ "${LLAMA_BUILD_SKIP_UPLOAD:-0}" = "1" ]; then
  echo "LLAMA_BUILD_SKIP_UPLOAD=1, not uploading. Artifacts: ${OUT}"
  shasum -a 256 "${OUT}"/* 2>/dev/null | grep -v "/release-\|/mirror-list-" || true
  exit 0
fi

while IFS= read -r name; do
  [ -n "${name}" ] || continue
  llama_build_upload_tarball "${TAG}" "${OUT}/${name}"
done <"${MIRROR_LIST}"

echo "mirrored ${MIRROR_COUNT} official assets for ${TAG}."
