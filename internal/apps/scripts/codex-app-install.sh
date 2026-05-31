#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-latest}"
DEFAULT_DIST_BASE_URL="https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/codex-app-releases"
DIST_BASE_URL="${CSGHUB_LITE_CODEX_APP_DIST_BASE_URL:-$DEFAULT_DIST_BASE_URL}"
WORKDIR=""
DOWNLOADER=""

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

log() {
  printf '%s\n' "$*"
}

cleanup() {
  if [[ -n "${WORKDIR}" && -d "${WORKDIR}" ]]; then
    rm -rf "${WORKDIR}"
  fi
}

trap cleanup EXIT

trim_trailing_slash() {
  local value="$1"
  while [[ "$value" == */ ]]; do
    value="${value%/}"
  done
  printf '%s\n' "$value"
}

resolve_requested_version() {
  local requested="${TARGET:-latest}"
  requested="${requested#v}"
  if [[ -z "$requested" || "$requested" == "latest" ]]; then
    download_text "$(trim_trailing_slash "$DIST_BASE_URL")/latest" | tr -d '[:space:]'
    return 0
  fi
  printf '%s\n' "$requested"
}

select_downloader() {
  if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
    return 0
  fi
  if command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
    return 0
  fi
  log "ERROR: either curl or wget is required"
  exit 1
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
  local url="$1"
  local output="$2"
  if [[ "$DOWNLOADER" == "curl" ]]; then
    curl --connect-timeout 15 --max-time 3600 --retry 3 --retry-delay 2 -fsSL -o "$output" "$url"
  else
    wget --tries=3 --timeout=30 -O "$output" "$url"
  fi
}

