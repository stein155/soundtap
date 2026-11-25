// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { capped, line, type IconSpec } from './svg';

export const navigationIcons = {
  search: {
    svg: capped(),
    body: (
      <>
        <circle cx="11" cy="11" r="6.5" />
        <path d="m16 16 4 4" />
      </>
    ),
  },
  info: {
    svg: capped(),
    body: (
      <>
        <circle cx="12" cy="12" r="10" />
        <path d="M12 11v6M12 7v.5" />
      </>
    ),
  },
  // Opens in another app or tab.
  external: {
    svg: { ...capped('2'), strokeLinejoin: 'round' },
    body: (
      <>
        <path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
      </>
    ),
  },
  plus: {
    svg: capped(),
    body: (
      <>
        <path d="M12 5v14M5 12h14" />
      </>
    ),
  },
  close: {
    svg: capped(),
    body: (
      <>
        <path d="M6 6l12 12M18 6 6 18" />
      </>
    ),
  },
  back: {
    svg: line(),
    body: (
      <>
        <path d="m14 6-6 6 6 6" />
      </>
    ),
  },
  chevron: {
    svg: line('1.7'),
    body: (
      <>
        <path d="m9 6 6 6-6 6" />
      </>
    ),
  },
  check: {
    svg: line('2'),
    body: (
      <>
        <path d="m5 12 5 5L20 7" />
      </>
    ),
  },
  eye: {
    svg: line(),
    body: (
      <>
        <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" />
        <circle cx="12" cy="12" r="3" />
      </>
    ),
  },
  eyeOff: {
    svg: line(),
    body: (
      <>
        <path d="M10.7 6.7A8.6 8.6 0 0 1 12 6.5c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-2.7 3.4M6.4 7.9A16 16 0 0 0 2.5 13S6 19.5 12 19.5a8.5 8.5 0 0 0 3.6-.8" />
        <path d="M9.9 10.1a3 3 0 0 0 4.2 4.2" />
        <path d="M3.5 3.5 20.5 20.5" />
      </>
    ),
  },
  menu: {
    svg: { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: '2' },
    body: (
      <>
        <path d="M2 5h20M2 12h20M2 19h20" />
      </>
    ),
  },
  // Six buttons, as on the top of the speakers.
  presets: {
    svg: { viewBox: '0 0 28 24', fill: 'none', stroke: 'currentColor', strokeWidth: '2' },
    body: (
      <>
        <rect x="1" y="5" width="26" height="14" rx="1.5" />
        <path d="M1 12h26M9.67 5v14M18.33 5v14" />
      </>
    ),
  },
  chevronDown: {
    svg: line('2'),
    body: (
      <>
        <path d="m6 9 6 6 6-6" />
      </>
    ),
  },
  chevronUp: {
    svg: line('2'),
    body: (
      <>
        <path d="m6 15 6-6 6 6" />
      </>
    ),
  },
} satisfies Record<string, IconSpec>;
