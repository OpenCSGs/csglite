#!/usr/bin/env bash
set -euo pipefail

APP="${APP:-csgclaw}"
VERSION="${VERSION:-${1:-latest}}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
LIB_DIR="${LIB_DIR:-$HOME/.local/lib/${APP}}"
BASE_URL="${BASE_URL:-https://csgclaw.opencsg.com/releases}"
TMPDIR_INSTALL=""

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

log() {
  printf '%s\n' "$*"
}

cleanup() {
  if [[ -n "${TMPDIR_INSTALL:-}" && -d "${TMPDIR_INSTALL:-}" ]]; then
    rm -rf "${TMPDIR_INSTALL}"
  fi
}

trap cleanup EXIT

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log "ERROR: missing required command: $1"
    exit 1
  }
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      log "ERROR: unsupported OS: $(uname -s)"
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      log "ERROR: unsupported architecture: $(uname -m)"
      exit 1
      ;;
  esac
}

ensure_supported_platform() {
  case "$1/$2" in
    darwin/arm64|linux/amd64|linux/arm64) ;;
    *)
      log "ERROR: unsupported platform: $1/$2"
      log "ERROR: prebuilt csgclaw binaries currently support macOS arm64, Linux amd64, and Linux arm64 only"
      exit 1
      ;;
  esac
}

resolve_latest_version() {
  local api_url tag base
  # Mirror serves GitHub-compatible JSON at ${BASE_URL}/latest (tag_name, assets, ...).
  base="${BASE_URL:-https://csgclaw.opencsg.com/releases}"
  while [[ "$base" == */ ]]; do base="${base%/}"; done
  api_url="${base}/latest"
  tag="$(curl -fsSL "$api_url" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  if [[ -z "$tag" ]]; then
    log "ERROR: failed to resolve latest release from ${api_url}"
    exit 1
  fi
  echo "$tag"
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
  local profile=""
  local line='case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac'

  export PATH="${INSTALL_DIR}:${PATH}"

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

trim_trailing_slash() {
  local value="$1"
  while [[ "$value" == */ ]]; do
    value="${value%/}"
  done
  printf '%s\n' "$value"
}

check_path_hint() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      log ""
      log "${INSTALL_DIR} is not on your PATH."
      log "Add this line to your shell profile:"
      log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      ;;
  esac
}

main() {
  local os=""
  local arch=""
  local version=""
  local base_url=""
  local archive_name=""
  local download_url=""
  local archive_path=""
  local bundle_path=""
  local bundle_bin_path=""
  local install_root=""
  local extracted_path=""

  need_cmd curl
  need_cmd tar
  need_cmd mktemp
  need_cmd install

  emit_progress 10 detecting_platform
  os="$(detect_os)"
  arch="$(detect_arch)"
  ensure_supported_platform "$os" "$arch"

  version="$VERSION"
  if [[ -z "$version" || "$version" == "latest" ]]; then
    emit_progress 25 resolving_latest
    version="$(resolve_latest_version)"
  fi

  version="$(printf '%s' "$version" | tr -d '[:space:]')"
  if [[ -z "$version" ]]; then
    log "ERROR: failed to resolve CSGClaw version"
    exit 1
  fi

  base_url="$(trim_trailing_slash "$BASE_URL")"
  archive_name="${APP}_${version}_${os}_${arch}.tar.gz"
  download_url="${base_url}/${version}/${archive_name}"

  emit_progress 55 downloading_archive
  TMPDIR_INSTALL="$(mktemp -d)"
  archive_path="${TMPDIR_INSTALL}/${archive_name}"
  log "INFO: downloading ${download_url}"
  curl --connect-timeout 15 --max-time 1800 --retry 3 --retry-delay 2 -fsSL "$download_url" -o "$archive_path"

  emit_progress 75 extracting_archive
  tar -xzf "$archive_path" -C "$TMPDIR_INSTALL"
  bundle_path="${TMPDIR_INSTALL}/${APP}"
  bundle_bin_path="${bundle_path}/bin/${APP}"

  emit_progress 90 installing_runtime
  mkdir -p "$INSTALL_DIR" "$LIB_DIR"
  if [[ -f "$bundle_bin_path" ]]; then
    install_root="${LIB_DIR}/${version}"
    rm -rf "$install_root"
    mkdir -p "$install_root"
    cp -R "$bundle_path" "$install_root/"
    ln -sfn "${install_root}/${APP}/bin/${APP}" "${INSTALL_DIR}/${APP}"
    extracted_path="${install_root}/${APP}/bin/${APP}"
  else
    extracted_path="${TMPDIR_INSTALL}/${APP}"
    if [[ ! -f "$extracted_path" ]]; then
      log "ERROR: archive did not contain ${APP}"
      exit 1
    fi
    install -m 0755 "$extracted_path" "${INSTALL_DIR}/${APP}"
    extracted_path="${INSTALL_DIR}/${APP}"
  fi
  ensure_local_bin_on_path

  emit_progress 100 complete
  log "INFO: installed ${APP} ${version} to ${extracted_path}"
  if command -v "$APP" >/dev/null 2>&1; then
    "$APP" --version || true
  fi
  log "INFO: next steps: ${APP} serve"
  log "INFO: CSGClaw installation complete"
  check_path_hint
}

main "$@"
