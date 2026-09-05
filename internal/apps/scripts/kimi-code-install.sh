#!/usr/bin/env bash
set -euo pipefail

emit_progress() {
  local progress="$1"
  local phase="$2"
  printf 'CSGHUB_PROGRESS|%s|%s\n' "${progress}" "${phase}"
}

log() {
  printf '%s\n' "$*"
}

DOWNLOAD_BASE="${CSGHUB_LITE_KIMI_CODE_DOWNLOAD_BASE:-https://code.kimi.com/kimi-code}"
INSTALL_DIR="${KIMI_INSTALL_DIR:-${HOME}/.kimi-code}"
LAUNCHER_DIR="${HOME}/.local/bin"
LAUNCHER_PATH="${LAUNCHER_DIR}/kimi"

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

  export PATH="${LAUNCHER_DIR}:${PATH}"

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

select_downloader() {
  if command -v curl >/dev/null 2>&1; then
    echo "curl"
    return 0
  fi
  if command -v wget >/dev/null 2>&1; then
    echo "wget"
    return 0
  fi
  log "ERROR: either curl or wget is required"
  exit 1
}

download_text() {
  local url="$1"
  local downloader="$2"
  if [[ "$downloader" == "curl" ]]; then
    curl --connect-timeout 15 --max-time 60 --retry 3 --retry-delay 2 -fsSL "$url"
  else
    wget --tries=3 --timeout=20 -q -O - "$url"
  fi
}

download_file() {
  local url="$1"
  local output="$2"
  local downloader="$3"
  if [[ "$downloader" == "curl" ]]; then
    curl --connect-timeout 15 --max-time 1800 --retry 3 --retry-delay 2 -fsSL -o "$output" "$url"
  else
    wget --tries=3 --timeout=30 -O "$output" "$url"
  fi
}

detect_target() {
  local os arch
  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux)  os="linux"  ;;
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
  if [[ "$os" == "darwin" && "$arch" == "x64" ]]; then
    if [[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" == "1" ]]; then
      arch="arm64"
    fi
  fi
  echo "${os}-${arch}"
}

manifest_field() {
  local manifest_json="$1"
  local target="$2"
  local field="$3"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$manifest_json" | jq -er ".platforms[\"$target\"].$field // empty" 2>/dev/null && return 0
  fi
  local one_line
  one_line="$(printf '%s' "$manifest_json" | tr -d '\n\r\t' | sed 's/ \+/ /g')"
  if [[ $one_line =~ \"$target\"[^}]*\"$field\"[[:space:]]*:[[:space:]]*\"([^\"]+)\" ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
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

WORKDIR=""
cleanup() {
  if [[ -n "${WORKDIR}" && -d "${WORKDIR}" ]]; then
    rm -rf "${WORKDIR}"
  fi
}
trap cleanup EXIT

install_kimi_code() {
  local downloader target version manifest_url manifest filename checksum binary_url
  local binary_base="${DOWNLOAD_BASE}/binaries"

  emit_progress 5 preflight
  downloader="$(select_downloader)"

  emit_progress 10 detecting_platform
  target="$(detect_target)"
  log "INFO: detected target ${target}"

  emit_progress 25 resolving_latest
  version="$(download_text "${DOWNLOAD_BASE}/latest" "$downloader" | tr -d '[:space:]')"
  if [[ -z "$version" ]]; then
    log "ERROR: failed to resolve latest Kimi Code version"
    exit 1
  fi
  log "INFO: latest version ${version}"

  emit_progress 40 downloading_binary
  manifest_url="${binary_base}/${version}/manifest.json"
  manifest="$(download_text "$manifest_url" "$downloader")"
  if [[ -z "$manifest" ]]; then
    log "ERROR: failed to fetch manifest ${manifest_url}"
    exit 1
  fi
  filename="$(manifest_field "$manifest" "$target" "filename" || true)"
  checksum="$(manifest_field "$manifest" "$target" "checksum" || true)"
  if [[ -z "$filename" || -z "$checksum" ]]; then
    log "ERROR: platform ${target} not found in manifest"
    exit 1
  fi

  WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/kimi-code-install.XXXXXX")"
  binary_url="${binary_base}/${version}/${filename}"
  log "INFO: downloading ${binary_url}"
  download_file "$binary_url" "${WORKDIR}/${filename}" "$downloader"

  emit_progress 75 verifying_kimi_code
  local actual
  actual="$(sha256_file "${WORKDIR}/${filename}")"
  if [[ "$actual" != "$checksum" ]]; then
    log "ERROR: checksum verification failed"
    exit 1
  fi

  emit_progress 85 installing_kimi_code
  chmod +x "${WORKDIR}/${filename}"
  mkdir -p "${INSTALL_DIR}/bin" "${LAUNCHER_DIR}"
  install -m 0755 "${WORKDIR}/${filename}" "${INSTALL_DIR}/bin/kimi"
  ln -sfn "${INSTALL_DIR}/bin/kimi" "${LAUNCHER_PATH}"
  ensure_local_bin_on_path
  hash -r 2>/dev/null || true

  log "INFO: installed Kimi Code ${version} to ${INSTALL_DIR}/bin/kimi"
  log "INFO: updated launcher ${LAUNCHER_PATH}"
}

install_kimi_code

emit_progress 100 complete
"${LAUNCHER_PATH}" --version 2>/dev/null || true
log "INFO: Kimi Code installed successfully."
