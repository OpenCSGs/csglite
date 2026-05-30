#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
if [[ -f "${REPO_ROOT}/local/secrets.env" ]]; then
  # shellcheck source=/dev/null
  set -a
  . "${REPO_ROOT}/local/secrets.env"
  set +a
fi

UPSTREAM_BASE_URL_DEFAULT="https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"
REQUESTED_VERSION="latest"
UPDATE_LATEST=1
KEEP_WORKDIR=0
PYTHON_BIN="${PYTHON_BIN:-}"

info() { printf '\033[0;32m[INFO]\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m[WARN]\033[0m %s\n' "$1"; }
die() { printf '\033[0;31m[ERROR]\033[0m %s\n' "$1" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: scripts/sync-claude-code-oss.sh [options] [version]

Mirror Claude Code native installer artifacts from Anthropic's public bucket
to the configured StarHub OSS bucket, preserving a versioned layout:

  <prefix>/latest
  <prefix>/<version>/manifest.json
  <prefix>/<version>/<platform>/<binary>

Options:
  --version VERSION     Claude Code version to mirror (default: latest)
  --no-update-latest    Upload versioned files only; do not overwrite latest
  --keep-workdir        Keep the temporary download directory for inspection
  -h, --help            Show this help

Required environment variables (loaded from local/secrets.env when present):
  STARHUB_OSS_ACCESS_KEY_ID
  STARHUB_OSS_ACCESS_KEY_SECRET
  STARHUB_OSS_ENDPOINT
  STARHUB_OSS_PUBLIC_BUCKET

Optional environment variables:
  STARHUB_OSS_REGION              Default: cn-beijing
  STARHUB_CLAUDE_DIST_PREFIX      Default: claude-code-releases
  STARHUB_CLAUDE_DIST_BASE_URL    Public URL override for generated manifest
  CLAUDE_CODE_UPSTREAM_BASE_URL   Override Anthropic upstream bucket

Notes:
  - Requires: python3, curl or wget, and the Python package oss2
  - The script updates latest only after every platform artifact has been
    downloaded, checksum-verified, and uploaded successfully.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || die "--version requires a value"
      REQUESTED_VERSION="$2"
      shift 2
      ;;
    --no-update-latest)
      UPDATE_LATEST=0
      shift
      ;;
    --keep-workdir)
      KEEP_WORKDIR=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      REQUESTED_VERSION="$1"
      shift
      ;;
  esac
done

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

resolve_python_bin() {
  local candidate=""
  local candidates=()
  if [[ -n "${PYTHON_BIN}" ]]; then
    candidates+=("${PYTHON_BIN}")
  fi
  candidates+=(
    /opt/homebrew/opt/python@3.11/bin/python3.11
    /usr/local/bin/python3.11
  )
  candidates+=(python3.11 python3 python)

  for candidate in "${candidates[@]}"; do
    if ! command -v "${candidate}" >/dev/null 2>&1; then
      continue
    fi
    candidate="$(command -v "${candidate}")"
    if "${candidate}" - <<'PY' >/dev/null 2>&1
import oss2  # noqa: F401
PY
    then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

normalize_version() {
  local value="$1"
  if [[ "$value" == "latest" ]]; then
    printf '%s\n' "latest"
    return
  fi
  printf '%s\n' "${value#v}"
}

trim_trailing_slash() {
  local value="$1"
  while [[ "$value" == */ ]]; do
    value="${value%/}"
  done
  printf '%s\n' "$value"
}

endpoint_url() {
  local endpoint="$1"
  case "$endpoint" in
    http://*|https://*) printf '%s\n' "$endpoint" ;;
    *) printf 'https://%s\n' "$endpoint" ;;
  esac
}

bucket_endpoint_url() {
  local endpoint="$1"
  local host=""
  host="$(endpoint_url "$endpoint")"
  host="${host#https://}"
  host="${host#http://}"
  printf 'https://%s.%s\n' "${STARHUB_OSS_PUBLIC_BUCKET}" "$host"
}

ensure_external_proxy() {
  local before=""
  local after=""
  before="${https_proxy:-}${HTTPS_PROXY:-}${http_proxy:-}${HTTP_PROXY:-}"
  if [[ -n "$before" ]]; then
    return 0
  fi
  if [[ -z "${HOME:-}" || ! -f "${HOME}/.myshrc" ]]; then
    return 0
  fi

  set +u
  # shellcheck source=/dev/null
  . "${HOME}/.myshrc" >/dev/null 2>&1 || true
  set -u

  after="${https_proxy:-}${HTTPS_PROXY:-}${http_proxy:-}${HTTP_PROXY:-}"
  if [[ -n "$after" ]]; then
    printf '\033[0;32m[INFO]\033[0m %s\n' "loaded proxy settings from ${HOME}/.myshrc for upstream downloads" >&2
  fi
}

