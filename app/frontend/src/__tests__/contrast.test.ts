// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import css from '../theme/GlobalStyle?raw';
import homeCss from '../pages/HomePage/home.css?raw';

// Contrast is the one accessibility rule that can be checked without a browser: it
// is arithmetic on the tokens themselves. Everything else about the UI on a phone
// (focus order, target size, what a screen reader announces) needs a real device;
// this at least stops a nice-looking colour from shipping as unreadable text.
//
// Thresholds are WCAG 2.1 AA for body text (4.5:1). Tokens are read out of
// GlobalStyle rather than duplicated here, so editing a colour is what fails.

function tokens(block: string): Record<string, string> {
  const start = css.indexOf(block);
  if (start < 0) throw new Error(`no ${block} block in GlobalStyle`);
  const body = css.slice(start, css.indexOf('}', start));
  const out: Record<string, string> = {};
  for (const [, name, value] of body.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/gi)) {
    out[name] = value;
  }
  return out;
}

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = channels.map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const surfaces = ['bg', 'bg-grey', 'section-bar', 'surface', 'surface-2', 'surface-3'];
const inks = ['ink', 'ink-2', 'ink-3'];

describe.each([
  ['light', ':root {'],
  ['dark', "[data-theme='dark'] {"],
])('%s theme', (_name, block) => {
  const t = tokens(block);

  it('danger is readable where it is used', () => {
    for (const surface of ['bg-grey', 'surface']) {
      expect(ratio(t.danger, t[surface]), `danger on ${surface}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it.each(inks)('%s is readable on every surface', (ink) => {
    for (const surface of surfaces) {
      expect(ratio(t[ink], t[surface]), `${ink} on ${surface}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  // The accent is both a text colour and a fill. Everything drawn on an accent
  // fill uses --on-accent, so that pairing is the one that has to hold — white
  // was hardcoded, and on the dark theme's brighter accent it reads at 2.6:1.
  it('accent works as text and as a filled control', () => {
    for (const surface of surfaces) {
      expect(ratio(t.accent, t[surface]), `accent on ${surface}`).toBeGreaterThanOrEqual(4.5);
    }
    expect(ratio(t.accent, t['on-accent']), 'on-accent over accent').toBeGreaterThanOrEqual(4.5);
  });

  // --accent-fill only ever carries icons (checkmarks, play/stop glyphs), so the bar is
  // WCAG's 3:1 for graphical objects -- which is what lets it stay the design's orange.
  it('accent fill carries its icons and stands out from a card', () => {
    expect(ratio(t['accent-fill'], t['on-accent-fill'])).toBeGreaterThanOrEqual(3);
    expect(ratio(t['accent-fill'], t.surface)).toBeGreaterThanOrEqual(3);
  });
});

// The slider thumb is the handle people grab. In the light theme a white thumb reads by
// its shadow; on the dark cards a shadow is invisible, so the thumb itself has to stand
// out.
it('dark slider thumb stands out from the card and the rail', () => {
  const t = tokens("[data-theme='dark'] {");
  for (const surface of ['surface', 'surface-3']) {
    expect(ratio(t.thumb, t[surface]), `thumb on ${surface}`).toBeGreaterThanOrEqual(3);
  }
});

// Fixed black text vanishes on the dark theme: the drawer's rows read at 1.1:1. Text takes an ink token.
it('home.css sets no translucent black text', () => {
  expect(homeCss).not.toMatch(/(^|[\s;{])color:\s*rgba\(0,\s*0,\s*0/m);
});
