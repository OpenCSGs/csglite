#!/bin/sh
# csghub-lite install script
# Usage: curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | sh
set -eu

REPO="${REPO:-OpenCSGs/csglite}"
INSTALL_DIR="${INSTALL_DIR:-}"
INSTALL_DIR_DEFAULT="/usr/local/bin"
BINARY_NAME="${BINARY_NAME:-csghub-lite}"
EE="${EE:-}"
LLAMA_CPP_REPO="ggml-org/llama.cpp"
LLAMA_CPP_DEFAULT_TAG="${CSGHUB_LITE_LLAMA_CPP_TAG:-b9158}"
INSTALL_PATH_PROFILE=""
INSTALL_PATH_DIR=""

GITHUB_API="https://api.github.com/repos"
GITLAB_HOST="https://git-devops.opencsg.com"
GITLAB_API="${GITLAB_HOST}/api/v4/projects"
GITLAB_CSGHUB_ID="392"
GITLAB_LLAMA_ID="393"
ENTERPRISE_LICENSE_URL="${GITLAB_HOST}/opensource/public_files/-/raw/main/license.txt"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info() { printf "${GREEN}[INFO]${NC} %s\n" "$1"; }
warn() { printf "${YELLOW}[WARN]${NC} %s\n" "$1"; }
error() { printf "${RED}[ERROR]${NC} %s\n" "$1" >&2; exit 1; }
step() { printf "${CYAN}[%s/%s]${NC} %s\n" "$1" "$2" "$3"; }

privileged_prefix() {
    if [ "$(id -u)" = "0" ]; then
        printf ""
    elif command -v sudo >/dev/null 2>&1; then
        printf "sudo "
    else
        printf ""
    fi
}

run_privileged() {
    if [ "$(id -u)" = "0" ]; then
        "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo "$@"
    else
        error "This step requires root privileges, but sudo is not available. Re-run as root or install sudo."
    fi
}

try_run_privileged() {
    if [ "$(id -u)" = "0" ]; then
        "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo "$@"
    else
        return 1
    fi
}

detect_os() {
    case "$(uname -s)" in
        Linux)  echo "linux" ;;
        Darwin) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *) error "Unsupported operating system: $(uname -s)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) error "Unsupported architecture: $(uname -m)" ;;
    esac
}

detect_region() {
    _region="${CSGHUB_LITE_REGION:-}"
    if [ -n "$_region" ]; then echo "$_region"; return; fi
    _country="$(curl -fsSL --connect-timeout 3 --max-time 5 https://ipinfo.io/country 2>/dev/null | tr -d '[:space:]' || true)"
    if [ "$_country" = "CN" ]; then
        echo "CN"
    elif [ -n "$_country" ]; then
        echo "INTL"
    else
        echo "CN"
    fi
}

download() {
    if command -v curl >/dev/null 2>&1; then
        curl -fSL --connect-timeout 15 --retry 3 --retry-delay 5 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget --timeout=15 --tries=3 -O "$2" "$1"
    else
        error "curl or wget is required"
    fi
}

download_text() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --connect-timeout 10 --max-time 30 "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget --timeout=10 -qO- "$1"
    else
        error "curl or wget is required"
    fi
}

# Try downloading a file from multiple URLs in order (first arg = destination)
try_download() {
    _td_dest="$1"; shift
    for _td_url in "$@"; do
        if download "$_td_url" "$_td_dest"; then
            return 0
        fi
    done
    return 1
}

# Try fetching text from multiple URLs in order
try_download_text() {
    for _tdt_url in "$@"; do
        _tdt_result="$(download_text "$_tdt_url" 2>/dev/null || true)"
        if [ -n "$_tdt_result" ]; then
            printf "%s\n" "$_tdt_result"
            return 0
        fi
    done
    return 1
}

# Region-aware file download: GitLab first for CN, GitHub first for INTL
region_download() {
    _rd_dest="$1"
    _rd_github="$2"
    _rd_gitlab="$3"
    if [ "$REGION" = "CN" ]; then
        try_download "$_rd_dest" "$_rd_gitlab" "$_rd_github"
    else
        try_download "$_rd_dest" "$_rd_github" "$_rd_gitlab"
    fi
}

# Region-aware text download
region_download_text() {
    _rdt_github="$1"
    _rdt_gitlab="$2"
    if [ "$REGION" = "CN" ]; then
        try_download_text "$_rdt_gitlab" "$_rdt_github"
    else
        try_download_text "$_rdt_github" "$_rdt_gitlab"
    fi
}

add_candidate() {
    _candidate="$1"
    [ -n "$_candidate" ] || return 0
    case " ${_candidates:-} " in
        *" ${_candidate} "*) return 0 ;;
    esac
    _candidates="${_candidates:+${_candidates} }${_candidate}"
}

extract_release_asset_names() {
    _pattern="$1"
    printf "%s\n" "${_llama_json:-}" | grep -Eo "$_pattern" | awk '!seen[$0]++'
}

add_release_candidates() {
    _pattern="$1"
    _matches="$(extract_release_asset_names "$_pattern" 2>/dev/null || true)"
    [ -n "$_matches" ] || return 0
    _old_ifs="$IFS"
    IFS='
'
    for _match in $_matches; do
        add_candidate "$_match"
    done
    IFS="$_old_ifs"
}

