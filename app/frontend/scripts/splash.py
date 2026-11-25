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

from dataclasses import dataclass
from pathlib import Path

from PIL import Image, ImageDraw

# (css width, css height, pixel ratio, top safe area, bottom safe area): every iPhone
# screen size in use.
SCREEN_SIZES = [
    (440, 956, 3, 62, 34),
    (430, 932, 3, 59, 34),
    (428, 926, 3, 47, 34),
    (414, 896, 3, 44, 34),
    (414, 896, 2, 48, 34),
    (402, 874, 3, 62, 34),
    (393, 852, 3, 59, 34),
    (390, 844, 3, 47, 34),
    (375, 812, 3, 50, 34),
    (375, 667, 2, 20, 0),
]

# index.html's .ls colours; "line" is --border over --surface, "edge" the speaker bar's
# top line (rgba(22, 24, 27, 0.14)) over --surface-2.
THEMES = {
    "light": {
        "bg": "#f9f9f9",
        "surface": "#ffffff",
        "surface2": "#f0f0f0",
        "sk": "#e5e5e5",
        "bar": "#ebebeb",
        "skbar": "#d9d9d9",
        "line": "#efefef",
        "edge": "#d1d2d2",
    },
    "dark": {
        "bg": "#121212",
        "surface": "#1c1c1e",
        "surface2": "#2a2a2c",
        "sk": "#333333",
        "bar": "#2a2a2c",
        "skbar": "#3a3a3c",
        "line": "#2d2d2f",
        "edge": "#27272a",
    },
}

SHELL = 440  # --shell-max


@dataclass(frozen=True)
class Screen:
    """One iPhone screen: its size in css px, pixel ratio and safe areas."""

    w: int
    h: int
    dpr: int
    top: int
    bottom: int


SCREENS = [Screen(*size) for size in SCREEN_SIZES]


@dataclass(frozen=True)
class Box:
    """A rectangle in css px within the frame, with its corner radius."""

    x: float
    y: float
    w: float
    h: float
    r: float = 0


@dataclass
class Canvas:
    """The frame the skeleton is drawn into, centred in the screen at --shell-max."""

    draw: ImageDraw.ImageDraw
    colours: dict[str, str]
    scale: int
    fw: float
    x0: float

    def box(self, b: Box, fill: str) -> None:
        """Fill a box, clipped to the frame as .ls clips the rail's last tile."""
        right = min(b.x + b.w, self.fw)
        if right <= b.x:
            return
        s = self.scale
        xy = ((self.x0 + b.x) * s, b.y * s, (self.x0 + right) * s - 1, (b.y + b.h) * s - 1)
        if b.r:
            self.draw.rounded_rectangle(xy, b.r * s, fill=fill)
        else:
            self.draw.rectangle(xy, fill=fill)

    def sk(self, b: Box) -> None:
        """Fill a box in the skeleton colour."""
        self.box(b, self.colours["sk"])

    def section(self, y: float, tw: float) -> float:
        """Draw a .ls-bar section heading at y and return the y below it."""
        self.box(Box(0, y, self.fw, 43), self.colours["bar"])
        self.box(Box(16, y + 15.5, tw, 12, 6), self.colours["skbar"])
        return y + 43


def draw_top(c: Canvas, top: int) -> float:
    """Draw .ls-top: two 44 px icon boxes and the centred brand (24 px icon, 8 gap, 110 text)."""
    fw = c.fw
    y = top
    c.box(Box(0, 0, fw, top + 60), c.colours["surface"])
    c.sk(Box(12 + 9, y + 20, 26, 20, 4))
    c.sk(Box(fw - 12 - 44 + 9, y + 20, 26, 20, 4))
    bx = (fw - (24 + 8 + 110)) / 2
    c.sk(Box(bx, y + 18, 24, 24, 12))
    c.sk(Box(bx + 32, y + 23.5, 110, 13, 6))
    return y + 60


def draw_rail(c: Canvas, y: float) -> float:
    """Draw a section with .ls-rail's tiles and return the y below it."""
    y = c.section(y, 130)
    for i, name in enumerate((0.70, 0.40, 0.60)):
        x = 16 + i * (140 + 12)
        c.sk(Box(x, y + 14, 140, 140, 7))
        c.sk(Box(x, y + 14 + 150, 140 * name, 12, 6))
        c.sk(Box(x, y + 14 + 168, 140 * 0.45, 10, 5))
    return y + 14 + 179 + 18


def draw_list(c: Canvas, y: float) -> None:
    """Draw a section with .ls-list's two rows."""
    fw = c.fw
    y = c.section(y, 120)
    text = fw - 16 - 44 - 14 - 14 - 10 - 16  # .ls-row's text column
    c.box(Box(0, y, fw, 68 + 1 + 68), c.colours["surface"])
    for i, line in enumerate((0.65, 0.70)):
        if i:
            c.box(Box(0, y, fw, 1), c.colours["line"])
            y += 1
        c.sk(Box(16, y + 12, 44, 44, 7))
        c.sk(Box(74, y + 18.5, text * 0.30, 13, 6))
        c.sk(Box(74, y + 39.5, text * line, 10, 5))
        c.sk(Box(fw - 16 - 10, y + 26, 10, 16, 3))
        y += 68


def draw_speaker(c: Canvas, h: int, bottom: int) -> None:
    """Draw .ls-idle and the .ls-spk speaker bar at the bottom of the screen."""
    fw = c.fw
    pad = max(0, bottom - 16)  # .ls-spk's --spk-pad
    spk = h - 64 - pad
    idle = spk - 64
    c.box(Box(0, idle, fw, 64), c.colours["surface2"])  # .ls-idle
    c.sk(Box(16, idle + 18, 28, 28, 14))
    c.sk(Box(56, idle + 26, 110, 12, 6))
    c.box(Box(0, spk, fw, 64 + pad), c.colours["surface"])  # .ls-spk
    c.box(Box(0, spk - 1, fw, 1), c.colours["edge"])
    c.sk(Box(10, spk + 10, 44, 44, 22))
    c.sk(Box(66, spk + 17, 90, 12, 6))
    c.sk(Box(66, spk + 37, 70, 10, 5))
    c.sk(Box(fw - 18 - 20, spk + 22, 20, 20, 4))


def draw(screen: Screen, colours: dict[str, str]) -> Image.Image:
    """Lay out the skeleton in css px and draw it at the screen's device pixels per css px."""
    s = screen.dpr
    img = Image.new("RGB", (screen.w * s, screen.h * s), colours["bg"])
    fw = min(screen.w, SHELL)
    c = Canvas(ImageDraw.Draw(img), colours, s, fw, (screen.w - fw) / 2)
    y = draw_top(c, screen.top)
    y = draw_rail(c, y)
    draw_list(c, y)
    draw_speaker(c, screen.h, screen.bottom)
    return img


def main() -> None:
    """Write every launch image to public/splash and print its <link> tag for index.html."""
    out = Path("public/splash")
    out.mkdir(parents=True, exist_ok=True)
    for screen in SCREENS:
        w, h, dpr = screen.w, screen.h, screen.dpr
        for theme, colours in THEMES.items():
            big = draw(screen, colours)
            name = f"{w}x{h}@{dpr}-{theme}.png"
            big.quantize(colors=16).save(out / name, optimize=True)
            print(
                f'    <link rel="apple-touch-startup-image" href="/splash/{name}" media="'
                f"(device-width: {w}px) and (device-height: {h}px) and "
                f"(-webkit-device-pixel-ratio: {dpr}) and (orientation: portrait) and "
                f'(prefers-color-scheme: {theme})" />',
            )


if __name__ == "__main__":
    main()
