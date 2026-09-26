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
# The output is kept, and shown if a golden fails, since a picture of a screen that changed
# unnoticed is worse than no picture.
if ! CANOPY_SCREENS_DIR="$frames" go test -count=1 -run Golden ./internal/tui/... >"$frames/test.log" 2>&1; then
  cat "$frames/test.log" >&2
  exit 1
fi
mkdir -p "$out"
if ! command -v freeze >/dev/null 2>&1; then
  echo "freeze is not installed (go install github.com/charmbracelet/freeze@v0.2.2); keeping the ANSI frames" >&2
  cp "$frames"/*.ansi "$out"/
  exit 0
fi
for frame in "$frames"/*.ansi; do
  name=$(basename "$frame" .ansi)
  freeze --execute "cat $frame" --window --padding 20 --output "$out/$name.svg" >/dev/null
done
ls "$out"
