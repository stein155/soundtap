# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Draw the iOS launch images: the launch skeleton from index.html, as a picture.

iOS shows an apple-touch-startup-image from the moment a home-screen SoundTap opens
until the page paints. The page itself cannot help before that: soundtap.local may
take seconds to resolve, and plain http has no service worker to serve a cached shell.
iOS ignores an image whose pixel size is not exactly the device's, so there is one
per screen size, in light and dark.

Run from app/frontend:
    docker run --rm -v "$PWD":/w -w /w python:3.12-slim \
        sh -c 'pip -q --disable-pip-version-check install pillow && python scripts/splash.py'
It writes public/splash/*.png and prints the <link> tags for index.html.
"""

from pathlib import Path

from PIL import Image, ImageDraw

# (css width, css height, pixel ratio, top safe area): every iPhone screen size in use.
SCREENS = [
    (440, 956, 3, 62),
    (430, 932, 3, 59),
    (428, 926, 3, 47),
    (414, 896, 3, 44),
    (414, 896, 2, 48),
    (402, 874, 3, 62),
    (393, 852, 3, 59),
    (390, 844, 3, 47),
    (375, 812, 3, 50),
    (375, 667, 2, 20),
]

# index.html's .ls colours.
THEMES = {
    "light": {"page": "#f0f0f0", "bg": "#f9f9f9", "surface": "#ffffff", "sk": "#e5e5e5"},
    "dark": {"page": "#0a0a0a", "bg": "#121212", "surface": "#1c1c1e", "sk": "#333333"},
}

SHELL = 393  # --shell-max


def draw(w: int, h: int, s: int, top: int, c: dict[str, str]) -> Image.Image:
    """Lay out the skeleton in css px and draw it at s device pixels per css px."""
    img = Image.new("RGB", (w * s, h * s), c["page"])
    d = ImageDraw.Draw(img)
    x0 = (w - min(w, SHELL)) / 2
    fw = min(w, SHELL)
    d.rectangle((x0 * s, 0, (x0 + fw) * s, h * s), fill=c["bg"])

    def sk(x: float, y: float, sw: float, sh: float, r: float, fill: str = c["sk"]) -> None:
        box = ((x0 + x) * s, y * s, (x0 + x + sw) * s, (y + sh) * s)
        d.rounded_rectangle(box, r * s, fill=fill)

    y = max(30, top + 12)  # .ls-hdr
    sk(20, y, 170, 20, 6)
    sk(20, y + 28, 120, 12, 6)
    sk(fw - 56, y + 2, 36, 36, 18)

    y += 40 + 18 + 18  # header block, its bottom padding, .ls-sect top padding
    sk(16, y, 64, 12, 6)
    y += 12 + 10
    for name, line in ((0.45, 0.30), (0.40, 0.25)):  # two .ls-room cards
        sk(16, y, fw - 32, 76, 22, c["surface"])
        sk(28, y + 12, 52, 52, 14)
        text = fw - 32 - 12 - 52 - 12 - 12 - 38 - 12
        sk(92, y + 22, text * name, 15, 6)
        sk(92, y + 44, text * line, 12, 6)
        sk(fw - 28 - 38, y + 19, 38, 38, 19)
        y += 76 + 10

    bottom = h - max(10, 34 + 6 if top > 20 else 10)  # .ls-mp
    sk(10, bottom - 62, fw - 20, 62, 22, c["surface"])
    sk(22, bottom - 50, (fw - 44) * 0.5, 12, 6)
    sk(22, bottom - 20, fw - 44, 8, 4)
    return img


def main() -> None:
    out = Path("public/splash")
    out.mkdir(parents=True, exist_ok=True)
    for w, h, dpr, top in SCREENS:
        for theme, colours in THEMES.items():
            big = draw(w, h, dpr, top, colours)
            name = f"{w}x{h}@{dpr}-{theme}.png"
            big.quantize(colors=8).save(out / name, optimize=True)
            print(
                f'    <link rel="apple-touch-startup-image" href="/splash/{name}" media="'
                f"(device-width: {w}px) and (device-height: {h}px) and "
                f"(-webkit-device-pixel-ratio: {dpr}) and (orientation: portrait) and "
                f'(prefers-color-scheme: {theme})" />'
            )


if __name__ == "__main__":
    main()
