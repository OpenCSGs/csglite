#!/usr/bin/env bash
set -euo pipefail

runtime_root="${HOME}/.local/share/csgclaw-desktop"

printf 'CSGHUB_PROGRESS|20|stopping_services\n'
target=""
if [[ -f "${runtime_root}/launch-target" ]]; then
  target="$(tr -d '\r\n' < "${runtime_root}/launch-target")"
fi
if [[ -n "$target" && "$target" == "${runtime_root}/"* && -d "$target" ]]; then
  app_name="$(basename "$target" .app)"
  osascript -e "if application \"${app_name}\" is running then tell application \"${app_name}\" to quit" >/dev/null 2>&1 || true
fi

printf 'CSGHUB_PROGRESS|60|removing_runtime\n'
rm -rf "${runtime_root}/current" "${runtime_root}/versions"
rm -f "${runtime_root}/version" "${runtime_root}/launch-target"
rmdir "$runtime_root" 2>/dev/null || true

printf 'CSGHUB_PROGRESS|100|complete\n'
printf 'INFO: removed the managed CSGClaw Desktop installation under %s\n' "$runtime_root"
