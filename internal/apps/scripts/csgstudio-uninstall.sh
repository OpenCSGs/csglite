#!/usr/bin/env bash
set -euo pipefail

runtime_root="${HOME}/.local/share/csgstudio"

printf 'CSGHUB_PROGRESS|20|stopping_services\n'
target=""
if [[ -f "${runtime_root}/launch-target" ]]; then
  target="$(tr -d '\r\n' < "${runtime_root}/launch-target")"
fi
managed_bundle=""
if [[ -f "${runtime_root}/managed-bundle" ]]; then
  managed_bundle="$(tr -d '\r\n' < "${runtime_root}/managed-bundle")"
fi
if [[ -n "$target" && ( "$target" == "${runtime_root}/"* || "$target" == "$managed_bundle" ) ]]; then
  if [[ -d "$target" ]]; then
    app_name="$(basename "$target" .app)"
    osascript -e "if application \"${app_name}\" is running then tell application \"${app_name}\" to quit" >/dev/null 2>&1 || true
  elif [[ -f "$target" ]]; then
    pkill -TERM -f "$target" >/dev/null 2>&1 || true
  fi
fi

printf 'CSGHUB_PROGRESS|60|removing_runtime\n'
if [[ "$managed_bundle" == *.app && -d "$managed_bundle" ]]; then
  rm -rf "$managed_bundle"
fi
rm -rf "${runtime_root}/current" "${runtime_root}/versions"
rm -f "${runtime_root}/version" "${runtime_root}/launch-target" "${runtime_root}/managed-bundle"
rmdir "$runtime_root" 2>/dev/null || true

printf 'CSGHUB_PROGRESS|100|complete\n'
printf 'INFO: removed the managed CSGStudio installation%s\n' "${managed_bundle:+ at ${managed_bundle}}"
