#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-latest}"
SITE_URL="${CSGHUB_LITE_ZCODE_SITE_URL:-https://zcode.z.ai/en/changelog}"
DIST_BASE_URL="${CSGHUB_LITE_ZCODE_DIST_BASE_URL:-https://cdn-zcode.z.ai/zcode/electron/releases}"
TMP_ROOT="${CSGHUB_LITE_TMPDIR:-${HOME}/.csghub-lite/tmp/apps/zcode}"
RUNTIME_ROOT="${HOME}/.local/share/zcode"
WORKDIR=""
DOWNLOADER=""

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$WORKDIR" && -d "$WORKDIR" ]]; then
    rm -rf "$WORKDIR"
  fi
}
trap cleanup EXIT

trim_trailing_slash() {
  local value="$1"
  while [[ "$value" == */ ]]; do value="${value%/}"; done
  printf '%s' "$value"
}

select_downloader() {
  if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
  elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
  else
    die "curl or wget is required"
  fi
}

download_text() {
  local url="$1"
  if [[ "$DOWNLOADER" == "curl" ]]; then
    curl --connect-timeout 15 --max-time 60 --retry 3 --retry-delay 2 -fsSL "$url"
  else
    wget --tries=3 --timeout=20 -q -O - "$url"
  fi
}

download_file() {
  local url="$1" output="$2"
  if [[ "$DOWNLOADER" == "curl" ]]; then
    curl --connect-timeout 15 --max-time 3600 --retry 3 --retry-delay 2 -fsSL -o "$output" "$url"
  else
    wget --tries=3 --timeout=30 -O "$output" "$url"
  fi
}

try_download_file() {
  local url="$1" output="$2"
  if [[ "$DOWNLOADER" == "curl" ]]; then
    curl --connect-timeout 15 --max-time 60 --retry 2 --retry-delay 1 -fsSL -o "$output" "$url"
  else
    wget --tries=2 --timeout=20 -q -O "$output" "$url"
  fi
}

resolve_version() {
  local requested="${TARGET#v}" html version
  if [[ -n "$requested" && "$requested" != "latest" ]]; then
    printf '%s' "$requested"
    return
  fi
  html="$(download_text "$SITE_URL")"
  version="$(
    printf '%s' "$html" |
      grep -Eo 'Release[[:space:]]+v?[0-9]+\.[0-9]+\.[0-9]+' |
      grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' |
      awk 'NR == 1 { first=$0 } END { print first }' || true
  )"
  if [[ -z "$version" ]]; then
    version="$(printf '%s' "$html" | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | awk 'NR == 1 { first=$0 } END { print first }' || true)"
  fi
  [[ -n "$version" ]] || die "could not parse the latest ZCode version from ${SITE_URL}"
  printf '%s' "$version"
}

file_size() {
  wc -c < "$1" | tr -d '[:space:]'
}

manifest_fields() {
  local manifest="$1" asset="$2"
  awk -v wanted="$asset" '
    function clean(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      first=substr(value, 1, 1)
      last=substr(value, length(value), 1)
      quote=sprintf("%c", 39)
      if ((first == "\"" && last == "\"") || (first == quote && last == quote))
        value=substr(value, 2, length(value) - 2)
      return value
    }
    /^[[:space:]]*-[[:space:]]+url:[[:space:]]*/ {
      value=$0
      sub(/^[^:]*:[[:space:]]*/, "", value)
      active=(clean(value) == wanted)
      next
    }
    active && /^[[:space:]]*sha512:[[:space:]]*/ {
      value=$0; sub(/^[^:]*:[[:space:]]*/, "", value); sha=clean(value)
    }
    active && /^[[:space:]]*size:[[:space:]]*/ {
      value=$0; sub(/^[^:]*:[[:space:]]*/, "", value); size=clean(value)
    }
    END { if (sha != "" && size != "") print sha "\t" size }
  ' "$manifest"
}

sha512_base64() {
  local path="$1"
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha512 -binary "$path" | openssl base64 -A
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$path" <<'PY'
import base64
import hashlib
import sys

digest = hashlib.sha512()
with open(sys.argv[1], "rb") as stream:
    for chunk in iter(lambda: stream.read(1024 * 1024), b""):
        digest.update(chunk)
print(base64.b64encode(digest.digest()).decode("ascii"), end="")
PY
  else
    die "openssl or python3 is required to verify SHA-512 metadata"
  fi
}

verify_asset() {
  local asset_path="$1" asset_name="$2"
  local manifest_path="${WORKDIR}/latest.yml" fields expected_sha expected_size actual_size actual_sha
  actual_size="$(file_size "$asset_path")"
  [[ "$actual_size" =~ ^[0-9]+$ && "$actual_size" -gt 0 ]] || die "downloaded asset is empty"

  if try_download_file "${RELEASE_URL}/latest.yml" "$manifest_path" 2>/dev/null; then
    fields="$(manifest_fields "$manifest_path" "$asset_name")"
  fi
  if [[ -z "${fields:-}" ]] &&
     try_download_file "${LEGACY_RELEASE_URL}/${LEGACY_MANIFEST_NAME}" "$manifest_path" 2>/dev/null; then
    fields="$(manifest_fields "$manifest_path" "$asset_name")"
  fi
  if [[ -n "${fields:-}" ]]; then
    IFS=$'\t' read -r expected_sha expected_size <<< "$fields"
    [[ "$actual_size" == "$expected_size" ]] || die "asset size verification failed"
    actual_sha="$(sha512_base64 "$asset_path")"
    [[ "$actual_sha" == "$expected_sha" ]] || die "asset SHA-512 verification failed"
    printf 'INFO: verified size and SHA-512 from latest.yml\n'
    return
  fi
  printf 'INFO: no matching checksum metadata; validated the non-empty asset size\n'
}