download_text() {
  local url="$1"
  ensure_external_proxy
  if command -v curl >/dev/null 2>&1; then
    curl --connect-timeout 15 --max-time 60 --retry 3 --retry-delay 2 -fsSL "$url"
  else
    wget --tries=3 --timeout=20 -q -O - "$url"
  fi
}

download_file() {
  local url="$1"
  local output="$2"
  ensure_external_proxy
  if [[ "$url" == https://storage.googleapis.com/* ]]; then
    if parallel_range_download_file "$url" "$output"; then
      return 0
    fi
    warn "range download unavailable for ${url}; falling back to curl"
  fi
  if command -v curl >/dev/null 2>&1; then
    if [[ -f "$output" ]]; then
      curl --connect-timeout 15 --max-time 1800 --retry 5 --retry-delay 5 -C - -fSL -o "$output" "$url"
    else
      curl --connect-timeout 15 --max-time 1800 --retry 5 --retry-delay 5 -fSL -o "$output" "$url"
    fi
  else
    wget --tries=3 --timeout=30 -O "$output" "$url"
  fi
}

parallel_range_download_file() {
  local url="$1"
  local output="$2"
  "${PYTHON_BIN}" - "$url" "$output" <<'PY'
import concurrent.futures
import os
import sys
import time
import urllib.error
import urllib.request

url, output = sys.argv[1:3]
part_path = output + ".part"
workers = int(os.environ.get("CLAUDE_CODE_DOWNLOAD_WORKERS", "8"))
chunk_size = int(os.environ.get("CLAUDE_CODE_DOWNLOAD_CHUNK_SIZE", str(2 * 1024 * 1024)))


def request(method, headers=None, timeout=60):
    req = urllib.request.Request(url, method=method, headers=headers or {})
    return urllib.request.urlopen(req, timeout=timeout)


def content_length():
    try:
        with request("HEAD") as resp:
            length = resp.headers.get("Content-Length") or resp.headers.get("x-goog-stored-content-length")
            ranges = resp.headers.get("Accept-Ranges", "")
            if not length or "bytes" not in ranges.lower():
                return 0
            return int(length)
    except Exception:
        return 0


def download_range(start, end):
    headers = {"Range": f"bytes={start}-{end}"}
    last_err = None
    for attempt in range(4):
        try:
            with request("GET", headers=headers, timeout=180) as resp:
                status = getattr(resp, "status", resp.getcode())
                if status not in (200, 206):
                    raise RuntimeError(f"unexpected HTTP status {status}")
                data = resp.read()
            expected = end - start + 1
            if len(data) != expected:
                raise RuntimeError(f"short read for bytes {start}-{end}: got {len(data)}, want {expected}")
            with open(part_path, "r+b") as fh:
                fh.seek(start)
                fh.write(data)
            return
        except Exception as exc:
            last_err = exc
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"range {start}-{end} failed: {last_err}")


total = content_length()
if total <= 0:
    raise SystemExit("range download unavailable")

os.makedirs(os.path.dirname(output) or ".", exist_ok=True)
with open(part_path, "wb") as fh:
    fh.truncate(total)

ranges = []
for start in range(0, total, chunk_size):
    end = min(start + chunk_size - 1, total - 1)
    ranges.append((start, end))

with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, workers)) as pool:
    futures = [pool.submit(download_range, start, end) for start, end in ranges]
    for future in concurrent.futures.as_completed(futures):
        future.result()

actual = os.path.getsize(part_path)
if actual != total:
    raise SystemExit(f"download size mismatch: got {actual}, want {total}")
os.replace(part_path, output)
PY
}

sha256_file() {
  "${PYTHON_BIN}" - "$1" <<'PY'
import hashlib
import sys

path = sys.argv[1]
h = hashlib.sha256()
with open(path, "rb") as fh:
    for chunk in iter(lambda: fh.read(1024 * 1024), b""):
        h.update(chunk)
print(h.hexdigest())
PY
}

resolve_public_base_url() {
  local endpoint_host=""
  if [[ -n "${STARHUB_CLAUDE_DIST_BASE_URL:-}" ]]; then
    trim_trailing_slash "${STARHUB_CLAUDE_DIST_BASE_URL}"
    return
  fi
  endpoint_host="${STARHUB_OSS_ENDPOINT#https://}"
  endpoint_host="${endpoint_host#http://}"
  printf 'https://%s.%s/%s\n' \
    "${STARHUB_OSS_PUBLIC_BUCKET}" \
    "${endpoint_host}" \
    "${STARHUB_CLAUDE_DIST_PREFIX}"
}

