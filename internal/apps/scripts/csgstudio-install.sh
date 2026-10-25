#!/usr/bin/env bash
set -euo pipefail

DEFAULT_MANIFEST_URL="https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/csgstudio/channels/release/downloads.json"
MANIFEST_URL="${CSGHUB_LITE_CSGSTUDIO_MANIFEST_URL:-$DEFAULT_MANIFEST_URL}"
RUNTIME_ROOT="${HOME}/.local/share/csgstudio"
WORKDIR=""
MOUNT_DIR=""
STAGING_TARGET=""

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

cleanup() {
  if [[ -n "$MOUNT_DIR" && -d "$MOUNT_DIR" ]]; then
    hdiutil detach "$MOUNT_DIR" >/dev/null 2>&1 || true
    rmdir "$MOUNT_DIR" >/dev/null 2>&1 || true
  fi
  if [[ -n "$STAGING_TARGET" && -d "$STAGING_TARGET" ]]; then
    rm -rf "$STAGING_TARGET"
  fi
  if [[ -n "$WORKDIR" && -d "$WORKDIR" ]]; then
    rm -rf "$WORKDIR"
  fi
}

trap cleanup EXIT

json_field() {
  local json="$1"
  local field="$2"
  printf '%s' "$json" | sed -n "s/.*\"${field}\":\"\\([^\"]*\\)\".*/\\1/p" | sed -n '1p'
}

# Prints the artifact object of the latest release matching platform and one of
# the accepted arch spellings. The manifest is flattened first so the match does
# not depend on its formatting.
artifact_entry() {
  local manifest="$1"
  local version="$2"
  local platform="$3"
  shift 3
  local flat
  flat="$(printf '%s' "$manifest" | tr -d '\n\r\t ')"
  local release
  release="$(printf '%s' "$flat" |
    sed 's/"version":"/\
&/g' |
    awk -v version="$version" 'index($0, "\"version\":\"" version "\"") == 1 { print; exit }')"
  local arch
  for arch in "$@"; do
    local entry
    entry="$(printf '%s' "$release" |
      sed 's/}[,\]]*{/}\
{/g' |
      awk -v platform="$platform" -v arch="$arch" '
        index($0, "\"platform\":\"" platform "\"") && index($0, "\"arch\":\"" arch "\"") { print; exit }
      ')"
    if [[ -n "$entry" ]]; then
      printf '%s' "$entry"
      return
    fi
  done
}

# /Applications is group-writable for admin users, so installing there needs no
# sudo. Standard users fall back to ~/Applications, which Finder and Launchpad
# also index.
darwin_applications_dir() {
  if [[ -n "${CSGHUB_LITE_CSGSTUDIO_APPLICATIONS_DIR:-}" ]]; then
    mkdir -p "$CSGHUB_LITE_CSGSTUDIO_APPLICATIONS_DIR"
    printf '%s' "$CSGHUB_LITE_CSGSTUDIO_APPLICATIONS_DIR"
  elif [[ -d /Applications && -w /Applications ]]; then
    printf '%s' /Applications
  else
    mkdir -p "${HOME}/Applications"
    printf '%s' "${HOME}/Applications"
  fi
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

emit_progress 10 detecting_platform
case "$(uname -s)" in
  Darwin) os="darwin"; platform="macos" ;;
  Linux) os="linux"; platform="linux" ;;
  *)
    printf 'ERROR: unsupported operating system: %s\n' "$(uname -s)"
    exit 1
    ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch_names=(arm64 aarch64) ;;
  x86_64|amd64) arch_names=(x86_64 amd64 x64) ;;
  *)
    printf 'ERROR: unsupported architecture: %s\n' "$(uname -m)"
    exit 1
    ;;
esac

required_commands=(curl)
if [[ "$os" == "darwin" ]]; then
  required_commands+=(hdiutil ditto)
fi
for command_name in "${required_commands[@]}"; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'ERROR: missing required command: %s\n' "$command_name"
    exit 1
  fi
done

