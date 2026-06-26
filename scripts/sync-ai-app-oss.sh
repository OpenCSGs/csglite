#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
if [[ -f "${REPO_ROOT}/local/secrets.env" ]]; then
  # shellcheck source=/dev/null
  set -a
  . "${REPO_ROOT}/local/secrets.env"
  set +a
fi

REQUESTED_VERSION="latest"
UPDATE_LATEST=1
KEEP_WORKDIR=0
PYTHON_BIN="${PYTHON_BIN:-}"
APP_IDS=()

info() { printf '\033[0;32m[INFO]\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m[WARN]\033[0m %s\n' "$1"; }
die() { printf '\033[0;31m[ERROR]\033[0m %s\n' "$1" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: scripts/sync-ai-app-oss.sh [options]

Mirror supported AI app release artifacts to the configured StarHub OSS bucket.

Supported apps:
  - claude-code
  - open-code
  - open-code-review
  - codex
  - codex-app

Options:
  --app APP             App to sync (repeatable). Defaults to all mirror-backed apps
  --version VERSION     Version to mirror. Use only when syncing a single app.
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
  STARHUB_OPEN_CODE_DIST_PREFIX   Default: open-code-releases
  STARHUB_OPEN_CODE_DIST_BASE_URL Public URL override for generated manifest
  STARHUB_OPEN_CODE_REVIEW_DIST_PREFIX
                                    Default: open-code-review-releases
  STARHUB_OPEN_CODE_REVIEW_DIST_BASE_URL
                                    Public URL override for generated manifest
  STARHUB_CODEX_DIST_PREFIX       Default: codex-releases
  STARHUB_CODEX_DIST_BASE_URL     Public URL override for generated manifest
  STARHUB_CODEX_APP_DIST_PREFIX   Default: codex-app-releases
  STARHUB_CODEX_APP_DIST_BASE_URL Public URL override for generated manifest

Examples:
  ./scripts/sync-ai-app-oss.sh
  ./scripts/sync-ai-app-oss.sh --app claude-code --app open-code --app open-code-review --app codex
  ./scripts/sync-ai-app-oss.sh --app codex --version 0.118.0
  ./scripts/sync-ai-app-oss.sh --app codex-app
  ./scripts/sync-ai-app-oss.sh --app claude-code
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app)
      [[ $# -ge 2 ]] || die "--app requires a value"
      APP_IDS+=("$2")
      shift 2
      ;;
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
    *)
      die "unknown argument: $1"
      ;;
  esac
done

if [[ "${#APP_IDS[@]}" -eq 0 ]]; then
  APP_IDS=(claude-code open-code open-code-review codex)
fi

if [[ "$REQUESTED_VERSION" != "latest" && "${#APP_IDS[@]}" -ne 1 ]]; then
  die "--version can only be used when syncing a single app"
fi

for app_id in "${APP_IDS[@]}"; do
  case "${app_id}" in
    claude-code|open-code|open-code-review|codex|codex-app) ;;
    *) die "unsupported app: ${app_id}" ;;
  esac
done

trim_trailing_slash() {
  local value="$1"
  while [[ "$value" == */ ]]; do
    value="${value%/}"
  done
  printf '%s\n' "$value"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
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

download_file() {
  local url="$1"
  local output="$2"
  ensure_external_proxy
  if command -v curl >/dev/null 2>&1; then
    curl --connect-timeout 15 --max-time 7200 --retry 5 --retry-delay 5 --retry-all-errors -fsSL -o "$output" "$url"
  else
    wget --tries=5 --timeout=60 -O "$output" "$url"
  fi
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

resolve_public_base_url() {
  local prefix="$1"
  local explicit_url="${2:-}"
  local endpoint_host=""
  if [[ -n "${explicit_url}" ]]; then
    trim_trailing_slash "${explicit_url}"
    return
  fi
  endpoint_host="${STARHUB_OSS_ENDPOINT#https://}"
  endpoint_host="${endpoint_host#http://}"
  printf 'https://%s.%s/%s\n' \
    "${STARHUB_OSS_PUBLIC_BUCKET}" \
    "${endpoint_host}" \
    "${prefix}"
}

app_repo() {
  case "$1" in
    open-code) printf '%s\n' "anomalyco/opencode" ;;
    open-code-review) printf '%s\n' "alibaba/open-code-review" ;;
    codex) printf '%s\n' "openai/codex" ;;
    *)
      die "unsupported release-backed app: $1"
      ;;
  esac
}