require_oss2() {
  "${PYTHON_BIN}" - <<'PY' >/dev/null 2>&1
import oss2  # noqa: F401
PY
}

oss_put_object() {
  local file_path="$1"
  local object_key="$2"
  local content_type="$3"

  local cmd=(
    env
    -u http_proxy
    -u https_proxy
    -u HTTP_PROXY
    -u HTTPS_PROXY
    -u all_proxy
    -u ALL_PROXY
    "${PYTHON_BIN}"
    -
    "${file_path}"
    "${object_key}"
    "${content_type}"
  )
  "${cmd[@]}" <<'PY' >/dev/null
import os
import sys

import oss2

file_path, object_key, content_type = sys.argv[1:4]
endpoint = os.environ["STARHUB_OSS_ENDPOINT"]
bucket_name = os.environ["STARHUB_OSS_PUBLIC_BUCKET"]
access_key = os.environ["STARHUB_OSS_ACCESS_KEY_ID"]
access_secret = os.environ["STARHUB_OSS_ACCESS_KEY_SECRET"]

if endpoint.startswith(("http://", "https://")):
    endpoint = endpoint.split("://", 1)[1]

auth = oss2.Auth(access_key, access_secret)
bucket = oss2.Bucket(auth, endpoint, bucket_name)
headers = {}
if content_type:
    headers["Content-Type"] = content_type

result = bucket.put_object_from_file(object_key, file_path, headers=headers)
if getattr(result, "status", None) not in (200, 201):
    raise SystemExit(f"unexpected OSS status for {object_key}: {getattr(result, 'status', 'unknown')}")
PY
}

require_cmd python3
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  die "curl or wget is required"
fi
PYTHON_BIN="$(resolve_python_bin || true)"
[[ -n "${PYTHON_BIN}" ]] || die "python package oss2 is required. Install it with: python3 -m pip install --user oss2"
if ! require_oss2; then
  die "python package oss2 is required. Install it with: python3 -m pip install --user oss2"
fi

STARHUB_OSS_ACCESS_KEY_ID="${STARHUB_OSS_ACCESS_KEY_ID:-}"
STARHUB_OSS_ACCESS_KEY_SECRET="${STARHUB_OSS_ACCESS_KEY_SECRET:-}"
STARHUB_OSS_ENDPOINT="${STARHUB_OSS_ENDPOINT:-}"
STARHUB_OSS_REGION="${STARHUB_OSS_REGION:-cn-beijing}"
STARHUB_OSS_PUBLIC_BUCKET="${STARHUB_OSS_PUBLIC_BUCKET:-}"
STARHUB_CLAUDE_DIST_PREFIX="$(trim_trailing_slash "${STARHUB_CLAUDE_DIST_PREFIX:-claude-code-releases}")"
CLAUDE_CODE_UPSTREAM_BASE_URL="$(trim_trailing_slash "${CLAUDE_CODE_UPSTREAM_BASE_URL:-$UPSTREAM_BASE_URL_DEFAULT}")"

[[ -n "$STARHUB_OSS_ACCESS_KEY_ID" ]] || die "STARHUB_OSS_ACCESS_KEY_ID is required"
[[ -n "$STARHUB_OSS_ACCESS_KEY_SECRET" ]] || die "STARHUB_OSS_ACCESS_KEY_SECRET is required"
[[ -n "$STARHUB_OSS_ENDPOINT" ]] || die "STARHUB_OSS_ENDPOINT is required"
[[ -n "$STARHUB_OSS_PUBLIC_BUCKET" ]] || die "STARHUB_OSS_PUBLIC_BUCKET is required"

export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-$STARHUB_OSS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-$STARHUB_OSS_ACCESS_KEY_SECRET}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-$STARHUB_OSS_REGION}"
export AWS_EC2_METADATA_DISABLED="true"

OSS_REGION="${STARHUB_OSS_REGION}"
OSS_BUCKET="${STARHUB_OSS_PUBLIC_BUCKET}"
PUBLIC_BASE_URL="$(resolve_public_base_url)"

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/claude-code-oss-sync.XXXXXX")"
cleanup() {
  if [[ "$KEEP_WORKDIR" == "1" ]]; then
    info "kept workdir: ${WORKDIR}"
  else
    rm -rf "${WORKDIR}"
  fi
}
trap cleanup EXIT

VERSION="$(normalize_version "$REQUESTED_VERSION")"
if [[ "$VERSION" == "latest" ]]; then
  info "resolving upstream latest version"
  VERSION="$(download_text "${CLAUDE_CODE_UPSTREAM_BASE_URL}/latest" | tr -d '[:space:]')"
fi
[[ -n "$VERSION" ]] || die "failed to resolve Claude Code version"
info "syncing Claude Code version ${VERSION}"

