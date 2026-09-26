#!/bin/sh
# screens.sh [out]: the demo images, drawn from the same fixtures the golden screen tests check.
#
# The tests write each frame, colour included, when CANOPY_SCREENS_DIR is set; freeze turns each
# into an SVG. Nothing here calls a model or reads a real key: the frames come from the fakes the
# tests use, so the pictures are reproducible and cannot show something the program does not draw.
set -eu
out=${1:-dist/screens}
frames=$(mktemp -d)
trap 'rm -rf "$frames"' EXIT
CANOPY_SCREENS_DIR="$frames" go test -count=1 -run Golden ./internal/tui/... >/dev/null
mkdir -p "$out"
if ! command -v freeze >/dev/null 2>&1; then
  echo "freeze is not installed (go install github.com/charmbracelet/freeze@latest); keeping the ANSI frames" >&2
  cp "$frames"/*.ansi "$out"/
  exit 0
fi
for frame in "$frames"/*.ansi; do
  name=$(basename "$frame" .ansi)
  freeze --execute "cat $frame" --window --padding 20 --output "$out/$name.svg" >/dev/null
done
ls "$out"
