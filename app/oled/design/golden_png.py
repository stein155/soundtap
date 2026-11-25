# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Turns golden.html's dumped frames into 16-level grey PNGs: golden_png.py <dom> <dir>."""

import re
import sys
from pathlib import Path

from PIL import Image

dom, outdir = sys.argv[1:3]
Path(outdir).mkdir(parents=True, exist_ok=True)
with Path(dom).open(encoding="utf-8") as f:
    text = re.search(r'<pre id="out">(.*?)</pre>', f.read(), re.DOTALL).group(1)
for line in text.strip().splitlines():
    name, hexes = line.split()
    if len(hexes) != 128 * 100:
        raise AssertionError(name)
    Image.frombytes("L", (128, 100), bytes(int(c, 16) * 17 for c in hexes)).save(f"{outdir}/{name}.png")