get_platform_entry_from_manifest() {
  local json="$1"
  local platform="$2"
  json="$(printf '%s' "$json" | tr -d '\n\r\t' | sed 's/ \+/ /g')"
  if [[ $json =~ \"$platform\"[[:space:]]*:[[:space:]]*\{([^}]*)\} ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

get_platform_string_field() {
  local entry="$1"
  local field="$2"
  if [[ $entry =~ \"$field\"[[:space:]]*:[[:space:]]*\"([^\"]+)\" ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

shell_profile_file() {
  local home_dir="${HOME:-}"
  if [[ -z "$home_dir" ]]; then
    return 1
  fi
  case "$(basename "${SHELL:-}")" in
    zsh)  printf '%s\n' "${home_dir}/.zprofile" ;;
    bash) printf '%s\n' "${home_dir}/.bash_profile" ;;
    *)    printf '%s\n' "${home_dir}/.profile" ;;
  esac
}

ensure_local_bin_on_path() {
  local bin_dir="${HOME}/.local/bin"
  local profile=""
  local line='case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac'

  export PATH="${bin_dir}:${PATH}"

  profile="$(shell_profile_file || true)"
  if [[ -z "$profile" ]]; then
    return 0
  fi
  mkdir -p "$(dirname "$profile")"
  [[ -f "$profile" ]] || : > "$profile"
  if ! grep -F "$line" "$profile" >/dev/null 2>&1; then
    printf '\n%s\n' "$line" >> "$profile"
  fi
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return 0
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
    return 0
  fi
  log "ERROR: sha256sum or shasum is required"
  exit 1
}

extract_zip_archive() {
  local archive_path="$1"
  local dest_dir="$2"
  if command -v unzip >/dev/null 2>&1; then
    unzip -oq "$archive_path" -d "$dest_dir" >/dev/null
    return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$archive_path" "$dest_dir" <<'PY'
import sys
import zipfile

archive_path, dest_dir = sys.argv[1:3]
with zipfile.ZipFile(archive_path) as zf:
    zf.extractall(dest_dir)
PY
    return 0
  fi
  log "ERROR: unzip or python3 is required to extract ${archive_path}"
  exit 1
}

extract_archive() {
  local archive_path="$1"
  local archive_format="$2"
  local dest_dir="$3"

  rm -rf "$dest_dir"
  mkdir -p "$dest_dir"

  case "$archive_format" in
    zip)
      extract_zip_archive "$archive_path" "$dest_dir"
      ;;
    dmg)
      local mount_dir=""
      mount_dir="$(mktemp -d "${TMPDIR:-/tmp}/codex-app-mount.XXXXXX")"
      hdiutil attach "$archive_path" -nobrowse -readonly -mountpoint "$mount_dir" >/dev/null
      cp -R "$mount_dir"/*.app "$dest_dir"/
      hdiutil detach "$mount_dir" >/dev/null
      rmdir "$mount_dir"
      ;;
    *)
      log "ERROR: unsupported archive format ${archive_format}"
      exit 1
      ;;
  esac
}

find_app_bundle() {
  local root="$1"
  find "$root" -maxdepth 3 -name '*.app' -type d | head -n 1
}

write_launcher() {
  local launcher_path="$1"
  local app_path="$2"
  cat > "$launcher_path" <<EOF
#!/usr/bin/env bash
set -euo pipefail
exec open "$app_path"
EOF
  chmod +x "$launcher_path"
}

install_desktop_app() {
  local version="$1"
  local app_bundle="$2"
  local runtime_root="${HOME}/.local/share/codex-app"
  local versions_dir="${runtime_root}/versions"
  local version_dir="${versions_dir}/${version}"
  local launcher_dir="${HOME}/.local/bin"
  local launcher_path="${launcher_dir}/codex-app"

  mkdir -p "$versions_dir" "$launcher_dir"
  rm -rf "$version_dir"
  mkdir -p "$version_dir"
  cp -R "$app_bundle" "$version_dir/"
  local installed_app="${version_dir}/$(basename "$app_bundle")"

  if [[ ! -d "$installed_app" ]]; then
    log "ERROR: installed Codex App bundle not found at ${installed_app}"
    exit 1
  fi

  xattr -cr "$installed_app" >/dev/null 2>&1 || true

  printf '%s\n' "$version" > "${runtime_root}/version"
  printf '%s\n' "$installed_app" > "${runtime_root}/launch-target"
  ln -sfn "$version_dir" "${runtime_root}/current"
  write_launcher "$launcher_path" "$installed_app"
  ensure_local_bin_on_path
  log "INFO: installed Codex App ${version} to ${installed_app}"
  log "INFO: updated launcher ${launcher_path}"
}

install_via_mirrored_archive() {
  local os=""
  local arch=""
  local platform=""
  local version=""
  local dist_base_url=""
  local manifest_json=""
  local platform_entry=""
  local checksum=""
  local asset_name=""
  local archive_format=""
  local archive_path=""
  local extract_dir=""
  local app_bundle=""
  local actual=""

  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    *)
      log "ERROR: Codex App desktop installer supports macOS only; use the PowerShell installer on Windows"
      exit 1
      ;;
  esac

  select_downloader

  emit_progress 10 detecting_platform
  case "$(uname -m)" in
    x86_64|amd64) arch="x64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      log "ERROR: unsupported architecture $(uname -m)"
      exit 1
      ;;
  esac

  if [[ "$os" == "darwin" && "$arch" == "x64" ]]; then
    if [[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" == "1" ]]; then
      arch="arm64"
    fi
  fi

  platform="${os}-${arch}"

  WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/codex-app-install.XXXXXX")"
  dist_base_url="$(trim_trailing_slash "$DIST_BASE_URL")"

  emit_progress 25 resolving_latest
  version="$(resolve_requested_version)"
  version="$(printf '%s' "$version" | tr -d '[:space:]')"
  if [[ -z "$version" ]]; then
    log "ERROR: failed to resolve Codex App version"
    exit 1
  fi

  manifest_json="$(download_text "$dist_base_url/$version/manifest.json")"
  platform_entry="$(get_platform_entry_from_manifest "$manifest_json" "$platform" || true)"
  checksum="$(get_platform_string_field "$platform_entry" "checksum" || true)"
  asset_name="$(get_platform_string_field "$platform_entry" "asset" || true)"
  archive_format="$(get_platform_string_field "$platform_entry" "archive_format" || true)"
  if [[ -z "$checksum" || -z "$asset_name" || -z "$archive_format" ]]; then
    log "ERROR: platform ${platform} not found in manifest"
    exit 1
  fi

  archive_path="${WORKDIR}/${asset_name}"
  extract_dir="${WORKDIR}/extract"

  emit_progress 55 downloading_archive
  log "INFO: downloading Codex App ${version} for ${platform} from ${dist_base_url}"
  download_file "$dist_base_url/$version/$platform/$asset_name" "$archive_path"

  emit_progress 75 verifying_checksum
  actual="$(sha256_file "$archive_path")"
  if [[ "$actual" != "$checksum" ]]; then
    log "ERROR: checksum verification failed"
    exit 1
  fi

  emit_progress 90 installing_runtime
  extract_archive "$archive_path" "$archive_format" "$extract_dir"
  app_bundle="$(find_app_bundle "$extract_dir")"
  if [[ -z "$app_bundle" ]]; then
    log "ERROR: Codex.app was not found in ${asset_name}"
    exit 1
  fi
  install_desktop_app "$version" "$app_bundle"

  emit_progress 100 complete
  log "INFO: Codex App installation complete"
}

install_via_mirrored_archive
