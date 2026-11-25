// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The browser's own bar (Android's status bar, a pinned app's title bar) takes its colour
// from <meta name="theme-color">. It follows the page's scheme, and a full-screen layer
// with its own background (now playing) sets its colour while it shows.

export const THEME_COLOR = { light: '#f9f9f9', dark: '#121212' };

const meta = () => document.querySelector('meta[name="theme-color"]');

// The colour a layer set, which outlasts a change of scheme while the layer shows.
let layer: string | null = null;

// The colour the bar shows now: the layer's, else the page's for its current scheme.
export function applyThemeColor(): void {
  const dark = document.documentElement.getAttribute('data-theme') === 'dark';
  meta()?.setAttribute('content', layer ?? THEME_COLOR[dark ? 'dark' : 'light']);
}

export function setThemeColor(color: string): void {
  layer = color;
  applyThemeColor();
}

// Back to the page's own colour, for its current scheme.
export function resetThemeColor(): void {
  layer = null;
  applyThemeColor();
}

// The colour at the top of now playing: its wash over the darkened artwork, as an rgb
// "r g b" string from artColor.
export function washColor(wash: string): string {
  const [r = 0, g = 0, b = 0] = wash.split(' ').map((n) => Math.round(Number(n) * 0.55));
  const hex = (n: number) => Math.max(0, Math.min(255, n)).toString(16).padStart(2, '0');
  return `#${hex(r)}${hex(g)}${hex(b)}`;
}
