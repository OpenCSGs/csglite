#!/usr/bin/env bash
set -euo pipefail

runtime_root="${HOME}/.local/share/zcode"
launcher_path="${HOME}/.local/bin/zcode"

printf 'CSGHUB_PROGRESS|20|removing_runtime\n'
if [[ -L "${runtime_root}/current" ]]; then
  rm -f "${runtime_root}/current"
fi
if [[ -d "${runtime_root}" ]]; then
  rm -rf "${runtime_root}"
fi

printf 'CSGHUB_PROGRESS|80|removing_launcher\n'
rm -f "${launcher_path}"

printf 'CSGHUB_PROGRESS|100|complete\n'
printf 'INFO: removed the managed ZCode runtime and launcher\n'
