#!/bin/sh
set -eu
mkdir -p "$TMPDIR" "$FREETOKEN_HOME"
exec ft "$@"