app_prefix() {
  case "$1" in
    open-code) trim_trailing_slash "${STARHUB_OPEN_CODE_DIST_PREFIX:-open-code-releases}" ;;
    open-code-review) trim_trailing_slash "${STARHUB_OPEN_CODE_REVIEW_DIST_PREFIX:-open-code-review-releases}" ;;
    codex) trim_trailing_slash "${STARHUB_CODEX_DIST_PREFIX:-codex-releases}" ;;
    *)
      die "unsupported release-backed app: $1"
      ;;
  esac
}

app_public_base_url() {
  case "$1" in
    open-code) resolve_public_base_url "$(app_prefix "$1")" "${STARHUB_OPEN_CODE_DIST_BASE_URL:-}" ;;
    open-code-review) resolve_public_base_url "$(app_prefix "$1")" "${STARHUB_OPEN_CODE_REVIEW_DIST_BASE_URL:-}" ;;
    codex) resolve_public_base_url "$(app_prefix "$1")" "${STARHUB_CODEX_DIST_BASE_URL:-}" ;;
    *)
      die "unsupported release-backed app: $1"
      ;;
  esac
}

normalize_app_version() {
  local app_id="$1"
  local value="$2"
  case "$app_id" in
    open-code|open-code-review)
      printf '%s\n' "${value#v}"
      ;;
    codex)
      value="${value#rust-v}"
      printf '%s\n' "${value#v}"
      ;;
    *)
      die "unsupported app for version normalization: $app_id"
      ;;
  esac
}

release_tag_for_version() {
  local app_id="$1"
  local value="$2"
  local normalized=""
  normalized="$(normalize_app_version "$app_id" "$value")"
  case "$app_id" in
    open-code|open-code-review) printf 'v%s\n' "${normalized}" ;;
    codex) printf 'rust-v%s\n' "${normalized}" ;;
    *)
      die "unsupported app for release tag resolution: $app_id"
      ;;
  esac
}

gh_release_json() {
  local repo="$1"
  local tag="${2:-}"
  ensure_external_proxy
  if [[ -n "$tag" ]]; then
    gh release view "$tag" --repo "$repo" --json tagName,assets
  else
    gh release view --repo "$repo" --json tagName,assets
  fi
}

sync_claude_via_wrapper() {
  local cmd=("${REPO_ROOT}/scripts/sync-claude-code-oss.sh")
  if [[ "${REQUESTED_VERSION}" != "latest" ]]; then
    cmd+=(--version "${REQUESTED_VERSION}")
  fi
  if [[ "${UPDATE_LATEST}" == "0" ]]; then
    cmd+=(--no-update-latest)
  fi
  if [[ "${KEEP_WORKDIR}" == "1" ]]; then
    cmd+=(--keep-workdir)
  fi
  info "delegating Claude Code sync to scripts/sync-claude-code-oss.sh"
  "${cmd[@]}"
}

