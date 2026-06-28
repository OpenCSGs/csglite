#!/bin/sh
set -eu

ROOT="$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
FORMULA_PATH="${ROOT}/Formula/csghub-lite.rb"
CHECKSUMS_PATH="${ROOT}/dist/checksums.txt"
TAG=""

usage() {
    cat <<'EOF'
Usage: scripts/update-homebrew-formula.sh --tag vX.Y.Z [--checksums dist/checksums.txt]

Update Formula/csghub-lite.rb from local release checksums.
Run this after `make package`, which writes dist/checksums.txt.
EOF
}

die() {
    printf '%s\n' "$1" >&2
    exit 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --tag)
            TAG="${2:-}"
            shift 2
            ;;
        --checksums)
            CHECKSUMS_PATH="${2:-}"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1"
            ;;
    esac
done

if [ -z "$TAG" ]; then
    TAG="$(git -C "$ROOT" describe --tags --exact-match 2>/dev/null || true)"
fi

[ -n "$TAG" ] || die "tag is required (use --tag vX.Y.Z or run on a tagged commit)"
[ -f "$CHECKSUMS_PATH" ] || die "checksums file not found: $CHECKSUMS_PATH"

VERSION="${TAG#v}"

lookup_checksum() {
    while [ $# -gt 0 ]; do
        filename="$1"
        checksum="$(awk -v name="$filename" '$2 == name { print $1; exit }' "$CHECKSUMS_PATH")"
        if [ -n "$checksum" ]; then
            printf '%s\n' "$checksum"
            return 0
        fi
        shift
    done
    return 1
}

darwin_arm64="$(lookup_checksum \
    "csghub-lite_${VERSION}_darwin-arm64.tar.gz" \
    "csghub-lite_${VERSION}_darwin_arm64.tar.gz")" || die "missing darwin arm64 checksum"
darwin_amd64="$(lookup_checksum \
    "csghub-lite_${VERSION}_darwin-amd64.tar.gz" \
    "csghub-lite_${VERSION}_darwin_amd64.tar.gz")" || die "missing darwin amd64 checksum"
linux_arm64="$(lookup_checksum \
    "csghub-lite_${VERSION}_linux-arm64.tar.gz" \
    "csghub-lite_${VERSION}_linux_arm64.tar.gz")" || die "missing linux arm64 checksum"
linux_amd64="$(lookup_checksum \
    "csghub-lite_${VERSION}_linux-amd64.tar.gz" \
    "csghub-lite_${VERSION}_linux_amd64.tar.gz")" || die "missing linux amd64 checksum"

cat > "$FORMULA_PATH" <<EOF
class CsghubLite < Formula
  desc "Lightweight tool for running LLMs locally with CSGHub platform"
  homepage "https://github.com/opencsgs/csglite"
  version "${VERSION}"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-arm64.tar.gz"
      sha256 "${darwin_arm64}"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_darwin-amd64.tar.gz"
      sha256 "${darwin_amd64}"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-arm64.tar.gz"
      sha256 "${linux_arm64}"
    end

    on_intel do
      url "https://github.com/OpenCSGs/csglite/releases/download/v#{version}/csghub-lite_#{version}_linux-amd64.tar.gz"
      sha256 "${linux_amd64}"
    end
  end

  depends_on "llama.cpp" => :recommended

  def install
    bin.install "csghub-lite"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/csghub-lite --version")
  end
end
EOF

printf '%s\n' "Updated ${FORMULA_PATH} for ${TAG}"
