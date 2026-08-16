# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Bake the ST20 panel faces into one glyph atlas for app/oled/screen.

The design draws text with Plus Jakarta Sans (500-800) and Silkscreen at fixed pixel
sizes. The speaker has no font rasteriser, so every size the design uses is rendered
here once, with FreeType's light hinting and unhinted advances, as Chromium on Linux
renders canvas text; golden.sh checks the Go side against Chromium.

usage: atlas.py <font dir> <out dir> [words file]   (writes st20-atlas.png and st20-atlas.txt.gz)

Thai, Japanese, Korean and Chinese letters are baked only as far as the words file (the
panel's translations) uses them: the whole of those scripts would not fit the binary.
"""

import gzip
import os
import sys

import freetype
import uharfbuzz as hb
from PIL import Image

TEXT = [*range(0x20, 0x7F), *range(0xA0, 0x250), *range(0x384, 0x3CF), *range(0x400, 0x460), *range(0x1E00, 0x1F00),
        *range(0x2010, 0x2027), 0x2030, 0x2039, 0x203A, 0x20AC, 0x2122]
DIGITS = [*map(ord, "0123456789:- "), 0x2013, 0x2014]  # en and em dash

# name, file, weight, pixel size, runes. Names are what app/oled/screen asks for. A rune
# the face's own font lacks comes from FALLBACK, as a browser falls back: Jakarta has
# no Greek or Cyrillic, Silkscreen nothing past Latin-1.
# Han letters come from the first CJK font that has them (Japanese first): at these sizes
# the regional forms do not differ visibly.
ASIAN = ["NotoSansThai.ttf", "NotoSansJP.ttf", "NotoSansKR.ttf", "NotoSansSC.ttf", "NotoSansTC.ttf"]
FALLBACK = {"PlusJakartaSans.ttf": ["Manrope.ttf", *ASIAN],
            "Silkscreen-Regular.ttf": ["PlusJakartaSans.ttf", "Manrope.ttf", *ASIAN]}
FACES = [
    ("j500-7.5", "PlusJakartaSans.ttf", 500, 7.5, TEXT),
    ("j500-8", "PlusJakartaSans.ttf", 500, 8, TEXT),
    ("j600-7.5", "PlusJakartaSans.ttf", 600, 7.5, TEXT),
    ("j600-8", "PlusJakartaSans.ttf", 600, 8, TEXT),
    ("j600-9", "PlusJakartaSans.ttf", 600, 9, TEXT),
    ("j700-9", "PlusJakartaSans.ttf", 700, 9, TEXT),
    ("j700-10", "PlusJakartaSans.ttf", 700, 10, TEXT),
    ("j700-32", "PlusJakartaSans.ttf", 700, 32, [*DIGITS, *map(ord, " AMP")]),
    ("j800-8.5", "PlusJakartaSans.ttf", 800, 8.5, DIGITS),
    ("j800-11", "PlusJakartaSans.ttf", 800, 11, TEXT),
    ("j800-12", "PlusJakartaSans.ttf", 800, 12, TEXT),
    ("j800-13", "PlusJakartaSans.ttf", 800, 13, TEXT),
    ("j800-15", "PlusJakartaSans.ttf", 800, 15, TEXT),
    ("j800-26", "PlusJakartaSans.ttf", 800, 26, [*range(0x20, 0x7F)]),
    ("j800-34", "PlusJakartaSans.ttf", 800, 34, DIGITS),
    ("j800-40", "PlusJakartaSans.ttf", 800, 40, DIGITS),
    ("pix-7", "Silkscreen-Regular.ttf", 0, 7, TEXT),
    ("pix-8", "Silkscreen-Regular.ttf", 0, 8, TEXT),
]
WIDTH = 1024
# Kerning is kept for pairs of these: what station, track and room names are made of.
KERN = [*range(0x20, 0x7F), *range(0xC0, 0x180)]


def kerning(fontdir, file, weight):
    """Yields (left, right, font units) for every kerned pair HarfBuzz applies."""
    blob = hb.Blob.from_file_path(f"{fontdir}/{file}")
    font = hb.Font(hb.Face(blob))
    if weight:
        font.set_variations({"wght": weight})
    have = [r for r in KERN if font.get_nominal_glyph(r)]
    alone = {r: font.get_glyph_h_advance(font.get_nominal_glyph(r)) for r in have}
    for a in have:
        for b in have:
            buf = hb.Buffer()
            buf.add_str(chr(a) + chr(b))
            buf.guess_segment_properties()
            hb.shape(font, buf, {"kern": True, "liga": False})
            pos = buf.glyph_positions
            if len(pos) == 2 and pos[0].x_advance != alone[a]:
                yield a, b, pos[0].x_advance - alone[a]


HINT = os.environ.get("HINT", "light")
PHASES = int(os.environ.get("PHASES", "4"))


def open_face(fontdir, file, weight, size):
    face = freetype.Face(f"{fontdir}/{file}")
    if face.has_multiple_masters:
        axes = face.get_variation_info().axes
        face.set_var_design_coords([(weight or 600) if a.tag == "wght" else a.default for a in axes])
    face.set_char_size(int(size * 64), 0, 72, 72)
    return face


def glyphs(fontdir, file, weight, size, runes):
    chain = [open_face(fontdir, f, weight, size) for f in [file, *FALLBACK.get(file, [])]]
    flags = freetype.FT_LOAD_NO_BITMAP | {"none": freetype.FT_LOAD_NO_HINTING,
                                          "light": freetype.FT_LOAD_TARGET_LIGHT}[HINT]
    for r in runes:
        face = next((f for f in chain if f.get_char_index(r)), None)
        if face is None:
            continue
        for phase in range(PHASES):
            # Each glyph is drawn at PHASES sub-pixel offsets: Chromium places canvas
            # glyphs at fractional positions, and a glyph snapped to whole pixels lands
            # up to half a pixel off.
            face.set_transform(freetype.Matrix(0x10000, 0, 0, 0x10000), freetype.Vector(phase * 64 // PHASES, 0))
            face.load_char(chr(r), flags)
            g = face.glyph
            g.render(freetype.FT_RENDER_MODE_NORMAL)
            bm = g.bitmap
            img = Image.frombytes("L", (bm.width, bm.rows), bytes(bm.buffer)) if bm.width and bm.rows else None
            # linearHoriAdvance is 16.16 and unhinted, as Chromium lays out canvas text; kept
            # at full precision, since a 1/64 pixel rounding moved centred text a pixel.
            yield r, phase, img, g.bitmap_left, g.bitmap_top, f"{g.linearHoriAdvance / 65536:.4f}".rstrip("0").rstrip(".")


def main(fontdir, outdir, words=None):
    if words:
        with open(words, encoding="utf-8") as f:
            extra = sorted({ord(c) for c in f.read() if ord(c) >= 0xE00 and ord(c) not in TEXT})
        TEXT.extend(extra)
    lines, placed = [], []
    x = y = row = 0
    for file, weight in sorted({(f[1], f[2]) for f in FACES}):
        upem = freetype.Face(f"{fontdir}/{file}").units_per_EM
        lines.append(f"w {file} {weight} {upem}")
        lines += [f"k {a:x} {b:x} {u}" for a, b, u in kerning(fontdir, file, weight)]
    for name, file, weight, size, runes in FACES:
        lines.append(f"f {name} {PHASES} {file} {weight} {size}")
        for r, phase, img, left, top, adv in glyphs(fontdir, file, weight, size, runes):
            w, h = img.size if img else (0, 0)
            if x + w > WIDTH:
                x, y, row = 0, y + row + 1, 0
            if img:
                placed.append((img, x, y))
            lines.append(f"g {r:x} {phase} {x} {y} {w} {h} {left} {top} {adv}")
            x, row = x + w + 1, max(row, h)
    atlas = Image.new("L", (WIDTH, y + row + 1))
    for img, px, py in placed:
        atlas.paste(img, (px, py))
    atlas.save(f"{outdir}/st20-atlas.png", optimize=True)
    with gzip.open(f"{outdir}/st20-atlas.txt.gz", "wt", compresslevel=9) as f:
        f.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main(*sys.argv[1:4])