add_release_cuda_candidates() {
    _arch_token="$1"
    _versioned_pattern="llama-${_llama_tag}-bin-ubuntu-cuda-[0-9]+(\\.[0-9]+(\\.[0-9]+)?)?-${_arch_token}\\.tar\\.gz"
    _versioned_matches="$(extract_release_asset_names "$_versioned_pattern" 2>/dev/null | while IFS= read -r _match; do
        [ -n "$_match" ] || continue
        _ver="$(printf "%s\n" "$_match" | sed -n "s/.*-cuda-\\([0-9][0-9]*\\)\\(\\.\\([0-9][0-9]*\\)\\)\\{0,1\\}\\(\\.\\([0-9][0-9]*\\)\\)\\{0,1\\}-${_arch_token}\\.tar\\.gz/\\1 \\3 \\5/p")"
        [ -n "$_ver" ] || continue
        # shellcheck disable=SC2086
        set -- $_ver
        printf "%06d %06d %06d %s\n" "${1:-0}" "${2:-0}" "${3:-0}" "$_match"
    done | sort -r | sed 's/^[0-9][0-9]* [0-9][0-9]* [0-9][0-9]* //')"
    if [ -n "$_versioned_matches" ]; then
        _old_ifs="$IFS"
        IFS='
'
        for _match in $_versioned_matches; do
            add_candidate "$_match"
        done
        IFS="$_old_ifs"
    fi

    # Some mirrored CUDA builds intentionally omit the CUDA minor version in the
    # filename. Prefer the release metadata name before trying hard-coded legacy
    # aliases, so installers do not stall on missing versioned URLs.
    add_release_candidates "llama-${_llama_tag}-bin-ubuntu-cuda-${_arch_token}\\.tar\\.gz"
}

normalize_minor_version() {
    printf "%s\n" "$1" | sed -n 's/.*\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | sed -n '1p'
}

extract_rocm_minor_version() {
    _rocm_text="$1"
    _rocm_version="$(printf "%s\n" "$_rocm_text" | sed -n 's/.*HIP version:[[:space:]]*\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | sed -n '1p')"
    [ -n "$_rocm_version" ] || _rocm_version="$(printf "%s\n" "$_rocm_text" | sed -n 's/.*ROCm version:[[:space:]]*\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | sed -n '1p')"
    [ -n "$_rocm_version" ] || _rocm_version="$(printf "%s\n" "$_rocm_text" | sed -n 's@.*rocm-\([0-9][0-9]*\.[0-9][0-9]*\).*@\1@p' | sed -n '1p')"
    [ -n "$_rocm_version" ] || _rocm_version="$(normalize_minor_version "$_rocm_text")"
    printf "%s\n" "$_rocm_version"
}

detect_rocm_version() {
    _rocm_version="$(extract_rocm_minor_version "${CSGHUB_LITE_LLAMA_ROCM_VERSION:-}")"
    if [ -n "$_rocm_version" ]; then
        printf "%s\n" "$_rocm_version"
        return 0
    fi

    for _version_file in \
        /opt/rocm/.info/version \
        /opt/rocm/.info/version-dev \
        /opt/rocm-*/.info/version \
        /opt/rocm-*/.info/version-dev
    do
        [ -f "$_version_file" ] || continue
        _rocm_version="$(extract_rocm_minor_version "$(sed -n '1p' "$_version_file" 2>/dev/null || true)")"
        if [ -n "$_rocm_version" ]; then
            printf "%s\n" "$_rocm_version"
            return 0
        fi
    done

    if command -v hipcc >/dev/null 2>&1; then
        _hipcc_version="$(hipcc --version 2>/dev/null || true)"
        _rocm_version="$(extract_rocm_minor_version "$_hipcc_version")"
        if [ -n "$_rocm_version" ]; then
            printf "%s\n" "$_rocm_version"
            return 0
        fi
    fi

    return 1
}

has_rocm_runtime() {
    if command -v rocm-smi >/dev/null 2>&1; then
        _rocm_smi_out="$(rocm-smi 2>/dev/null || true)"
        if printf "%s\n" "$_rocm_smi_out" | grep -Eq '^[[:space:]]*[0-9]+[[:space:]]'; then
            return 0
        fi
    fi

    if command -v rocminfo >/dev/null 2>&1; then
        _rocminfo_out="$(rocminfo 2>/dev/null || true)"
        if printf "%s\n" "$_rocminfo_out" | grep -Eq 'gfx[0-9]+'; then
            return 0
        fi
    fi

    if command -v hipcc >/dev/null 2>&1 && [ -e /dev/kfd ]; then
        return 0
    fi

    return 1
}

