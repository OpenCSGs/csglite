#!/bin/sh
set -eu

REPO_ROOT="$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
MODE="sync"
TAG=""
ASSETS_VERSION=""
ASSETS_MODULE="github.com/opencsgs/llama-cpp-assets"
INSTALL_SH="${REPO_ROOT}/scripts/install.sh"
INSTALL_PS1="${REPO_ROOT}/scripts/install.ps1"
INSTALL_GUIDE="${REPO_ROOT}/docs/getting-started/installation.md"

info() { printf "\033[0;32m[INFO]\033[0m %s\n" "$1"; }
die() { printf "\033[0;31m[ERROR]\033[0m %s\n" "$1" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: scripts/sync-llama-converter.sh [options]

Check llama.cpp lockstep or update CSGLite to a published llama-cpp-assets module.

Options:
  --check                   Verify dependency and installer lockstep
  --tag TAG                 Target llama.cpp release tag
  --assets-version VERSION  Published llama-cpp-assets module version
  -h, --help                Show this help

Upgrade order:
  1. In OpenCSGs/llama-cpp-assets, sync and publish the target llama.cpp tag.
  2. Run this script with --tag and --assets-version.
  3. Build or mirror matching llama-server binaries.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --check) MODE="check"; shift ;;
        --tag) TAG="$2"; shift 2 ;;
        --assets-version) ASSETS_VERSION="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done

cd "${REPO_ROOT}"

if [ "${MODE}" = "check" ]; then
    go test ./internal/convert -run 'TestBundled|TestLlamaCppVersionLockstepWithInstallScripts'
    CURRENT_ASSETS_VERSION="$(go list -m -f '{{.Version}}' "${ASSETS_MODULE}")"
    [ -n "${CURRENT_ASSETS_VERSION}" ] && [ "${CURRENT_ASSETS_VERSION}" != "(devel)" ] ||
        die "unable to determine a published ${ASSETS_MODULE} version"
    MODULE_JSON="$(go mod download -json "${ASSETS_MODULE}@${CURRENT_ASSETS_VERSION}")"
    ASSETS_DIR="$(printf "%s" "${MODULE_JSON}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')"
    [ -f "${ASSETS_DIR}/scripts/sync-llama-cpp.sh" ] ||
        die "${ASSETS_MODULE}@${CURRENT_ASSETS_VERSION} does not include its source validation script"
    sh "${ASSETS_DIR}/scripts/sync-llama-cpp.sh" --check
    info "llama.cpp asset dependency and installer tags are in lockstep"
    exit 0
fi

[ -n "${TAG}" ] || die "--tag is required when updating llama.cpp"
[ -n "${ASSETS_VERSION}" ] || die "--assets-version is required; publish the dependency repository first"

command -v go >/dev/null 2>&1 || die "go not found on PATH"
command -v python3 >/dev/null 2>&1 || die "python3 not found on PATH"

MODULE_JSON="$(go mod download -json "${ASSETS_MODULE}@${ASSETS_VERSION}")"
ASSETS_DIR="$(printf "%s" "${MODULE_JSON}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')"
ASSETS_REF="$(sed -n 's/^[[:space:]]*LlamaCppRef = "\(.*\)"$/\1/p' "${ASSETS_DIR}/assets.go")"
[ "${ASSETS_REF}" = "${TAG}" ] ||
    die "${ASSETS_MODULE}@${ASSETS_VERSION} contains ${ASSETS_REF:-unknown}, expected ${TAG}"

go get "${ASSETS_MODULE}@${ASSETS_VERSION}"

python3 - "${INSTALL_SH}" "${INSTALL_PS1}" "${INSTALL_GUIDE}" "${TAG}" <<'PY'
from pathlib import Path
import re
import sys

install_sh_path = Path(sys.argv[1])
install_ps1_path = Path(sys.argv[2])
install_guide_path = Path(sys.argv[3])
tag = sys.argv[4]

install_sh = install_sh_path.read_text(encoding="utf-8")
install_sh, shell_count = re.subn(
    r'LLAMA_CPP_DEFAULT_TAG="\$\{CSGHUB_LITE_LLAMA_CPP_TAG:-[^}]+\}"',
    f'LLAMA_CPP_DEFAULT_TAG="${{CSGHUB_LITE_LLAMA_CPP_TAG:-{tag}}}"',
    install_sh,
    count=1,
)
if shell_count != 1:
    raise SystemExit("failed to update scripts/install.sh")
install_sh_path.write_text(install_sh, encoding="utf-8")

install_ps1 = install_ps1_path.read_text(encoding="utf-8")
install_ps1, powershell_count = re.subn(
    r'\$LlamaCppDefaultTag = if \(\$env:CSGHUB_LITE_LLAMA_CPP_TAG\) \{ \$env:CSGHUB_LITE_LLAMA_CPP_TAG \} else \{ "[^"]+" \}',
    f'$LlamaCppDefaultTag = if ($env:CSGHUB_LITE_LLAMA_CPP_TAG) {{ $env:CSGHUB_LITE_LLAMA_CPP_TAG }} else {{ "{tag}" }}',
    install_ps1,
    count=1,
)
if powershell_count != 1:
    raise SystemExit("failed to update scripts/install.ps1")
install_ps1_path.write_text(install_ps1, encoding="utf-8")

guide = install_guide_path.read_text(encoding="utf-8")
guide, guide_count = re.subn(
    r'\| `CSGHUB_LITE_LLAMA_CPP_TAG` \| .* \|',
    "| `CSGHUB_LITE_LLAMA_CPP_TAG` | 指定要安装的 `llama.cpp` release tag。默认固定到与 `llama-cpp-assets` 依赖对齐的 tag，确保 converter、`gguf-py` 和 `llama-server` 版本一致。 |",
    guide,
    count=1,
)
if guide_count != 1:
    raise SystemExit("failed to update installation guide")
install_guide_path.write_text(guide, encoding="utf-8")
PY

go mod tidy
go test ./internal/convert -run 'TestBundled|TestLlamaCppVersionLockstepWithInstallScripts'
info "Updated CSGLite to ${ASSETS_MODULE}@${ASSETS_VERSION} for llama.cpp ${TAG}"
