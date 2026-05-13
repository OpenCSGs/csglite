#!/usr/bin/env bash
set -euo pipefail

binary_name="${CSGHUB_LITE_BINARY_NAME:-csghub-lite}"
install_url="${CSGHUB_LITE_INSTALL_URL:-https://hub.opencsg.com/csghub-lite/install.sh}"
install_dir="${INSTALL_DIR:-/usr/local/bin}"
llama_install_dir="${CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR:-${install_dir}}"

has_csghub_lite() {
    command -v "${binary_name}" >/dev/null 2>&1
}

installed_version_matches() {
    local requested="${CSGHUB_LITE_VERSION:-}"
    local requested_without_v="${requested#v}"
    local current=""

    [ -n "${requested}" ] || return 1
    current="$("${binary_name}" --version 2>/dev/null || true)"
    [ -n "${current}" ] || return 1

    case "${current}" in
        *"${requested}"*|*"${requested_without_v}"*) return 0 ;;
        *) return 1 ;;
    esac
}

needs_install() {
    if [ "${CSGHUB_LITE_INSTALL_ALWAYS:-0}" = "1" ]; then
        return 0
    fi
    if ! has_csghub_lite; then
        return 0
    fi
    if [ -n "${CSGHUB_LITE_VERSION:-}" ] && ! installed_version_matches; then
        return 0
    fi
    return 1
}

install_csghub_lite() {
    local tmp_script="/tmp/csghub-lite-install.sh"

    echo "Installing csghub-lite${CSGHUB_LITE_VERSION:+ ${CSGHUB_LITE_VERSION}}..."
    curl -fsSL "${install_url}" -o "${tmp_script}"
    CSGHUB_LITE_FORCE="${CSGHUB_LITE_FORCE:-1}" \
        INSTALL_DIR="${install_dir}" \
        CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR="${llama_install_dir}" \
        sh "${tmp_script}"
    rm -f "${tmp_script}"

    # The public installer starts a background service for desktop installs.
    # Containers run the requested command in the foreground instead.
    "${binary_name}" stop-service >/dev/null 2>&1 || true
}

if needs_install; then
    install_csghub_lite
else
    echo "csghub-lite already installed; set CSGHUB_LITE_INSTALL_ALWAYS=1 to reinstall on startup."
fi

if [ "$#" -eq 0 ]; then
    set -- serve --listen 0.0.0.0:11435
fi

case "$1" in
    serve|run|chat|pull|list|show|ps|stop|stop-service|restart|restart-service|restart-server|reload|rm|login|search|config|upgrade|apps|launch|--help|--version|-*)
        exec "${binary_name}" "$@"
        ;;
    csghub-lite)
        exec "$@"
        ;;
    *)
        exec "$@"
        ;;
esac