UPSTREAM_MANIFEST="${WORKDIR}/upstream-manifest.json"
NORMALIZED_MANIFEST="${WORKDIR}/manifest.json"
LATEST_FILE="${WORKDIR}/latest"
PLATFORM_INDEX="${WORKDIR}/platforms.tsv"

download_text "${CLAUDE_CODE_UPSTREAM_BASE_URL}/${VERSION}/manifest.json" > "${UPSTREAM_MANIFEST}"

"${PYTHON_BIN}" - "${UPSTREAM_MANIFEST}" "${PLATFORM_INDEX}" <<'PY'
import json
import sys

manifest_path, index_path = sys.argv[1:3]
with open(manifest_path, "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

platforms = manifest.get("platforms", {})
if not platforms:
    raise SystemExit("manifest does not contain any platforms")

with open(index_path, "w", encoding="utf-8") as out:
    for platform in sorted(platforms):
        entry = platforms[platform]
        binary = entry.get("binary") or ("claude.exe" if platform.startswith("win32-") else "claude")
        checksum = entry.get("checksum")
        size = entry.get("size", "")
        if not checksum:
            raise SystemExit(f"missing checksum for {platform}")
        out.write(f"{platform}\t{binary}\t{checksum}\t{size}\n")
PY

while IFS=$'\t' read -r platform binary checksum expected_size; do
  [[ -n "$platform" ]] || continue
  artifact_path="${WORKDIR}/${platform}-${binary}"
  source_url="${CLAUDE_CODE_UPSTREAM_BASE_URL}/${VERSION}/${platform}/${binary}"
  object_key="${STARHUB_CLAUDE_DIST_PREFIX}/${VERSION}/${platform}/${binary}"

  info "downloading ${platform}/${binary}"
  download_file "${source_url}" "${artifact_path}"

  actual_checksum="$(sha256_file "${artifact_path}")"
  if [[ "${actual_checksum}" != "${checksum}" ]]; then
    die "checksum mismatch for ${platform}/${binary}: expected ${checksum}, got ${actual_checksum}"
  fi

  if [[ -n "${expected_size}" ]]; then
    actual_size="$("${PYTHON_BIN}" - "${artifact_path}" <<'PY'
import os
import sys

print(os.path.getsize(sys.argv[1]))
PY
)"
    if [[ "${actual_size}" != "${expected_size}" ]]; then
      die "size mismatch for ${platform}/${binary}: expected ${expected_size}, got ${actual_size}"
    fi
  fi

  info "uploading ${object_key}"
  oss_put_object "${artifact_path}" "${object_key}" "application/octet-stream"
done < "${PLATFORM_INDEX}"

"${PYTHON_BIN}" - "${UPSTREAM_MANIFEST}" "${NORMALIZED_MANIFEST}" "${VERSION}" "${CLAUDE_CODE_UPSTREAM_BASE_URL}" "${PUBLIC_BASE_URL}" <<'PY'
import json
import sys
from datetime import datetime, timezone

src_path, dst_path, version, upstream_base_url, public_base_url = sys.argv[1:6]
with open(src_path, "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

manifest["version"] = version
manifest["upstream_base_url"] = upstream_base_url
manifest["public_base_url"] = public_base_url
manifest["synced_at"] = datetime.now(timezone.utc).isoformat()

for platform, entry in manifest.get("platforms", {}).items():
    binary = entry.get("binary") or ("claude.exe" if platform.startswith("win32-") else "claude")
    entry["binary"] = binary
    entry["path"] = f"{version}/{platform}/{binary}"
    entry["source_url"] = f"{upstream_base_url}/{version}/{platform}/{binary}"
    entry["public_url"] = f"{public_base_url}/{version}/{platform}/{binary}"

with open(dst_path, "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

info "uploading ${STARHUB_CLAUDE_DIST_PREFIX}/${VERSION}/manifest.json"
oss_put_object "${NORMALIZED_MANIFEST}" "${STARHUB_CLAUDE_DIST_PREFIX}/${VERSION}/manifest.json" "application/json"

if [[ "${UPDATE_LATEST}" == "1" ]]; then
  printf '%s\n' "${VERSION}" > "${LATEST_FILE}"
  info "uploading ${STARHUB_CLAUDE_DIST_PREFIX}/latest"
  oss_put_object "${LATEST_FILE}" "${STARHUB_CLAUDE_DIST_PREFIX}/latest" "text/plain"
fi

info "Claude Code ${VERSION} mirror is ready"
info "public base URL: ${PUBLIC_BASE_URL}"
info "manifest URL: ${PUBLIC_BASE_URL}/${VERSION}/manifest.json"
if [[ "${UPDATE_LATEST}" == "1" ]]; then
  info "latest URL: ${PUBLIC_BASE_URL}/latest"
fi
