// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { createGlobalStyle } from 'styled-components';
import { desktop } from './media';

// Design tokens and the document-wide reset. Components read the tokens as CSS custom
// properties, so the dark theme is a second set of values on <html data-theme="dark">
// (set before first paint by index.html and at runtime by hooks/useThemeMode).
//
// The palette is the Stockholm design's: neutral off-white surfaces, near-black ink and
// black filled controls (white in the dark theme), small corner radii and open letter
// spacing. --ink-3 is the design's light grey darkened until it passes WCAG AA on every
// surface (contrast.test.ts holds every token to it).
export const GlobalStyle = createGlobalStyle`
  :root {
    --bg: #f9f9f9;
    --surface: #ffffff;
    --surface-2: #f0f0f0;
    --rail: #f0f0f0; /* a slider's empty track */
    --surface-3: #e5e5e5;
    --border: rgba(22, 24, 27, 0.07);
    --border-strong: rgba(22, 24, 27, 0.14);
    --ink: #111111;
    --ink-2: #333333;
    --ink-3: #5e5e5e;
    --accent: #111111;
    --on-accent: #ffffff;
    --accent-fill: #111111;
    --on-accent-fill: #ffffff;
    --accent-soft: #ececec;
    --on-ink: #ffffff;
    --glow-color: #ffffff;
    --thumb: #ffffff;
    /* The plate behind station logos: light enough for dark lettering, softer than white. */
    --logo-plate: #f1efec;
    --shimmer: rgba(255, 255, 255, 0.55);
    --tracking-title: -0.018em;
    /* Stockholm's one curve, for pages, the drawer, the tray and now playing. */
    --ease: cubic-bezier(0, 0.4, 0.4, 1);
    --bg-grey: #f2f2f2;
    --section-bar: #ebebeb;
    --danger: #b90000;
    --backdrop: rgba(187, 187, 187, 0.4);
    --radius: 7px;
    --radius-sm: 4px;
    --shell-max: 393px;
    --shadow-sm: 0 1px 2px rgba(31, 24, 20, 0.05), 0 0 0 1px rgba(31, 24, 20, 0.04);
    --shadow: 0 8px 24px -8px rgba(31, 24, 20, 0.14), 0 0 0 1px rgba(31, 24, 20, 0.04);
    --shadow-lg: 0 22px 50px -16px rgba(31, 24, 20, 0.2);
  }

  @media ${desktop} {
    :root {
      --shell-max: 1000px;
    }
  }

  /* Dark theme: the same cool greys inverted, the accent brightened so it still reads.
     Set on <html> before paint (index.html) and at runtime (theme/themeMode.ts). */
  [data-theme='dark'] {
    --bg: #121212;
    --bg-grey: #161618;
    --section-bar: #2a2a2c;
    --danger: #ff6b60;
    --backdrop: rgba(0, 0, 0, 0.5);
    --surface: #1c1c1e;
    --surface-2: #2a2a2c;
    /* 3.4:1 on --bg: the surface grey left the track of a slider near invisible. */
    --rail: #6a6a6c;
    --surface-3: #333333;
    --border: rgba(238, 240, 243, 0.08);
    --border-strong: rgba(238, 240, 243, 0.16);
    --ink: #ffffff;
    --ink-2: #e6e6e6;
    --ink-3: #a3a3a3;
    --accent: #f2f2f2;
    --on-accent: #111111;
    --accent-fill: #f2f2f2;
    --on-accent-fill: #111111;
    --accent-soft: #2a2a2c;
    --logo-plate: #cfcbc6;
    --on-ink: #141619;
    --glow-color: #1d2024;
    --thumb: #eef0f3;
    --shimmer: rgba(255, 255, 255, 0.06);
    --shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(0, 0, 0, 0.3);
    --shadow: 0 8px 24px -8px rgba(0, 0, 0, 0.55), 0 0 0 1px rgba(0, 0, 0, 0.35);
    --shadow-lg: 0 22px 50px -16px rgba(0, 0, 0, 0.65);
  }

  * {
    box-sizing: border-box;
  }

  html,
  body {
    margin: 0;
    padding: 0;
    color: var(--ink);
    font-family: 'Montserrat Variable', -apple-system, sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    height: 100dvh;
    overflow: hidden;
    overscroll-behavior: none;
    /* pan-y alone also turns off pinch-zoom, which index.html promises to keep
       (WCAG 1.4.4). */
    touch-action: pan-y pinch-zoom;
  }

  html {
    background: var(--bg);
  }

  body {
    background: var(--bg);
    /* The app is a control surface: a long press on a tile must not select its text. */
    user-select: none;
    -webkit-user-select: none;
  }


  button {
    font: inherit;
    color: inherit;
    background: none;
    border: 0;
    padding: 0;
    cursor: pointer;
  }

  input,
  textarea,
  select {
    font: inherit;
    color: inherit;
    user-select: text;
    -webkit-user-select: text;
  }

  /* Keyboard focus has to be visible, and only for the keyboard: every control here is
     also a touch target, and a ring that lingers after a tap reads as "stuck". */
  :focus-visible {
    outline: 3px solid var(--accent);
    outline-offset: 2px;
    border-radius: var(--radius-sm);
  }

  :focus:not(:focus-visible) {
    outline: none;
  }

  ::selection {
    background: var(--accent);
    color: var(--on-accent);
  }
`;