install_enterprise_license() {
    _install_dir="$1"
    if [ "$EE" != "1" ]; then
        return 0
    fi

    _license_path="${_install_dir}/license.txt"
    _license_tmp="$(mktemp)"

    info "EE=1 detected. Installing enterprise license..."
    if ! download_text "$ENTERPRISE_LICENSE_URL" > "$_license_tmp"; then
        rm -f "$_license_tmp"
        error "Failed to download enterprise license from ${ENTERPRISE_LICENSE_URL}"
    fi
    if [ ! -s "$_license_tmp" ]; then
        rm -f "$_license_tmp"
        error "Downloaded enterprise license is empty"
    fi

    if [ -w "$_install_dir" ]; then
        mv "$_license_tmp" "$_license_path"
        chmod 0644 "$_license_path"
    else
        info "Requires root privileges to install enterprise license to ${_install_dir}"
        run_privileged mv "$_license_tmp" "$_license_path"
        run_privileged chmod 0644 "$_license_path"
    fi

    info "Installed enterprise license to ${_license_path}"
}

path_contains_dir() {
    _target="$1"
    _old_ifs="$IFS"
    IFS=':'
    for _entry in $PATH; do
        if [ "$_entry" = "$_target" ]; then
            IFS="$_old_ifs"
            return 0
        fi
    done
    IFS="$_old_ifs"
    return 1
}

nearest_existing_parent_writable() {
    _dir="$1"
    while [ ! -e "$_dir" ]; do
        _next="$(dirname "$_dir")"
        if [ "$_next" = "$_dir" ]; then
            return 1
        fi
        _dir="$_next"
    done
    [ -d "$_dir" ] && [ -w "$_dir" ]
}

dir_is_writable_or_creatable() {
    _dir="$1"
    if [ -d "$_dir" ]; then
        [ -w "$_dir" ]
        return
    fi
    nearest_existing_parent_writable "$_dir"
}

ensure_dir_exists() {
    _dir="$1"
    [ -d "$_dir" ] || mkdir -p "$_dir"
}

shell_profile_file() {
    _home="${HOME:-}"
    if [ -z "$_home" ]; then
        return 1
    fi
    case "$(basename "${SHELL:-}")" in
        zsh)  printf '%s\n' "${_home}/.zprofile" ;;
        bash) printf '%s\n' "${_home}/.bash_profile" ;;
        *)    printf '%s\n' "${_home}/.profile" ;;
    esac
}

shell_path_expr() {
    case "$1" in
        "${HOME}/bin") printf '%s\n' '$HOME/bin' ;;
        "${HOME}/.local/bin") printf '%s\n' '$HOME/.local/bin' ;;
        *) printf '%s\n' "$1" ;;
    esac
}

ensure_future_shell_path() {
    _path_dir="$1"
    if path_contains_dir "$_path_dir"; then
        return 0
    fi
    _profile="$(shell_profile_file || true)"
    if [ -z "$_profile" ]; then
        return 1
    fi
    _expr="$(shell_path_expr "$_path_dir")"
    _line="case \":\$PATH:\" in *\":${_expr}:\"*) ;; *) export PATH=\"${_expr}:\$PATH\" ;; esac"
    ensure_dir_exists "$(dirname "$_profile")"
    [ -f "$_profile" ] || : > "$_profile"
    if ! grep -F "$_line" "$_profile" >/dev/null 2>&1; then
        printf '\n%s\n' "$_line" >> "$_profile"
    fi
    INSTALL_PATH_PROFILE="$_profile"
    INSTALL_PATH_DIR="$_path_dir"
    return 0
}

resolve_darwin_install_dir() {
    _existing_bin="$1"
    _home_bin="${HOME}/bin"
    _local_bin="${HOME}/.local/bin"
    _brew_bin=""

    if [ -n "$_existing_bin" ]; then
        _existing_dir="$(dirname "$_existing_bin")"
        if dir_is_writable_or_creatable "$_existing_dir"; then
            ensure_dir_exists "$_existing_dir"
            INSTALL_DIR="$_existing_dir"
            return 0
        fi
    fi

    if command -v brew >/dev/null 2>&1; then
        _brew_bin="$(dirname "$(command -v brew)")"
    fi

    _old_ifs="$IFS"
    IFS=':'
    for _entry in $PATH; do
        [ -n "$_entry" ] || continue
        _supported=false
        case "$_entry" in
            "$_home_bin"|"$_local_bin"|"/opt/homebrew/bin"|"/usr/local/bin")
                _supported=true
                ;;
        esac
        if [ "$_supported" = false ] && [ -n "$_brew_bin" ] && [ "$_entry" = "$_brew_bin" ]; then
            _supported=true
        fi
        if [ "$_supported" = true ] && dir_is_writable_or_creatable "$_entry"; then
            ensure_dir_exists "$_entry"
            IFS="$_old_ifs"
            INSTALL_DIR="$_entry"
            return 0
        fi
    done
    IFS="$_old_ifs"

    if dir_is_writable_or_creatable "$_home_bin"; then
        ensure_dir_exists "$_home_bin"
        ensure_future_shell_path "$_home_bin" || true
        INSTALL_DIR="$_home_bin"
        return 0
    fi

    error "Could not find a writable install directory on macOS. Set INSTALL_DIR manually."
}

resolve_install_dir() {
    _existing_bin="$1"
    if [ -n "$INSTALL_DIR" ]; then
        ensure_dir_exists "$INSTALL_DIR"
        return
    fi
    if [ "$OS" = "darwin" ]; then
        resolve_darwin_install_dir "$_existing_bin"
        return
    fi
    if [ -n "$_existing_bin" ]; then
        INSTALL_DIR="$(dirname "$_existing_bin")"
    else
        INSTALL_DIR="${INSTALL_DIR_DEFAULT}"
    fi
}

