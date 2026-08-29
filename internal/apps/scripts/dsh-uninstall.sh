#!/usr/bin/env bash
set -euo pipefail

emit_progress() {
  local progress="$1"
  local phase="$2"
  printf 'CSGHUB_PROGRESS|%s|%s\n' "${progress}" "${phase}"
}

emit_progress 20 uninstalling_dsh
install_root="${CSGHUB_LITE_DSH_INSTALL_ROOT:-${HOME}/.local/share/deepseek-harness}"
launcher_path="${HOME}/.local/bin/dsh"

rm -rf "${install_root}"
rm -f "${launcher_path}"

emit_progress 100 complete
printf '%s\n' "INFO: DeepSeek Harness uninstall complete."
