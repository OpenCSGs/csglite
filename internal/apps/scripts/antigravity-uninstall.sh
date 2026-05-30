#!/usr/bin/env bash
set -euo pipefail

emit_progress() {
  printf 'CSGHUB_PROGRESS|%s|%s\n' "$1" "$2"
}

log() {
  printf '%s\n' "$*"
}

emit_progress 5 preflight

launchers=(
  "${HOME}/.local/bin/agy"
  "${HOME}/bin/agy"
)

emit_progress 55 removing_runtime
for launcher in "${launchers[@]}"; do
  rm -f "${launcher}"
done
rm -rf "${HOME}/.cache/antigravity/staging"
hash -r 2>/dev/null || true

emit_progress 80 verifying_uninstall
if command -v agy >/dev/null 2>&1; then
  log "ERROR: Antigravity binary is still available at $(command -v agy)"
  exit 1
fi

emit_progress 100 complete
log "INFO: Antigravity uninstallation complete"