cleanup_previous_binary() {
    _old_bin="$1"
    _new_bin="$2"
    if [ -z "$_old_bin" ] || [ "$_old_bin" = "$_new_bin" ] || [ ! -f "$_old_bin" ]; then
        return 0
    fi
    if [ -w "$_old_bin" ] || [ -w "$(dirname "$_old_bin")" ]; then
        rm -f "$_old_bin"
        info "Removed previous installation at ${_old_bin}"
        return 0
    fi
    warn "Previous installation remains at ${_old_bin}"
    warn "Remove it later if you no longer need it: $(privileged_prefix)rm -f ${_old_bin}"
}

get_latest_version() {
    _gh_url="${GITHUB_API}/${REPO}/releases/latest"
    _gl_url="${GITLAB_API}/${GITLAB_CSGHUB_ID}/releases/permalink/latest"
    _json="$(region_download_text "$_gh_url" "$_gl_url" 2>/dev/null || true)"
    if [ -n "$_json" ]; then
        _tag="$(printf "%s\n" "$_json" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
        if [ -n "$_tag" ]; then
            printf "%s\n" "$_tag"
            return 0
        fi
    fi
    return 1
}

install_llama_server() {
    _existing_llama="$(command -v llama-server 2>/dev/null || true)"
    if [ -n "$_existing_llama" ]; then
        info "llama-server found at ${_existing_llama}"
    else
        warn "llama-server not found. It is required for model inference."
    fi

    _auto="${CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER:-1}"
    if [ "$_auto" != "1" ]; then
        warn "Auto-install disabled (CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=${_auto})."
        return
    fi

    _custom="${CSGHUB_LITE_LLAMA_CPP_INSTALL_CMD:-}"
    if [ -n "$_custom" ]; then
        info "Installing llama.cpp via custom command..."
        if sh -c "$_custom" >/dev/null 2>&1 && command -v llama-server >/dev/null 2>&1; then
            info "llama-server installed successfully."
            return
        fi
        warn "Custom install command failed."
    fi

    OS="$(detect_os)"
    ARCH="$(detect_arch)"

    _llama_tag="${LLAMA_CPP_DEFAULT_TAG}"
    _gh_url="${GITHUB_API}/${LLAMA_CPP_REPO}/releases/tags/${_llama_tag}"
    _gl_url="${GITLAB_API}/${GITLAB_LLAMA_ID}/releases/${_llama_tag}"
    _llama_json="$(region_download_text "$_gh_url" "$_gl_url" 2>/dev/null || true)"
    if [ -z "$_llama_json" ]; then
        warn "Failed to query llama.cpp release metadata for ${_llama_tag}; continuing with the pinned tag."
    fi

    # Compare local and remote versions to skip unnecessary downloads.
    # llama-server --version prints: "version: <n> (<hash>)" (stderr). Release tags: "b<n>".
    # Upstream sets <n> from git rev-list --count at build time; shallow clones often get n=1,
    # which must not be compared to official b-tags (would falsely show "from b1").
    # Linux does not load co-located .so from the binary directory by default; without
    # patchelf $ORIGIN, --version fails and we must not treat that as "unknown version"
    # (would re-download every install).
    if [ -n "$_existing_llama" ]; then
        _llama_bin_dir="$(dirname "$_existing_llama")"
        case "$OS" in
            linux)
                _llama_ver_out="$(env LD_LIBRARY_PATH="${_llama_bin_dir}${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}" "$_existing_llama" --version 2>&1 || true)"
                ;;
            darwin)
                _llama_ver_out="$(env DYLD_LIBRARY_PATH="${_llama_bin_dir}${DYLD_LIBRARY_PATH:+:${DYLD_LIBRARY_PATH}}" "$_existing_llama" --version 2>&1 || true)"
                ;;
            *)
                _llama_ver_out="$("$_existing_llama" --version 2>&1 || true)"
                ;;
        esac
        # Prefer the real footer line (after CUDA/backend init), not a stray "version:" elsewhere.
        _local_build="$(printf "%s\n" "$_llama_ver_out" | grep -E '^version: [0-9]+ \(' | tail -1 | sed -n 's/^version: *\([0-9][0-9]*\) (.*/\1/p')"
        if [ -z "$_local_build" ]; then
            _local_build="$(printf "%s\n" "$_llama_ver_out" | sed -n 's/^version: *\([0-9][0-9]*\).*/\1/p' | tail -1)"
        fi
        _remote_build="${_llama_tag#b}"
        # Ignore tiny local ids when release is large (shallow clone / non-official build id).
        if [ -n "$_local_build" ] && [ -n "$_remote_build" ]; then
            if [ "$_local_build" -le 100 ] 2>/dev/null && [ "$_remote_build" -ge 2000 ] 2>/dev/null; then
                info "Ignoring local llama-server build id ${_local_build} (not comparable to official ${_llama_tag}; often from shallow git clone)."
                _local_build=""
            fi
        fi
        if [ -n "$_local_build" ] && [ "$_local_build" = "$_remote_build" ]; then
            info "llama-server is already up to date (${_llama_tag})."
            return
        fi
        if [ -n "$_local_build" ]; then
            info "Upgrading llama-server from b${_local_build} to ${_llama_tag}..."
        else
            info "Upgrading llama-server to ${_llama_tag}..."
        fi
    fi

    info "Downloading llama.cpp ${_llama_tag} for ${OS}/${ARCH}..."

    # Build ordered list of candidate asset names (best match first)
    _candidates=""
    case "$OS" in
        darwin)
            case "$ARCH" in
                amd64) add_candidate "llama-${_llama_tag}-bin-macos-x64.tar.gz" ;;
                arm64) add_candidate "llama-${_llama_tag}-bin-macos-arm64.tar.gz" ;;
            esac ;;
        linux)
            case "$ARCH" in
                amd64) _arch_token="x64" ;;
                arm64) _arch_token="arm64" ;;
            esac
            if [ -n "${_arch_token:-}" ]; then
                if command -v nvidia-smi >/dev/null 2>&1; then
                    info "NVIDIA GPU detected, trying CUDA build first."
                    add_release_cuda_candidates "$_arch_token"
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-cuda-${_arch_token}.tar.gz"
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-cuda-12.4-${_arch_token}.tar.gz"
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-vulkan-${_arch_token}.tar.gz"
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-${_arch_token}.tar.gz"
                elif [ "$_arch_token" = "x64" ] && has_rocm_runtime; then
                    _rocm_version="$(detect_rocm_version || true)"
                    if [ -n "$_rocm_version" ]; then
                        info "ROCm runtime detected (${_rocm_version}), trying ROCm build first."
                        add_candidate "llama-${_llama_tag}-bin-ubuntu-rocm-${_rocm_version}-${_arch_token}.tar.gz"
                    else
                        info "ROCm runtime detected, trying published ROCm builds first."
                    fi
                    add_release_candidates "llama-${_llama_tag}-bin-ubuntu-rocm-[0-9]+\\.[0-9]+-${_arch_token}\\.tar\\.gz"
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-vulkan-${_arch_token}.tar.gz"
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-${_arch_token}.tar.gz"
                else
                    add_candidate "llama-${_llama_tag}-bin-ubuntu-${_arch_token}.tar.gz"
                fi
            fi ;;
    esac
    if [ -z "$_candidates" ]; then
        warn "No compatible llama.cpp asset for ${OS}/${ARCH} at ${_llama_tag}."
        warn "Install manually from: https://github.com/${LLAMA_CPP_REPO}/releases/tag/${_llama_tag}"
        return
    fi

    _tmpdir="$(mktemp -d)"
    _downloaded=false
    _llama_asset=""
    for _candidate in $_candidates; do
        _github_dl="https://github.com/${LLAMA_CPP_REPO}/releases/download/${_llama_tag}/${_candidate}"
        _gitlab_dl="${GITLAB_API}/${GITLAB_LLAMA_ID}/packages/generic/llama-cpp/${_llama_tag}/${_candidate}"
        _archive="${_tmpdir}/${_candidate}"
        if region_download "$_archive" "$_github_dl" "$_gitlab_dl"; then
            _llama_asset="$_candidate"
            _downloaded=true
            break
        fi
        warn "Asset ${_candidate} not available, trying next option..."
    done
    if [ "$_downloaded" = false ]; then
        warn "Failed to download llama.cpp ${_llama_tag}."
        warn "Install manually from: https://github.com/${LLAMA_CPP_REPO}/releases/tag/${_llama_tag}"
        rm -rf "$_tmpdir"
        return
    fi
    info "Downloaded ${_llama_asset}"

    tar xzf "$_archive" -C "$_tmpdir"
    _llama_bin="$(find "$_tmpdir" -name "llama-server" -type f | head -1)"
    if [ -z "$_llama_bin" ]; then
        warn "llama-server not found in archive."
        rm -rf "$_tmpdir"
        return
    fi
    chmod +x "$_llama_bin"

    _llama_dir="${CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR:-}"
    if [ -z "$_llama_dir" ]; then
        if [ "$OS" = "darwin" ] && [ -n "${INSTALL_DIR:-}" ]; then
            _llama_dir="${INSTALL_DIR}"
        elif [ -n "$_existing_llama" ]; then
            _llama_dir="$(dirname "$_existing_llama")"
        elif command -v csghub-lite >/dev/null 2>&1; then
            _llama_dir="$(dirname "$(command -v csghub-lite)")"
        else
            _llama_dir="${INSTALL_DIR_DEFAULT}"
        fi
    fi
    mkdir -p "$_llama_dir"

    # Install shared libraries from the entire extract tree (tarballs may put libs under lib/
    # next to bin/llama-server; only searching dirname(llama-server) misses them).
    # Preserve symlinks like libmtmd.0.dylib -> libmtmd.0.0.8429.dylib; the loader resolves
    # @rpath against those compatibility names, not just the versioned payload files.
    if [ -w "$_llama_dir" ]; then
        find "$_tmpdir" \( -type f -o -type l \) \( -name "*.dylib" -o -name "*.so" -o -name "*.so.*" \) | while read -r _lib; do
            mv "$_lib" "$_llama_dir/"
        done
        mv "$_llama_bin" "$_llama_dir/"
    else
        info "Requires root privileges to install llama-server."
        find "$_tmpdir" \( -type f -o -type l \) \( -name "*.dylib" -o -name "*.so" -o -name "*.so.*" \) | while read -r _lib; do
            run_privileged mv "$_lib" "$_llama_dir/"
        done
        run_privileged mv "$_llama_bin" "$_llama_dir/"
    fi

    # Fix @rpath on macOS so llama-server can find co-located dylibs
    if [ "$OS" = "darwin" ] && command -v install_name_tool >/dev/null 2>&1; then
        _llama_installed="${_llama_dir}/llama-server"
        if [ -f "$_llama_installed" ]; then
            # Add @executable_path to rpath (ignore error if already present)
            if [ -w "$_llama_installed" ]; then
                install_name_tool -add_rpath @executable_path "$_llama_installed" 2>/dev/null || true
            else
                try_run_privileged install_name_tool -add_rpath @executable_path "$_llama_installed" 2>/dev/null || true
            fi
        fi
    fi

    # Linux: default dynamic linker does not search the executable directory.
    # Prefer patchelf $ORIGIN so `llama-server --version` works without LD_LIBRARY_PATH.
    if [ "$OS" = "linux" ]; then
        _llama_installed="${_llama_dir}/llama-server"
        if [ -f "$_llama_installed" ] && ! command -v patchelf >/dev/null 2>&1; then
            if [ "${CSGHUB_LITE_AUTO_INSTALL_PATCHELF:-1}" = "1" ]; then
                if command -v apt-get >/dev/null 2>&1; then
                    info "Installing patchelf (apt) so llama-server can load co-located .so..."
                    try_run_privileged apt-get update -qq >/dev/null 2>&1 && try_run_privileged apt-get install -y patchelf >/dev/null 2>&1 || true
                elif command -v dnf >/dev/null 2>&1; then
                    info "Installing patchelf (dnf) so llama-server can load co-located .so..."
                    try_run_privileged dnf install -y patchelf >/dev/null 2>&1 || true
                elif command -v yum >/dev/null 2>&1; then
                    info "Installing patchelf (yum) so llama-server can load co-located .so..."
                    try_run_privileged yum install -y patchelf >/dev/null 2>&1 || true
                fi
            fi
        fi
        if [ -f "$_llama_installed" ] && command -v patchelf >/dev/null 2>&1; then
            if [ -w "$_llama_installed" ]; then
                patchelf --set-rpath '$ORIGIN' "$_llama_installed" 2>/dev/null || true
            else
                try_run_privileged patchelf --set-rpath '$ORIGIN' "$_llama_installed" 2>/dev/null || true
            fi
            info "patchelf: llama-server uses RUNPATH \$ORIGIN (co-located .so)."
        elif [ -f "$_llama_installed" ]; then
            info "To run llama-server directly, set: export LD_LIBRARY_PATH=\"${_llama_dir}:\${LD_LIBRARY_PATH}\""
            info "(csghub-lite sets this automatically when starting inference.)"
            info "Or install patchelf: apt install patchelf / dnf install patchelf — then re-run this installer."
        fi
    fi

    rm -rf "$_tmpdir"
    info "llama-server installed successfully."
}

