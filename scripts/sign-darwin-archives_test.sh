#!/usr/bin/env bash
# Exercise archive rewriting with a fake codesign. No certificate required.
set -euo pipefail

root="$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

mock="${work}/codesign"
cat >"${mock}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "--verify" ]]; then
  exit 0
fi
if [[ "$1" == "-dv" ]]; then
  echo "Authority=Developer ID Application: Beijing OpenCSG Technology Co., Ltd. (3S93B6Z434)" >&2
  echo "TeamIdentifier=3S93B6Z434" >&2
  echo "Identifier=csghub-lite" >&2
  exit 0
fi
file="${*: -1}"
printf 'signed' >>"${file}"
EOF
chmod +x "${mock}"

make_archive() {
  local name="$1"
  local dir="${work}/src-${name}"
  mkdir -p "${dir}"
  printf 'binary-%s' "${name}" >"${dir}/csghub-lite"
  printf 'readme' >"${dir}/README.md"
  COPYFILE_DISABLE=1 tar czf "${work}/dist/${name}" --no-xattrs -C "${dir}" .
}

mkdir -p "${work}/dist"
make_archive "csghub-lite_9.9.9_darwin-arm64.tar.gz"
make_archive "csghub-lite_9.9.9_darwin-amd64.tar.gz"
printf 'untouched' >"${work}/linux-bin"
mkdir -p "${work}/linux"
printf 'untouched' >"${work}/linux/csghub-lite"
COPYFILE_DISABLE=1 tar czf "${work}/dist/csghub-lite_9.9.9_linux-amd64.tar.gz" --no-xattrs -C "${work}/linux" .
linux_before="$(shasum -a 256 "${work}/dist/csghub-lite_9.9.9_linux-amd64.tar.gz" | awk '{print $1}')"

APPLE_SIGNING_IDENTITY="Developer ID Application: Beijing OpenCSG Technology Co., Ltd. (3S93B6Z434)" \
  CODESIGN="${mock}" \
  "${root}/scripts/sign-darwin-archives.sh" "${work}/dist"

for platform in darwin-arm64 darwin-amd64; do
  archive="${work}/dist/csghub-lite_9.9.9_${platform}.tar.gz"
  extract="${work}/out-${platform}"
  mkdir -p "${extract}"
  tar xzf "${archive}" -C "${extract}"
  grep -q 'signed' "${extract}/csghub-lite"
  grep -q 'readme' "${extract}/README.md"
done

linux_after="$(shasum -a 256 "${work}/dist/csghub-lite_9.9.9_linux-amd64.tar.gz" | awk '{print $1}')"
if [[ "${linux_before}" != "${linux_after}" ]]; then
  echo "linux archive was modified" >&2
  exit 1
fi
grep -q 'csghub-lite_9.9.9_darwin-arm64.tar.gz' "${work}/dist/checksums.txt"
grep -q 'csghub-lite_9.9.9_linux-amd64.tar.gz' "${work}/dist/checksums.txt"
echo "sign-darwin-archives tests passed"