sync_release_app() {
  local app_id="$1"
  local repo=""
  local prefix=""
  local public_base_url=""
  local requested_tag=""
  local release_json=""
  local version=""
  local tag=""
  local workdir=""
  local release_file=""
  local index_file=""
  local manifest_file=""
  local latest_file=""
  local object_key=""
  local artifact_path=""
  local actual_checksum=""
  local actual_size=""

  repo="$(app_repo "${app_id}")"
  prefix="$(app_prefix "${app_id}")"
  public_base_url="$(app_public_base_url "${app_id}")"

  if [[ "${REQUESTED_VERSION}" != "latest" ]]; then
    requested_tag="$(release_tag_for_version "${app_id}" "${REQUESTED_VERSION}")"
  fi

  workdir="$(mktemp -d "${TMPDIR:-/tmp}/${app_id}-oss-sync.XXXXXX")"
  if [[ "${KEEP_WORKDIR}" == "1" ]]; then
    info "kept workdir for ${app_id}: ${workdir}"
  fi

  release_file="${workdir}/release.json"
  index_file="${workdir}/platforms.tsv"
  manifest_file="${workdir}/manifest.json"
  latest_file="${workdir}/latest"

  gh_release_json "${repo}" "${requested_tag}" > "${release_file}"

  tag="$("${PYTHON_BIN}" - "${release_file}" <<'PY'
import json
import sys
with open(sys.argv[1], "r", encoding="utf-8") as fh:
    data = json.load(fh)
print(data["tagName"])
PY
)"
  version="$(normalize_app_version "${app_id}" "${tag}")"
  info "syncing ${app_id} version ${version} from ${repo}@${tag}"

  "${PYTHON_BIN}" - "${app_id}" "${release_file}" "${index_file}" <<'PY'
import json
import sys

app_id, release_path, index_path = sys.argv[1:4]
with open(release_path, "r", encoding="utf-8") as fh:
    release = json.load(fh)

assets = {asset["name"]: asset for asset in release.get("assets", [])}
tag = release["tagName"]

config = {
    "open-code": [
        ("darwin-arm64", "opencode-darwin-arm64.zip", "zip", "opencode"),
        ("darwin-x64", "opencode-darwin-x64-baseline.zip", "zip", "opencode"),
        ("linux-arm64", "opencode-linux-arm64.tar.gz", "tar.gz", "opencode"),
        ("linux-arm64-musl", "opencode-linux-arm64-musl.tar.gz", "tar.gz", "opencode"),
        ("linux-x64", "opencode-linux-x64-baseline.tar.gz", "tar.gz", "opencode"),
        ("linux-x64-musl", "opencode-linux-x64-baseline-musl.tar.gz", "tar.gz", "opencode"),
        ("win32-arm64", "opencode-windows-arm64.zip", "zip", "opencode.exe"),
        ("win32-x64", "opencode-windows-x64-baseline.zip", "zip", "opencode.exe"),
    ],
    "open-code-review": [
        ("darwin-arm64", "opencodereview-darwin-arm64", "binary", "ocr"),
        ("darwin-x64", "opencodereview-darwin-amd64", "binary", "ocr"),
        ("linux-arm64", "opencodereview-linux-arm64", "binary", "ocr"),
        ("linux-x64", "opencodereview-linux-amd64", "binary", "ocr"),
        ("win32-arm64", "opencodereview-windows-arm64.exe", "binary", "ocr.exe"),
        ("win32-x64", "opencodereview-windows-amd64.exe", "binary", "ocr.exe"),
    ],
    "codex": [
        ("darwin-arm64", "codex-aarch64-apple-darwin.tar.gz", "tar.gz", "codex-aarch64-apple-darwin"),
        ("darwin-x64", "codex-x86_64-apple-darwin.tar.gz", "tar.gz", "codex-x86_64-apple-darwin"),
        ("linux-arm64", "codex-aarch64-unknown-linux-musl.tar.gz", "tar.gz", "codex-aarch64-unknown-linux-musl"),
        ("linux-x64", "codex-x86_64-unknown-linux-musl.tar.gz", "tar.gz", "codex-x86_64-unknown-linux-musl"),
        ("win32-arm64", "codex-aarch64-pc-windows-msvc.exe.zip", "zip", "codex-aarch64-pc-windows-msvc.exe"),
        ("win32-x64", "codex-x86_64-pc-windows-msvc.exe.zip", "zip", "codex-x86_64-pc-windows-msvc.exe"),
    ],
}

rows = config.get(app_id)
if not rows:
    raise SystemExit(f"unsupported app config: {app_id}")