check_python_optional() {
    # Python is optional — only needed for rare/unsupported architectures.
    # The built-in Go converter handles 160+ architectures natively.
    _python=""
    for _name in python3 python; do
        if command -v "$_name" >/dev/null 2>&1; then
            _ver="$("$_name" -c 'import sys; print(sys.version_info.major)' 2>/dev/null || echo "0")"
            if [ "$_ver" = "3" ]; then
                _python="$_name"
                break
            fi
        fi
    done

    if [ -n "$_python" ]; then
        info "Python 3 found (optional): $(${_python} --version 2>&1)"
    else
        info "Python 3 not found (optional — not required for most models)."
    fi
}

has_running_named_processes() {
    _proc_name="$1"
    if ! command -v pgrep >/dev/null 2>&1; then
        return 1
    fi
    pgrep -x "$_proc_name" >/dev/null 2>&1
}

force_stop_named_processes() {
    _proc_name="$1"
    if ! command -v pkill >/dev/null 2>&1; then
        return 1
    fi
    pkill -x "$_proc_name" 2>/dev/null || true
    sleep 1
    pkill -9 -x "$_proc_name" 2>/dev/null || true
    sleep 1
    ! has_running_named_processes "$_proc_name"
}

check_existing() {
    _existing="$(command -v "$BINARY_NAME" 2>/dev/null || true)"
    if [ -z "$_existing" ]; then
        return 0
    fi

    _old_ver="$("$_existing" --version 2>/dev/null | head -1 || echo "unknown")"

    printf "\n"
    warn "Existing installation detected:"
    printf "  ${BOLD}Binary:${NC}  %s\n" "$_existing"
    printf "  ${BOLD}Version:${NC} %s\n" "$_old_ver"

    _has_running=false
    if has_running_named_processes "$BINARY_NAME"; then
        _has_running=true
        warn "Running ${BINARY_NAME} process(es) detected."
    fi

    if [ "${CSGHUB_LITE_FORCE:-}" = "1" ]; then
        if [ "$_has_running" = true ]; then
            info "Force mode: stopping running processes..."
            force_stop_named_processes "$BINARY_NAME" || true
        fi
        return 0
    fi

    printf "\n"
    if [ "$_has_running" = true ]; then
        printf "${YELLOW}Stop running instances and replace with the new version? [y/N] ${NC}"
    else
        printf "${YELLOW}Replace the existing installation? [y/N] ${NC}"
    fi

    _answer=""
    if [ -t 0 ]; then
        read -r _answer
    elif [ -e /dev/tty ]; then
        read -r _answer < /dev/tty
    else
        printf "\n"
        info "Non-interactive mode: proceeding with replacement."
        _answer="y"
    fi

    case "$_answer" in
        [yY]|[yY][eE][sS])
            if [ "$_has_running" = true ]; then
                info "Stopping running processes..."
                force_stop_named_processes "$BINARY_NAME" || true
            fi
            ;;
        *)
            printf "\n"
            info "Installation cancelled."
            exit 0
            ;;
    esac
    printf "\n"
}

