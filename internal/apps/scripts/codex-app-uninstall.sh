#!/usr/bin/env bash
set -euo pipefail

runtime_root="${HOME}/.local/share/codex-app"
launcher_path="${HOME}/.local/bin/codex-app"

if [[ -L "${runtime_root}/current" || -d "${runtime_root}/current" ]]; then
  rm -rf "${runtime_root}/current"
fi
if [[ -d "${runtime_root}/versions" ]]; then
  rm -rf "${runtime_root}/versions"
fi
rm -f "${runtime_root}/version" "${runtime_root}/launch-target"
rmdir "${runtime_root}" 2>/dev/null || true

rm -f "${launcher_path}"
printf 'INFO: removed Codex App launcher and user data under %s\n' "${runtime_root}"
