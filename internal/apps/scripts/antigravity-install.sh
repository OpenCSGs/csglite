#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-latest}"
DEFAULT_DIST_BASE_URL="https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/antigravity-releases"
DIST_BASE_URL="${CSGHUB_LITE_ANTIGRAVITY_DIST_BASE_URL:-$DEFAULT_DIST_BASE_URL}"
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
    curl --connect-timeout 15 --max-time 1800 --retry 3 --retry-delay 2 -fsSL -o "$output" "$url"
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

parse_json_key() {
  local payload="$1"
  local key="$2"
  printf '%s\n' "$payload" | sed -n 's/.*"'"$key"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
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

sha512_file() {
  local path="$1"
  if command -v sha512sum >/dev/null 2>&1; then
    sha512sum "$path" | awk '{print $1}'
    return 0
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 512 "$path" | awk '{print $1}'
    return 0
  fi
  log "ERROR: sha512sum or shasum is required"
  exit 1
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

detect_platform() {
  local os=""
  local arch=""

  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux) os="linux" ;;
    MINGW*|MSYS*|CYGWIN*)
      log "ERROR: use the PowerShell installer on Windows"
      exit 1
      ;;
    *)
      log "ERROR: unsupported operating system $(uname -s)"
      exit 1
      ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64) arch="x64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      log "ERROR: unsupported architecture $(uname -m)"
      exit 1
      ;;
  esac

  printf '%s-%s\n' "$os" "$arch"
}

upstream_platform_for() {
  case "$1" in
    darwin-arm64) printf '%s\n' "darwin_arm64" ;;
    darwin-x64) printf '%s\n' "darwin_amd64" ;;
    linux-arm64) printf '%s\n' "linux_arm64" ;;
    linux-x64) printf '%s\n' "linux_amd64" ;;
    *)
      return 1
      ;;
  esac
}

install_antigravity() {
  local platform=""
  local upstream_platform=""
  local version=""
  local dist_base_url=""
  local manifest_json=""
  local platform_entry=""
  local checksum=""
  local asset_name=""
  local archive_format=""
  local binary_name=""
  local download_url=""
  local download_source=""
  local archive_path=""
  local extract_dir=""
  local extracted_binary=""
  local launcher_dir="${HOME}/.local/bin"
  local launcher_path="${launcher_dir}/agy"
  local actual=""

  select_downloader

  emit_progress 10 detecting_platform
  platform="$(detect_platform)"
  upstream_platform="$(upstream_platform_for "$platform" || true)"

  WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/antigravity-install.XXXXXX")"
  dist_base_url="$(trim_trailing_slash "$DIST_BASE_URL")"

  emit_progress 25 resolving_latest
  version="$(resolve_requested_version || true)"
  version="$(printf '%s' "$version" | tr -d '[:space:]')"

  if [[ -n "$version" ]]; then
    manifest_json="$(download_text "$dist_base_url/$version/manifest.json" || true)"
    platform_entry="$(get_platform_entry_from_manifest "$manifest_json" "$platform" || true)"
    checksum="$(get_platform_string_field "$platform_entry" "checksum_sha512" || true)"
    asset_name="$(get_platform_string_field "$platform_entry" "asset" || true)"
    archive_format="$(get_platform_string_field "$platform_entry" "archive_format" || true)"
    binary_name="$(get_platform_string_field "$platform_entry" "binary" || true)"
    if [[ -n "$checksum" && -n "$asset_name" && -n "$archive_format" && -n "$binary_name" ]]; then
      download_url="$dist_base_url/$version/$platform/$asset_name"
      download_source="$dist_base_url"
    fi
  fi

  if [[ -z "$download_url" ]]; then
    if [[ "${TARGET:-latest}" != "latest" && -n "${TARGET:-}" ]]; then
      log "ERROR: mirrored Antigravity version ${TARGET} is unavailable"
      exit 1
    fi
    if [[ -z "$upstream_platform" ]]; then
      log "ERROR: unsupported Antigravity platform ${platform}"
      exit 1
    fi
    log "INFO: Antigravity mirror is not ready; falling back to the official Google CLI release manifest"
    manifest_json="$(download_text "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/${upstream_platform}.json" || true)"
    version="$(parse_json_key "$manifest_json" "version")"
    download_url="$(parse_json_key "$manifest_json" "url")"
    checksum="$(parse_json_key "$manifest_json" "sha512")"
    asset_name="${download_url%%\?*}"
    asset_name="${asset_name##*/}"
    if [[ "$download_url" == *.tar.gz* ]]; then
      archive_format="tar.gz"
      binary_name="antigravity"
    else
      archive_format="raw"
      binary_name="agy"
    fi
    download_source="official"
  fi

  if [[ -z "$version" || -z "$download_url" || -z "$checksum" || -z "$asset_name" || -z "$archive_format" || -z "$binary_name" ]]; then
    log "ERROR: failed to resolve Antigravity version"
    exit 1
  fi

  archive_path="${WORKDIR}/${asset_name}"
  extract_dir="${WORKDIR}/extract"

  emit_progress 55 downloading_archive
  log "INFO: downloading Antigravity ${version} for ${platform} from ${download_source}"
  download_file "$download_url" "$archive_path"

  emit_progress 75 verifying_checksum
  actual="$(sha512_file "$archive_path")"
  if [[ "$actual" != "$checksum" ]]; then
    log "ERROR: checksum verification failed"
    exit 1
  fi

  emit_progress 90 installing_runtime
  mkdir -p "$launcher_dir" "$extract_dir"
  case "$archive_format" in
    tar.gz)
      tar -xzf "$archive_path" -C "$extract_dir" "$binary_name"
      extracted_binary="${extract_dir}/${binary_name}"
      ;;
    raw)
      extracted_binary="$archive_path"
      ;;
    *)
      log "ERROR: unsupported archive format ${archive_format}"
      exit 1
      ;;
  esac
  [[ -f "$extracted_binary" ]] || {
    log "ERROR: Antigravity binary not found after extraction"
    exit 1
  }
  cp "$extracted_binary" "$launcher_path"
  chmod +x "$launcher_path"
  if [[ "$(uname -s)" == "Darwin" ]]; then
    xattr -d com.apple.quarantine "$launcher_path" 2>/dev/null || true
  fi
  ensure_local_bin_on_path

  "$launcher_path" install || true

  emit_progress 100 complete
  "$launcher_path" --version || true
  log "INFO: Antigravity installation complete"
}

install_antigravity