restart_running_csghub_lite_server() {
    _server_bin="$1"
    if ! "$_server_bin" ps >/dev/null 2>&1 && ! has_running_named_processes "$BINARY_NAME"; then
        return 1
    fi

    info "Existing csghub-lite service detected. Restarting it to load the new version..."
    if "$_server_bin" stop-service >/dev/null 2>&1; then
        sleep 1
        if ! "$_server_bin" ps >/dev/null 2>&1 && ! has_running_named_processes "$BINARY_NAME"; then
            return 0
        fi
    fi

    warn "Graceful stop-service did not complete; forcing running ${BINARY_NAME} processes to exit..."
    force_stop_named_processes "$BINARY_NAME" || true
    if "$_server_bin" ps >/dev/null 2>&1 || has_running_named_processes "$BINARY_NAME"; then
        warn "Could not stop the existing csghub-lite service automatically."
        warn "Restart it manually to use the new binary: ${_server_bin} stop-service && ${_server_bin} serve"
        SERVER_START_STATUS="stale"
        return 1
    fi
    return 0
}

start_csghub_lite_server() {
    _server_bin="$1"
    _restarted=false
    if restart_running_csghub_lite_server "$_server_bin"; then
        _restarted=true
    elif [ "${SERVER_START_STATUS:-}" = "stale" ]; then
        return 0
    fi

    if command -v nohup >/dev/null 2>&1; then
        nohup "$_server_bin" serve >/dev/null 2>&1 &
    else
        "$_server_bin" serve >/dev/null 2>&1 &
    fi

    sleep 1
    if "$_server_bin" ps >/dev/null 2>&1; then
        if [ "$_restarted" = true ]; then
            info "Restarted csghub-lite server in background."
            SERVER_START_STATUS="restarted"
        else
            info "Started csghub-lite server in background."
            SERVER_START_STATUS="started"
        fi
    else
        if [ "$_restarted" = true ]; then
            warn "Could not verify background server restart. Try: ${_server_bin} serve"
        else
            warn "Could not verify background server startup. Try: ${_server_bin} serve"
        fi
        SERVER_START_STATUS="failed"
    fi
}

