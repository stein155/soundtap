#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Regenerates the ST20 panel atlas and the Chromium goldens, in throwaway containers.
#   app/oled/design/golden.sh <repo root>      (ATLAS_ONLY=1 skips the Chromium goldens)
# Fonts (SIL OFL) come from the google/fonts repository; their licence is OFL.txt.
set -eu
root=$(cd "$1" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fonts=https://raw.githubusercontent.com/google/fonts/main/ofl
mkdir -p "$work/fonts"
curl -fsSL -o "$work/fonts/PlusJakartaSans.ttf" "$fonts/plusjakartasans/PlusJakartaSans%5Bwght%5D.ttf"
curl -fsSL -o "$work/fonts/Silkscreen-Regular.ttf" "$fonts/silkscreen/Silkscreen-Regular.ttf"
curl -fsSL -o "$work/fonts/Manrope.ttf" "$fonts/manrope/Manrope%5Bwght%5D.ttf"
curl -fsSL -o "$work/OFL.txt" "$fonts/plusjakartasans/OFL.txt"
for dir in silkscreen manrope; do { echo; echo "---- $dir"; curl -fsSL "$fonts/$dir/OFL.txt"; } >> "$work/OFL.txt"; done
# Thai, Japanese, Korean and Chinese: only the letters the panel's own words use.
for f in "Thai[wdth,wght]" "JP[wght]" "KR[wght]" "SC[wght]" "TC[wght]"; do
  name=NotoSans${f%%\[*}
  dir=$(echo "$name" | tr A-Z a-z)
  curl -fsSL -o "$work/fonts/$name.ttf" "$fonts/$dir/$(echo "NotoSans$f" | sed 's/\[/%5B/; s/\]/%5D/; s/,/%2C/').ttf"
  { echo; echo "---- $name"; curl -fsSL "$fonts/$dir/OFL.txt"; } >> "$work/OFL.txt"
done
cat "$root"/app/internal/panel/words.go > "$work/words.txt"
cp "$root"/app/oled/design/*.html "$root"/app/oled/design/*.js "$root"/app/oled/design/*.py "$work/"
docker run --rm -e HINT -e PHASES -v "$work:/w" -w /w python:3.13-slim@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90 sh -c \
  'pip -q install freetype-py pillow uharfbuzz >/dev/null && HINT=${HINT:-light} PHASES=${PHASES:-4} python -I atlas.py fonts . words.txt'
[ -n "${ATLAS_ONLY:-}" ] || docker run --rm -v "$work:/w" -w /w debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818 sh -c \
  'apt-get -qq update >/dev/null && apt-get -qq install -y chromium python3-pil >/dev/null &&
   chromium --headless --no-sandbox --disable-gpu --allow-file-access-from-files \
     --virtual-time-budget=10000 --dump-dom file:///w/golden.html > dom.html &&
   python3 -I golden_png.py dom.html golden'
cp "$work/st20-atlas.png" "$work/st20-atlas.txt.gz" "$work/OFL.txt" "$root/app/oled/screen/assets/"
[ -n "${ATLAS_ONLY:-}" ] || { mkdir -p "$root/app/oled/screen/testdata"; cp "$work"/golden/*.png "$root/app/oled/screen/testdata/"; }