with open(index_path, "w", encoding="utf-8") as out:
    for platform, asset_name, archive_format, binary in rows:
        asset = assets.get(asset_name)
        if not asset:
            raise SystemExit(f"missing expected asset for {app_id}: {asset_name}")
        digest = asset.get("digest", "")
        if not digest.startswith("sha256:"):
            raise SystemExit(f"missing sha256 digest for {asset_name}")
        checksum = digest.split(":", 1)[1]
        size = asset.get("size", "")
        download_url = asset.get("url")
        if not download_url:
            raise SystemExit(f"missing download url for {asset_name}")
        out.write(f"{platform}\t{asset_name}\t{archive_format}\t{binary}\t{checksum}\t{size}\t{download_url}\n")
PY

  while IFS=$'\t' read -r platform asset_name archive_format binary checksum expected_size download_url; do
    [[ -n "$platform" ]] || continue

    artifact_path="${workdir}/${asset_name}"
    object_key="${prefix}/${version}/${platform}/${asset_name}"

    info "downloading ${app_id} ${platform}/${asset_name}"
    download_file "${download_url}" "${artifact_path}"

    actual_checksum="$(sha256_file "${artifact_path}")"
    if [[ "${actual_checksum}" != "${checksum}" ]]; then
      die "checksum mismatch for ${app_id} ${platform}/${asset_name}: expected ${checksum}, got ${actual_checksum}"
    fi

    actual_size="$("${PYTHON_BIN}" - "${artifact_path}" <<'PY'
import os
import sys
print(os.path.getsize(sys.argv[1]))
PY
)"
    if [[ -n "${expected_size}" && "${actual_size}" != "${expected_size}" ]]; then
      die "size mismatch for ${app_id} ${platform}/${asset_name}: expected ${expected_size}, got ${actual_size}"
    fi

    info "uploading ${object_key}"
    oss_put_object "${artifact_path}" "${object_key}" "application/octet-stream"
  done < "${index_file}"

  "${PYTHON_BIN}" - "${app_id}" "${release_file}" "${index_file}" "${manifest_file}" "${version}" "${tag}" "${prefix}" "${repo}" "${public_base_url}" <<'PY'
import json
import sys
from datetime import datetime, timezone

app_id, release_path, index_path, manifest_path, version, tag, prefix, repo, public_base_url = sys.argv[1:10]
with open(release_path, "r", encoding="utf-8") as fh:
    release = json.load(fh)

manifest = {
    "app_id": app_id,
    "version": version,
    "release_tag": tag,
    "source": "github-release",
    "repository": repo,
    "prefix": prefix,
    "public_base_url": public_base_url,
    "synced_at": datetime.now(timezone.utc).isoformat(),
    "platforms": {},
}

with open(index_path, "r", encoding="utf-8") as fh:
    for raw in fh:
        raw = raw.strip()
        if not raw:
            continue
        platform, asset_name, archive_format, binary, checksum, size, download_url = raw.split("\t")
        entry = {
            "asset": asset_name,
            "archive_format": archive_format,
            "binary": binary,
            "checksum": checksum,
            "size": int(size),
            "path": f"{version}/{platform}/{asset_name}",
            "source_url": download_url,
            "public_url": f"{public_base_url}/{version}/{platform}/{asset_name}",
        }
        manifest["platforms"][platform] = entry

with open(manifest_path, "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

  info "uploading ${prefix}/${version}/manifest.json"
  oss_put_object "${manifest_file}" "${prefix}/${version}/manifest.json" "application/json"

  if [[ "${UPDATE_LATEST}" == "1" ]]; then
    printf '%s\n' "${version}" > "${latest_file}"
    info "uploading ${prefix}/latest"
    oss_put_object "${latest_file}" "${prefix}/latest" "text/plain"
  fi

  info "${app_id} ${version} mirror is ready"
  info "public base URL: ${public_base_url}"
  info "manifest URL: ${public_base_url}/${version}/manifest.json"

  if [[ "${KEEP_WORKDIR}" != "1" ]]; then
    rm -rf "${workdir}"
  fi
}

