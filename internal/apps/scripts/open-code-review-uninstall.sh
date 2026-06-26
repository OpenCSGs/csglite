#!/usr/bin/env bash
set -euo pipefail

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

log() {
  printf '%s\n' "$*"
}

RUNTIME_ROOT="${HOME}/.local/share/open-code-review"
launchers=(
  "${HOME}/.local/bin/ocr"
  "${HOME}/bin/ocr"
)

emit_progress 5 preflight

emit_progress 55 removing_runtime
for launcher in "${launchers[@]}"; do
  rm -f "${launcher}"
done
rm -rf "${RUNTIME_ROOT}"
hash -r 2>/dev/null || true

emit_progress 80 verifying_uninstall
if command -v ocr >/dev/null 2>&1; then
  log "ERROR: Open Code Review binary is still available at $(command -v ocr)"
  exit 1
fi

emit_progress 100 complete
log "INFO: Open Code Review uninstallation complete"