write_launcher() {
  local launcher="$1" platform="$2"
  if [[ "$platform" == "mac" ]]; then
    cat > "$launcher" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
target="$(<"${HOME}/.local/share/zcode/launch-target")"
[[ -d "$target" ]] || { printf 'ERROR: ZCode launch target is missing\n' >&2; exit 1; }
exec open "$target" --args "$@"
EOF
  else
    cat > "$launcher" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
target="$(<"${HOME}/.local/share/zcode/launch-target")"
[[ -f "$target" && -x "$target" ]] || { printf 'ERROR: ZCode launch target is missing\n' >&2; exit 1; }
exec "$target" "$@"
EOF
  fi
  chmod +x "$launcher"
}

[[ -n "${HOME:-}" ]] || die "HOME is not set"
mkdir -p "$TMP_ROOT"
export TMPDIR="$TMP_ROOT" TMP="$TMP_ROOT" TEMP="$TMP_ROOT"
WORKDIR="$(mktemp -d "${TMP_ROOT%/}/zcode-install.XXXXXX")"
select_downloader

emit_progress 10 detecting_platform
case "$(uname -s)" in
  Darwin) PLATFORM="mac"; RELEASE_PLATFORM="macos"; LEGACY_MANIFEST_NAME="latest.yml"; EXTENSION="zip" ;;
  Linux) PLATFORM="linux"; RELEASE_PLATFORM="linux"; LEGACY_MANIFEST_NAME="latest-linux.yml"; EXTENSION="AppImage" ;;
  *) die "unsupported operating system $(uname -s)" ;;
esac
case "$(uname -m)" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64) ARCH="x64" ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
if [[ "$PLATFORM" == "mac" && "$ARCH" == "x64" ]] &&
   [[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" == "1" ]]; then
  ARCH="arm64"
fi

emit_progress 25 resolving_latest
VERSION="$(resolve_version)"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid ZCode version: ${VERSION}"
ASSET_NAME="ZCode-${VERSION}-${PLATFORM}-${ARCH}.${EXTENSION}"
ASSET_PATH="${WORKDIR}/${ASSET_NAME}"
VERSION_DIR="${RUNTIME_ROOT}/versions/${VERSION}"
LAUNCHER_DIR="${HOME}/.local/bin"
LEGACY_RELEASE_URL="$(trim_trailing_slash "$DIST_BASE_URL")/${VERSION}"
RELEASE_URL="${LEGACY_RELEASE_URL}/${RELEASE_PLATFORM}-${ARCH}"

emit_progress 50 downloading_asset
printf 'INFO: downloading %s\n' "$ASSET_NAME"
if ! try_download_file "${RELEASE_URL}/${ASSET_NAME}" "$ASSET_PATH" 2>/dev/null; then
  printf 'INFO: platform release path unavailable; trying legacy release path\n'
  download_file "${LEGACY_RELEASE_URL}/${ASSET_NAME}" "$ASSET_PATH"
fi

emit_progress 70 verifying_asset
verify_asset "$ASSET_PATH" "$ASSET_NAME"

emit_progress 85 installing_runtime
mkdir -p "${RUNTIME_ROOT}/versions" "$LAUNCHER_DIR"
rm -rf "$VERSION_DIR"
mkdir -p "$VERSION_DIR"

if [[ "$PLATFORM" == "mac" ]]; then
  command -v ditto >/dev/null 2>&1 || die "ditto is required to install the ZIP"
  ditto -x -k "$ASSET_PATH" "$VERSION_DIR"
  [[ -d "${VERSION_DIR}/ZCode.app" && -x "${VERSION_DIR}/ZCode.app/Contents/MacOS/ZCode" ]] ||
    die "the ZIP does not contain a runnable ZCode.app"
  LAUNCH_TARGET="${VERSION_DIR}/ZCode.app"
  xattr -cr "$LAUNCH_TARGET" >/dev/null 2>&1 || true
else
  magic="$(od -An -tx1 -N4 "$ASSET_PATH" | tr -d '[:space:]')"
  [[ "$magic" == "7f454c46" ]] || die "the downloaded AppImage is not an ELF executable"
  LAUNCH_TARGET="${VERSION_DIR}/${ASSET_NAME}"
  mv "$ASSET_PATH" "$LAUNCH_TARGET"
  chmod +x "$LAUNCH_TARGET"
  [[ -x "$LAUNCH_TARGET" ]] || die "installed AppImage is not executable"
fi

printf '%s\n' "$VERSION" > "${RUNTIME_ROOT}/version"
printf '%s\n' "$LAUNCH_TARGET" > "${RUNTIME_ROOT}/launch-target"
ln -sfn "$VERSION_DIR" "${RUNTIME_ROOT}/current"
write_launcher "${LAUNCHER_DIR}/zcode" "$PLATFORM"

emit_progress 100 complete
printf 'INFO: installed ZCode %s to %s\n' "$VERSION" "$VERSION_DIR"