sync_codex_app() {
  local prefix public_base_url workdir version manifest_file latest_file index_file
  prefix="$(trim_trailing_slash "${STARHUB_CODEX_APP_DIST_PREFIX:-codex-app-releases}")"
  public_base_url="$(resolve_public_base_url "$prefix" "${STARHUB_CODEX_APP_DIST_BASE_URL:-}")"
  workdir="$(mktemp -d "${TMPDIR:-/tmp}/codex-app-sync.XXXXXX")"
  manifest_file="${workdir}/manifest.json"
  latest_file="${workdir}/latest"
  index_file="${workdir}/index.tsv"

  info "syncing codex-app desktop installers"

  version="$("${PYTHON_BIN}" - "${index_file}" <<'PY'
import json
import subprocess
import sys
import urllib.request
import xml.etree.ElementTree as ET

index_path = sys.argv[1]
sparkle_ns = {"sparkle": "http://www.andymatuschak.org/xml-namespaces/sparkle"}


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "csghub-lite-sync"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        return resp.read()


def appcast_entry(url):
    root = ET.fromstring(fetch(url))
    item = root.find("./channel/item")
    if item is None:
        raise SystemExit(f"appcast has no item: {url}")
    enclosure = item.find("enclosure")
    if enclosure is None:
        raise SystemExit(f"appcast item has no enclosure: {url}")
    download_url = enclosure.attrib.get("url", "").strip()
    if not download_url:
        raise SystemExit(f"appcast enclosure missing url: {url}")
    return {
        "version": (item.findtext("sparkle:shortVersionString", namespaces=sparkle_ns) or "").strip(),
        "url": download_url,
        "asset": download_url.rsplit("/", 1)[-1],
    }


def gh_desktop_exe(repo, arch_asset):
    payload = subprocess.check_output(
        ["gh", "release", "view", "--repo", repo, "--json", "tagName,assets"],
        text=True,
    )
    release = json.loads(payload)
    assets = release.get("assets") or []
    candidates = [asset for asset in assets if asset.get("name") == arch_asset]
    if not candidates:
        raise SystemExit(f"missing expected desktop asset in {release.get('tagName')}: {arch_asset}")
    asset = candidates[0]
    size = int(asset.get("size") or 0)
    if size < 150_000_000:
        raise SystemExit(f"asset looks too small for Codex App desktop build: {arch_asset} ({size} bytes)")
    return {
        "url": asset.get("url"),
        "asset": arch_asset,
    }


entries = []
mac_arm = appcast_entry("https://persistent.oaistatic.com/codex-app-prod/appcast.xml")
mac_x64 = appcast_entry("https://persistent.oaistatic.com/codex-app-prod/appcast-x64.xml")
version = mac_arm["version"] or mac_x64["version"]
if not version:
    raise SystemExit("failed to resolve Codex App version from macOS appcasts")
if mac_x64["version"] and mac_x64["version"] != version:
    raise SystemExit(f"macOS appcast versions differ: arm64={version} x64={mac_x64['version']}")

entries.append(("darwin-arm64", mac_arm["asset"], "zip", "Codex.app", mac_arm["url"]))
entries.append(("darwin-x64", mac_x64["asset"], "zip", "Codex.app", mac_x64["url"]))

for platform, asset_name in (
    ("win32-arm64", "codex-aarch64-pc-windows-msvc.exe"),
    ("win32-x64", "codex-x86_64-pc-windows-msvc.exe"),
):
    win = gh_desktop_exe("openai/codex", asset_name)
    entries.append((platform, asset_name, "exe", asset_name, win["url"]))

with open(index_path, "w", encoding="utf-8") as index_out:
    for platform, asset_name, archive_format, binary, download_url in entries:
        index_out.write(f"{platform}\t{asset_name}\t{archive_format}\t{binary}\t{download_url}\n")

print(version)
PY
)"

  [[ -n "${version}" ]] || die "failed to resolve codex-app version"

  : > "${workdir}/uploaded.tsv"

  while IFS=$'\t' read -r platform asset_name archive_format binary download_url; do
    [[ -n "$platform" ]] || continue

    artifact_path="${workdir}/${asset_name}"
    object_key="${prefix}/${version}/${platform}/${asset_name}"
    mirror_url="${public_base_url}/${version}/${platform}/${asset_name}"

    if curl -fsSI "${mirror_url}" >/dev/null 2>&1; then
      info "reusing mirrored codex-app ${platform}/${asset_name} for checksum"
      download_file "${mirror_url}" "${artifact_path}"
    else
      info "downloading codex-app ${platform}/${asset_name}"
      download_file "${download_url}" "${artifact_path}"
      info "uploading ${object_key}"
      oss_put_object "${artifact_path}" "${object_key}" "application/octet-stream"
    fi

    checksum="$(sha256_file "${artifact_path}")"
    actual_size="$("${PYTHON_BIN}" - "${artifact_path}" <<'PY'
