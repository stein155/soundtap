# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Draw the iOS launch images and the wordmark of index.html's launch screen.

iOS shows an apple-touch-startup-image from the moment a home-screen SoundTap opens
until the page paints. The page itself cannot help before that: soundtap.local may
take seconds to resolve, and plain http has no service worker to serve a cached shell.
iOS ignores an image whose pixel size is not exactly the device's, so there is one
per screen size, in light and dark.

The picture is index.html's launch screen (#launch) at rest: the SoundTap mark with the
wordmark under it, a little above the middle. The page draws the wordmark as a path, so
it looks the same before the UI font has loaded; this script prints that path from the
font the app ships, shaped by HarfBuzz as the browser shapes it.

Run from app/frontend, after npm ci (for the font):
    docker run --rm -v "$PWD":/w -w /w python:3.12-slim sh -c 'pip -q \
        --disable-pip-version-check install pillow fonttools brotli uharfbuzz \
        && python scripts/splash.py'
It writes public/splash/*.png and prints the <link> tags and the wordmark for index.html.
"""

import io
from dataclasses import dataclass
from pathlib import Path

import uharfbuzz as hb
from fontTools.ttLib import TTFont
from PIL import Image, ImageDraw, ImageFont

FONT = Path("node_modules/@fontsource-variable/montserrat/files/montserrat-latin-wght-normal.woff2")
WORD = "SoundTap"
WEIGHT = 700
SIZE = 22  # css px

# (css width, css height, pixel ratio): every iPhone screen size in use.
SCREEN_SIZES = [
    (440, 956, 3),
    (430, 932, 3),
    (428, 926, 3),
    (414, 896, 3),
    (414, 896, 2),
    (402, 874, 3),
    (393, 852, 3),
    (390, 844, 3),
    (375, 812, 3),
    (375, 667, 2),
]

# index.html's #launch colours.
THEMES = {
    "light": {"bg": "#f9f9f9", "ink": "#111111"},
    "dark": {"bg": "#121212", "ink": "#ffffff"},
}

# The mark of docs/assets/logo.svg, cut to its ink (index.html's viewBox="60 40 162 172") and
# drawn at MARK_SCALE css px per unit: a note (open head, straight stem) and the grille holes
# of the sound leaving it, the outer four at 60%.
MARK_BOX = (60, 40, 162, 172)
MARK_SCALE = 0.6
STROKE = 16
HEAD = (96, 176, 28)
STEM = (124, 60, 176)
INNER = [(152, 76), (168, 100), (176, 128)]
OUTER = [(172, 48), (196, 76), (210, 108), (214, 142)]
DOT = 8
GAP = 18  # css px from the mark to the wordmark's ink
LIFT = 24  # css px the pair sits above the middle


@dataclass(frozen=True)
class Wordmark:
    """The wordmark's ink box in css px at SIZE (x, y, w, h) and its SVG path."""

    x: float
    y: float
    w: float
    h: float
    path: str


def wordmark() -> Wordmark:
    """Shape WORD at WEIGHT and SIZE and return its outline and ink box, y down."""
    sfnt = io.BytesIO()
    font_file = TTFont(FONT)
    font_file.flavor = None
    font_file.save(sfnt)
    face = hb.Face(sfnt.getvalue())
    font = hb.Font(face)
    font.set_variations({"wght": WEIGHT})
    buf = hb.Buffer()
    buf.add_str(WORD)
    buf.guess_segment_properties()
    hb.shape(font, buf, {"kern": True, "liga": True})
    k = SIZE / face.upem

    parts: list[str] = []
    origin = [0.0]

    def p(x: float, y: float) -> str:
        return f"{(origin[0] + x) * k:.1f} {-y * k:.1f}"

    funcs = hb.DrawFuncs()
    funcs.set_move_to_func(lambda x, y, _: parts.append("M" + p(x, y)))
    funcs.set_line_to_func(lambda x, y, _: parts.append("L" + p(x, y)))
    funcs.set_quadratic_to_func(lambda cx, cy, x, y, _: parts.append("Q" + p(cx, cy) + " " + p(x, y)))
    funcs.set_cubic_to_func(lambda c1x, c1y, c2x, c2y, x, y, _: parts.append("C" + p(c1x, c1y) + " " + p(c2x, c2y) + " " + p(x, y)))
    funcs.set_close_path_func(lambda _: parts.append("Z"))

    xs: list[float] = []
    ys: list[float] = []
    pen_x = 0
    for info, pos in zip(buf.glyph_infos, buf.glyph_positions, strict=True):
        origin[0] = pen_x + pos.x_offset
        funcs.draw_glyph(font, info.codepoint, None)
        ext = font.get_glyph_extents(info.codepoint)
        if ext and ext.width:
            xs += [origin[0] + ext.x_bearing, origin[0] + ext.x_bearing + ext.width]
            ys += [ext.y_bearing, ext.y_bearing + ext.height]
        pen_x += pos.x_advance
    x0, x1 = min(xs) * k, max(xs) * k
    top, bottom = -max(ys) * k, -min(ys) * k
    return Wordmark(x0, top, x1 - x0, bottom - top, "".join(parts))


def draw(screen: tuple[int, int, int], colours: dict[str, str], word: Wordmark) -> Image.Image:
    """Draw #launch at rest, laid out in css px, at the screen's device pixels per css px."""
    w, h, s = screen
    img = Image.new("RGB", (w * s, h * s), colours["bg"])
    d = ImageDraw.Draw(img)
    ink = colours["ink"]
    mw, mh = MARK_BOX[2] * MARK_SCALE, MARK_BOX[3] * MARK_SCALE
    top = (h - (mh + GAP + word.h)) / 2 - LIFT
    left = (w - mw) / 2
    k = MARK_SCALE * s

    def pt(x: float, y: float) -> tuple[float, float]:
        return (left * s + (x - MARK_BOX[0]) * k, top * s + (y - MARK_BOX[1]) * k)

    def disc(x: float, y: float, r: float, fill: str | tuple[int, ...]) -> None:
        cx, cy = pt(x, y)
        d.ellipse((cx - r * k, cy - r * k, cx + r * k, cy + r * k), fill=fill)

    hx, hy, hr = HEAD
    a, b = pt(hx - hr - STROKE / 2, hy - hr - STROKE / 2), pt(hx + hr + STROKE / 2, hy + hr + STROKE / 2)
    d.ellipse((*a, *b), outline=ink, width=round(STROKE * k))
    sx, sy0, sy1 = STEM
    a, b = pt(sx - STROKE / 2, sy0), pt(sx + STROKE / 2, sy1)
    d.rectangle((*a, *b), fill=ink)
    disc(sx, sy0, STROKE / 2, ink)  # the stem's round cap

    fg = Image.new("RGB", (1, 1), ink).getpixel((0, 0))
    bg = Image.new("RGB", (1, 1), colours["bg"]).getpixel((0, 0))
    faded = tuple(round(f * 0.6 + g * 0.4) for f, g in zip(fg, bg, strict=True))
    for x, y in INNER:
        disc(x, y, DOT, ink)
    for x, y in OUTER:
        disc(x, y, DOT, faded)

    font = ImageFont.truetype(str(FONT), SIZE * s, layout_engine=ImageFont.Layout.RAQM)
    font.set_variation_by_axes([WEIGHT])
    bx0, by0, _, _ = font.getbbox(WORD)
    d.text(((w - word.w) / 2 * s - bx0, (top + mh + GAP) * s - by0), WORD, font=font, fill=ink)
    return img


def main() -> None:
    """Write every launch image to public/splash; print the <link> tags and the wordmark."""
    word = wordmark()
    out = Path("public/splash")
    out.mkdir(parents=True, exist_ok=True)
    for screen in SCREEN_SIZES:
        w, h, dpr = screen
        for theme, colours in THEMES.items():
            name = f"{w}x{h}@{dpr}-{theme}.png"
            draw(screen, colours, word).quantize(colors=16).save(out / name, optimize=True)
            print(
                f'    <link rel="apple-touch-startup-image" href="/splash/{name}" media="'
                f"(device-width: {w}px) and (device-height: {h}px) and "
                f"(-webkit-device-pixel-ratio: {dpr}) and (orientation: portrait) and "
                f'(prefers-color-scheme: {theme})" />',
            )
    print(f'\n<svg width="{word.w:.2f}" height="{word.h:.2f}" ', end="")
    print(f'viewBox="{word.x:.2f} {word.y:.2f} {word.w:.2f} {word.h:.2f}">')
    print(f'<path d="{word.path}"/></svg>')


if __name__ == "__main__":
    main()