main() {
    TOTAL_STEPS=6
    SERVER_START_STATUS="failed"
    printf "\n${BOLD}Installing ${BINARY_NAME}${NC}\n\n"

    # Step 1: Detect environment
    step 1 "$TOTAL_STEPS" "Detecting environment..."
    OS="$(detect_os)"
    ARCH="$(detect_arch)"
    REGION="$(detect_region)"
    info "OS: ${OS}, Arch: ${ARCH}, Region: ${REGION}"

    # Step 2: Check existing installation
    step 2 "$TOTAL_STEPS" "Checking for existing installation..."
    check_existing

    # Step 3: Resolve version
    step 3 "$TOTAL_STEPS" "Resolving version..."
    VERSION="${CSGHUB_LITE_VERSION:-}"
    if [ -z "$VERSION" ]; then
        VERSION="$(get_latest_version)" || true
        if [ -z "$VERSION" ]; then
            error "Could not determine latest version. Set CSGHUB_LITE_VERSION env var manually."
        fi
    fi
    info "Version: ${VERSION}"

    # Step 4: Download
    step 4 "$TOTAL_STEPS" "Downloading ${BINARY_NAME} ${VERSION}..."
    EXT="tar.gz"
    [ "$OS" = "windows" ] && EXT="zip"
    ARCHIVE_NAME="${BINARY_NAME}_${VERSION#v}_${OS}-${ARCH}.${EXT}"

    _github_url="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE_NAME}"
    _gitlab_url="${GITLAB_API}/${GITLAB_CSGHUB_ID}/packages/generic/${BINARY_NAME}/${VERSION#v}/${ARCHIVE_NAME}"

    TMPDIR="$(mktemp -d)"
    ARCHIVE_PATH="${TMPDIR}/${ARCHIVE_NAME}"
    if ! region_download "$ARCHIVE_PATH" "$_github_url" "$_gitlab_url"; then
        rm -rf "$TMPDIR"
        error "Failed to download ${BINARY_NAME} ${VERSION}."
    fi
    info "Download complete."

    # Step 5: Extract and install
    step 5 "$TOTAL_STEPS" "Installing..."
    case "$EXT" in
        tar.gz) tar xzf "$ARCHIVE_PATH" -C "$TMPDIR" ;;
        zip)    unzip -q "$ARCHIVE_PATH" -d "$TMPDIR" ;;
    esac

    BINARY_PATH="$(find "$TMPDIR" -name "$BINARY_NAME" -type f | head -1)"
    if [ -z "$BINARY_PATH" ]; then
        error "Binary not found in archive"
    fi
    chmod +x "$BINARY_PATH"

    EXISTING_BIN="$(command -v "$BINARY_NAME" 2>/dev/null || true)"
    resolve_install_dir "$EXISTING_BIN"
    ensure_dir_exists "$INSTALL_DIR"

    TARGET="${INSTALL_DIR}/${BINARY_NAME}"
    if [ "$OS" = "darwin" ] && [ -n "$EXISTING_BIN" ] && [ "$EXISTING_BIN" != "$TARGET" ]; then
        info "Installing to ${TARGET} instead of ${EXISTING_BIN} to avoid sudo on macOS."
    fi
    if [ -w "$INSTALL_DIR" ]; then
        mv "$BINARY_PATH" "$TARGET"
    else
        info "Requires root privileges to install to ${INSTALL_DIR}"
        run_privileged mv "$BINARY_PATH" "$TARGET"
    fi
    rm -rf "$TMPDIR"
    install_enterprise_license "$INSTALL_DIR"
    cleanup_previous_binary "$EXISTING_BIN" "$TARGET"

    ACTIVE_BIN="$(command -v "$BINARY_NAME" 2>/dev/null || true)"
    if [ -n "$ACTIVE_BIN" ] && [ "$ACTIVE_BIN" != "$TARGET" ]; then
        warn "Current PATH resolves ${BINARY_NAME} to ${ACTIVE_BIN}, not ${TARGET}"
    fi

    # Step 6: Install llama-server
    step 6 "$TOTAL_STEPS" "Setting up inference engine..."
    install_llama_server

    # Start API server in background by default after install
    start_csghub_lite_server "$TARGET"

    # Done
    printf "\n${GREEN}${BOLD}✔ ${BINARY_NAME} ${VERSION} installed successfully!${NC}\n\n"

    QUICKSTART_BIN="${BINARY_NAME}"
    if [ -n "$INSTALL_PATH_DIR" ]; then
        QUICKSTART_BIN="${TARGET}"
        warn "${INSTALL_DIR} is not on your current PATH yet."
        info "Added ${INSTALL_DIR} to future shells via ${INSTALL_PATH_PROFILE}"
        info "To use ${BINARY_NAME} in this shell now, run: export PATH=\"${INSTALL_PATH_DIR}:\$PATH\""
        info "Then run: hash -r"
        printf "\n"
    elif [ "$OS" = "darwin" ] && [ -n "$EXISTING_BIN" ] && [ "$EXISTING_BIN" != "$TARGET" ]; then
        QUICKSTART_BIN="${TARGET}"
        info "If your current shell still resolves the previous copy, run: hash -r"
        printf "\n"
    fi

    printf "${BOLD}Quick start:${NC}\n"
    if [ "$SERVER_START_STATUS" = "started" ] || [ "$SERVER_START_STATUS" = "restarted" ] || [ "$SERVER_START_STATUS" = "running" ]; then
        printf "  %s run Qwen/Qwen3-0.6B-GGUF    # Run a model\n" "$QUICKSTART_BIN"
        printf "  %s ps                          # List running models\n" "$QUICKSTART_BIN"
        printf "  %s stop-service                # Stop background server\n" "$QUICKSTART_BIN"
    else
        printf "  %s serve                       # Start server with Web UI\n" "$QUICKSTART_BIN"
        printf "  %s run Qwen/Qwen3-0.6B-GGUF    # Run a model\n" "$QUICKSTART_BIN"
        printf "  %s ps                          # List running models\n" "$QUICKSTART_BIN"
    fi
    printf "  %s login                       # Set CSGHub token\n" "$QUICKSTART_BIN"
    printf "  %s --help                      # Show all commands\n" "$QUICKSTART_BIN"
    printf "\n"
    printf "${BOLD}Web UI:${NC}\n"
    if [ "$SERVER_START_STATUS" = "stale" ]; then
        printf "  Existing server could not be restarted automatically. Run ${CYAN}%s stop-service && %s serve${NC}.\n" "$QUICKSTART_BIN" "$QUICKSTART_BIN"
    elif [ "$SERVER_START_STATUS" = "started" ] || [ "$SERVER_START_STATUS" = "restarted" ] || [ "$SERVER_START_STATUS" = "running" ]; then
        printf "  Server is already running. Open ${CYAN}http://localhost:11435${NC} in your browser.\n"
    else
        printf "  Start the server and open ${CYAN}http://localhost:11435${NC} in your browser.\n"
    fi
    printf "  Dashboard, Marketplace, Library and Chat are all available.\n"
    printf "\n"

    printf "${BOLD}Want more?${NC}\n"
    printf "  Visit ${CYAN}https://opencsg.com${NC} for advanced features,\n"
    printf "  enterprise solutions, and the full CSGHub platform.\n"
    printf "\n"
}

main "$@"
