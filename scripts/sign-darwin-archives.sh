#!/usr/bin/env bash
# Developer ID-sign csghub-lite inside macOS release archives and refresh checksums.
set -euo pipefail

dist_dir="${1:-dist}"
identity="${APPLE_SIGNING_IDENTITY:?Set APPLE_SIGNING_IDENTITY}"
team_id="${APPLE_TEAM_ID:-3S93B6Z434}"
codesign_bin="${CODESIGN:-codesign}"

if [[ ! -d "${dist_dir}" ]]; then
  echo "dist directory not found: ${dist_dir}" >&2
  exit 1
fi

shopt -s nullglob
archives=("${dist_dir}"/csghub-lite_*_darwin-*.tar.gz "${dist_dir}"/csghub-lite_*_darwin_*.tar.gz)
if [[ ${#archives[@]} -eq 0 ]]; then
  echo "no darwin archives in ${dist_dir}" >&2
  exit 1
fi

sign_one() {
  local archive="$1"
  local tmp bin details
  tmp="$(mktemp -d)"
  tar xzf "${archive}" -C "${tmp}"
  bin="${tmp}/csghub-lite"
  if [[ ! -f "${bin}" ]]; then
    echo "missing csghub-lite in ${archive}" >&2
    exit 1
  fi
  "${codesign_bin}" \
    --force \
    --sign "${identity}" \
    --options runtime \
    --timestamp \
    --identifier csghub-lite \
    "${bin}"
  "${codesign_bin}" --verify --strict --verbose=2 "${bin}"
  details="$("${codesign_bin}" -dv --verbose=4 "${bin}" 2>&1)"
  if ! grep -q "Developer ID Application" <<<"${details}"; then
    echo "signature for ${archive} is not Developer ID" >&2
    exit 1
  fi
  if ! grep -q "TeamIdentifier=${team_id}" <<<"${details}"; then
    echo "signature for ${archive} does not use team ${team_id}" >&2
    exit 1
  fi
  rm -f "${archive}"
  COPYFILE_DISABLE=1 tar czf "${archive}" --no-xattrs -C "${tmp}" .
  rm -rf "${tmp}"
  echo "Signed $(basename "${archive}")"
}

found_arm=0
found_amd=0
for archive in "${archives[@]}"; do
  name="$(basename "${archive}")"
  case "${name}" in
    *_darwin-arm64.tar.gz | *_darwin_arm64.tar.gz) found_arm=1 ;;
    *_darwin-amd64.tar.gz | *_darwin_amd64.tar.gz) found_amd=1 ;;
    *)
      echo "unexpected darwin archive: ${name}" >&2
      exit 1
      ;;
  esac
  sign_one "${archive}"
done

if [[ "${found_arm}" -ne 1 || "${found_amd}" -ne 1 ]]; then
  echo "expected both darwin-arm64 and darwin-amd64 archives" >&2
  exit 1
fi

script_dir="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
"${script_dir}/write-checksums.sh" "${dist_dir}"
echo "Updated checksums for signed macOS archives"
