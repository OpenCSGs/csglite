#!/usr/bin/env bash
# Import the Developer ID certificate into an ephemeral keychain for codesign.
set -euo pipefail

: "${APPLE_CERTIFICATE:?Set APPLE_CERTIFICATE to the base64-encoded Developer ID .p12}"
: "${APPLE_CERTIFICATE_PASSWORD:?Set APPLE_CERTIFICATE_PASSWORD}"

certificate_path="${RUNNER_TEMP:-/tmp}/apple-signing.p12"
keychain_path="${RUNNER_TEMP:-/tmp}/csglite-signing.keychain-db"
keychain_password="${KEYCHAIN_PASSWORD:-$(openssl rand -base64 32)}"

APPLE_CERTIFICATE="$(printf '%s' "${APPLE_CERTIFICATE}" | tr -d '[:space:]')"
APPLE_CERTIFICATE_PASSWORD="$(printf '%s' "${APPLE_CERTIFICATE_PASSWORD}" | tr -d '\r\n')"
printf '%s' "${APPLE_CERTIFICATE}" | base64 --decode >"${certificate_path}"
rm -f "${keychain_path}"
security create-keychain -p "${keychain_password}" "${keychain_path}"
security set-keychain-settings -lut 21600 "${keychain_path}"
security unlock-keychain -p "${keychain_password}" "${keychain_path}"

import_p12() {
  security import "$1" \
    -k "${keychain_path}" \
    -P "${APPLE_CERTIFICATE_PASSWORD}" \
    -T /usr/bin/codesign \
    -T /usr/bin/security
}

# Current macOS rejects some OpenSSL 3 PKCS#12 MAC algorithms. Re-export with
# the legacy encoder and import that file when the original import fails.
if ! import_p12 "${certificate_path}"; then
  echo "PKCS12 import failed; retrying with a legacy certificate" >&2
  pem_path="${RUNNER_TEMP:-/tmp}/apple-signing.pem"
  legacy_path="${RUNNER_TEMP:-/tmp}/apple-signing-legacy.p12"
  openssl pkcs12 \
    -in "${certificate_path}" \
    -out "${pem_path}" \
    -nodes \
    -passin env:APPLE_CERTIFICATE_PASSWORD
  openssl pkcs12 \
    -export \
    -legacy \
    -in "${pem_path}" \
    -out "${legacy_path}" \
    -passout env:APPLE_CERTIFICATE_PASSWORD
  import_p12 "${legacy_path}"
  rm -f "${pem_path}" "${legacy_path}"
fi
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "${keychain_password}" "${keychain_path}"
previous=()
while IFS= read -r line; do
  line="${line#"${line%%[![:space:]]*}"}"
  line="${line%\"}"
  line="${line#\"}"
  [[ -n "${line}" && "${line}" != "${keychain_path}" ]] && previous+=("${line}")
done < <(security list-keychains -d user)
security list-keychains -d user -s "${keychain_path}" "${previous[@]}"
security default-keychain -d user -s "${keychain_path}"
rm -f "${certificate_path}"

if [[ -z "${APPLE_SIGNING_IDENTITY:-}" ]]; then
  APPLE_SIGNING_IDENTITY="$(
    security find-identity -v -p codesigning "${keychain_path}" \
      | awk -F'"' '/Developer ID Application/ { print $2; exit }'
  )"
  if [[ -z "${APPLE_SIGNING_IDENTITY}" ]]; then
    echo "Could not detect a Developer ID Application identity" >&2
    security find-identity -v -p codesigning "${keychain_path}" >&2
    exit 1
  fi
fi

if [[ -n "${GITHUB_ENV:-}" ]]; then
  {
    printf 'APPLE_SIGNING_IDENTITY<<__CSGLITE_ENV_EOF__\n'
    printf '%s\n' "${APPLE_SIGNING_IDENTITY}"
    printf '__CSGLITE_ENV_EOF__\n'
  } >>"${GITHUB_ENV}"
fi

security find-identity -v -p codesigning "${keychain_path}"
echo "Imported Apple signing certificate"