emit_progress 25 resolving_latest
manifest="$(curl --connect-timeout 15 --max-time 60 --retry 3 -fsSL "$MANIFEST_URL")"
version="$(json_field "$(printf '%s' "$manifest" | tr -d '\n\r\t ')" latest)"
entry=""
if [[ -n "$version" ]]; then
  entry="$(artifact_entry "$manifest" "$version" "$platform" "${arch_names[@]}")"
fi
url="$(json_field "$entry" url)"
checksum="$(json_field "$entry" sha256)"
if [[ -z "$version" || -z "$url" ]]; then
  printf 'ERROR: CSGStudio %s does not publish a %s/%s package in %s\n' "${version:-latest}" "$platform" "${arch_names[0]}" "$MANIFEST_URL"
  exit 1
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/csgstudio-install.XXXXXX")"
package_path="${WORKDIR}/$(basename "${url%%\?*}")"

emit_progress 55 downloading_archive
printf 'INFO: downloading CSGStudio %s for %s/%s\n' "$version" "$platform" "${arch_names[0]}"
curl --connect-timeout 15 --max-time 3600 --retry 3 --retry-delay 2 -fsSL "$url" -o "$package_path"

emit_progress 75 verifying_checksum
if [[ -n "$checksum" ]]; then
  actual="$(sha256_file "$package_path")"
  if [[ "$actual" != "$checksum" ]]; then
    printf 'ERROR: checksum verification failed\n'
    exit 1
  fi
else
  printf 'WARN: downloads.json has no sha256 for this package; skipping checksum verification\n'
fi

version_dir="${RUNTIME_ROOT}/versions/${version}"
case "$os" in
  darwin)
    emit_progress 85 mounting_installer
    MOUNT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/csgstudio-mount.XXXXXX")"
    hdiutil attach "$package_path" -nobrowse -readonly -mountpoint "$MOUNT_DIR" >/dev/null
    app_bundle="$(printf '%s\n' "$MOUNT_DIR"/*.app | sed -n '1p')"
    if [[ ! -d "$app_bundle" ]]; then
      printf 'ERROR: CSGStudio app bundle was not found in the DMG\n'
      exit 1
    fi

    emit_progress 90 installing_runtime
    applications_dir="$(darwin_applications_dir)"
    installed_target="${applications_dir}/$(basename "$app_bundle")"
    STAGING_TARGET="${installed_target}.installing.$$"
    rm -rf "$STAGING_TARGET"
    ditto "$app_bundle" "$STAGING_TARGET"
    xattr -cr "$STAGING_TARGET" >/dev/null 2>&1 || true
    if [[ -d "$installed_target" ]]; then
      app_name="$(basename "$installed_target" .app)"
      osascript -e "if application \"${app_name}\" is running then tell application \"${app_name}\" to quit" >/dev/null 2>&1 || true
      rm -rf "$installed_target"
    fi
    mv "$STAGING_TARGET" "$installed_target"
    STAGING_TARGET=""
    mkdir -p "$RUNTIME_ROOT"
    printf '%s\n' "$installed_target" > "${RUNTIME_ROOT}/managed-bundle"
    # Earlier releases kept the bundle under the runtime root, where Finder and
    # Launchpad never list it.
    rm -rf "${RUNTIME_ROOT}/versions" "${RUNTIME_ROOT}/current"
    ;;
  linux)
    case "$(printf '%s' "$package_path" | tr '[:upper:]' '[:lower:]')" in
      *.appimage) ;;
      *)
        printf 'ERROR: unsupported CSGStudio Linux package: %s\n' "$(basename "$package_path")"
        exit 1
        ;;
    esac

    emit_progress 90 installing_runtime
    installed_target="${version_dir}/CSGStudio.AppImage"
    rm -rf "$version_dir"
    mkdir -p "$version_dir"
    cp "$package_path" "$installed_target"
    chmod 0755 "$installed_target"
    ;;
esac

mkdir -p "$RUNTIME_ROOT"
printf '%s\n' "$version" > "${RUNTIME_ROOT}/version"
printf '%s\n' "$installed_target" > "${RUNTIME_ROOT}/launch-target"
if [[ "$os" == "linux" ]]; then
  ln -sfn "$version_dir" "${RUNTIME_ROOT}/current"
fi

emit_progress 100 complete
printf 'INFO: installed CSGStudio %s to %s\n' "$version" "$installed_target"