import os
import sys
print(os.path.getsize(sys.argv[1]))
PY
)"

    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "${platform}" "${asset_name}" "${archive_format}" "${binary}" \
      "${checksum}" "${actual_size}" "${download_url}" >> "${workdir}/uploaded.tsv"
  done < "${index_file}"

  "${PYTHON_BIN}" - "${workdir}/uploaded.tsv" "${manifest_file}" "${version}" "${prefix}" "${public_base_url}" <<'PY'
import json
import sys
from datetime import datetime, timezone

index_path, manifest_path, version, prefix, public_base_url = sys.argv[1:6]
manifest = {
    "app_id": "codex-app",
    "version": version,
    "source": "codex-app-prod",
    "prefix": prefix,
    "public_base_url": public_base_url,
    "synced_at": datetime.now(timezone.utc).isoformat(),
    "platforms": {},
}
with open(index_path, "r", encoding="utf-8") as fh:
    for raw in fh:
        raw = raw.strip()
        if not raw:
            continue
        platform, asset_name, archive_format, binary, checksum, size, download_url = raw.split("\t")
        manifest["platforms"][platform] = {
            "asset": asset_name,
            "archive_format": archive_format,
            "binary": binary,
            "checksum": checksum,
            "size": int(size),
            "path": f"{version}/{platform}/{asset_name}",
            "source_url": download_url,
            "public_url": f"{public_base_url}/{version}/{platform}/{asset_name}",
        }
with open(manifest_path, "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

  info "uploading ${prefix}/${version}/manifest.json"
  oss_put_object "${manifest_file}" "${prefix}/${version}/manifest.json" "application/json"

  if [[ "${UPDATE_LATEST}" == "1" ]]; then
    printf '%s\n' "${version}" > "${latest_file}"
    info "uploading ${prefix}/latest"
    oss_put_object "${latest_file}" "${prefix}/latest" "text/plain"
  fi

  info "codex-app ${version} mirror is ready"
  info "public base URL: ${public_base_url}"
  info "manifest URL: ${public_base_url}/${version}/manifest.json"

  if [[ "${KEEP_WORKDIR}" != "1" ]]; then
    rm -rf "${workdir}"
  fi
}

require_cmd python3
require_cmd gh
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

[[ -n "$STARHUB_OSS_ACCESS_KEY_ID" ]] || die "STARHUB_OSS_ACCESS_KEY_ID is required"
[[ -n "$STARHUB_OSS_ACCESS_KEY_SECRET" ]] || die "STARHUB_OSS_ACCESS_KEY_SECRET is required"
[[ -n "$STARHUB_OSS_ENDPOINT" ]] || die "STARHUB_OSS_ENDPOINT is required"
[[ -n "$STARHUB_OSS_PUBLIC_BUCKET" ]] || die "STARHUB_OSS_PUBLIC_BUCKET is required"

for app_id in "${APP_IDS[@]}"; do
  case "${app_id}" in
    claude-code)
      sync_claude_via_wrapper
      ;;
    open-code|open-code-review|codex)
      sync_release_app "${app_id}"
      ;;
    codex-app)
      sync_codex_app
      ;;
    *)
      die "unsupported app: ${app_id}"
      ;;
  esac
done
