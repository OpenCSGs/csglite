#!/usr/bin/env bash
set -euo pipefail

emit_progress() {
  local progress="$1"
  local phase="$2"
  printf 'CSGHUB_PROGRESS|%s|%s\n' "${progress}" "${phase}"
}

emit_progress 20 uninstalling_kimi_code
install_dir="${KIMI_INSTALL_DIR:-${HOME}/.kimi-code}"
launcher_path="${HOME}/.local/bin/kimi"

rm -rf "${install_dir}"
rm -f "${launcher_path}"

emit_progress 100 complete
printf '%s\n' "INFO: Kimi Code uninstall complete."
