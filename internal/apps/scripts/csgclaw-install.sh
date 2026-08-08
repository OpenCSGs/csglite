#!/usr/bin/env bash
set -euo pipefail

DEFAULT_MANIFEST_URL="https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/csgclaw-desktop/channels/release/downloads.json"
MANIFEST_URL="${CSGHUB_LITE_CSGCLAW_DESKTOP_MANIFEST_URL:-$DEFAULT_MANIFEST_URL}"
RUNTIME_ROOT="${HOME}/.local/share/csgclaw-desktop"
WORKDIR=""
MOUNT_DIR=""

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

cleanup() {
  if [[ -n "$MOUNT_DIR" && -d "$MOUNT_DIR" ]]; then
    hdiutil detach "$MOUNT_DIR" >/dev/null 2>&1 || true
    rmdir "$MOUNT_DIR" >/dev/null 2>&1 || true
  fi
  if [[ -n "$WORKDIR" && -d "$WORKDIR" ]]; then
    rm -rf "$WORKDIR"
  fi
}

trap cleanup EXIT

json_field() {
  local json="$1"
  local field="$2"
  printf '%s' "$json" | sed -n "s/.*\"${field}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" | sed -n '1p'
}

artifact_entry() {
  local manifest="$1"
  local arch="$2"
  printf '%s' "$manifest" |
    tr -d '\n\r\t' |
    sed 's/}[[:space:]]*,[[:space:]]*{/}\
{/g' |
    awk -v arch="$arch" '
      index($0, "\"platform\": \"macos\"") && index($0, "\"arch\": \"" arch "\"") { print; exit }
    '
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

if [[ "$(uname -s)" != "Darwin" ]]; then
  printf 'ERROR: CSGClaw Desktop installation is only supported on macOS and Windows\n'
  exit 1
fi

for command_name in curl hdiutil shasum; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'ERROR: missing required command: %s\n' "$command_name"
    exit 1
  fi
done

emit_progress 10 detecting_platform
case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64) arch="x86_64" ;;
  *)
    printf 'ERROR: unsupported macOS architecture: %s\n' "$(uname -m)"
    exit 1
    ;;
esac

emit_progress 25 resolving_latest
manifest="$(curl --connect-timeout 15 --max-time 60 --retry 3 -fsSL "$MANIFEST_URL")"
version="$(json_field "$manifest" latest)"
entry="$(artifact_entry "$manifest" "$arch")"
url="$(json_field "$entry" url)"
checksum="$(json_field "$entry" sha256)"
if [[ -z "$version" || -z "$url" || -z "$checksum" ]]; then
  printf 'ERROR: no CSGClaw Desktop artifact for macOS/%s in %s\n' "$arch" "$MANIFEST_URL"
  exit 1
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/csgclaw-desktop-install.XXXXXX")"
dmg_path="${WORKDIR}/csgclaw-desktop.dmg"

emit_progress 55 downloading_archive
printf 'INFO: downloading CSGClaw Desktop %s for macOS/%s\n' "$version" "$arch"
curl --connect-timeout 15 --max-time 3600 --retry 3 --retry-delay 2 -fsSL "$url" -o "$dmg_path"

emit_progress 75 verifying_checksum
actual="$(sha256_file "$dmg_path")"
if [[ "$actual" != "$checksum" ]]; then
  printf 'ERROR: checksum verification failed\n'
  exit 1
fi

emit_progress 85 mounting_installer
MOUNT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/csgclaw-desktop-mount.XXXXXX")"
hdiutil attach "$dmg_path" -nobrowse -readonly -mountpoint "$MOUNT_DIR" >/dev/null
app_bundle="$(printf '%s\n' "$MOUNT_DIR"/*.app | sed -n '1p')"
if [[ ! -d "$app_bundle" ]]; then
  printf 'ERROR: CSGClaw Desktop app bundle was not found in the DMG\n'
  exit 1
fi

emit_progress 90 installing_runtime
version_dir="${RUNTIME_ROOT}/versions/${version}"
installed_app="${version_dir}/$(basename "$app_bundle")"
rm -rf "$version_dir"
mkdir -p "$version_dir"
cp -R "$app_bundle" "$version_dir/"
xattr -cr "$installed_app" >/dev/null 2>&1 || true
printf '%s\n' "$version" > "${RUNTIME_ROOT}/version"
printf '%s\n' "$installed_app" > "${RUNTIME_ROOT}/launch-target"
ln -sfn "$version_dir" "${RUNTIME_ROOT}/current"

emit_progress 100 complete
printf 'INFO: installed CSGClaw Desktop %s to %s\n' "$version" "$installed_app"
