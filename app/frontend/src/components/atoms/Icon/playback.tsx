// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { line, solid, type IconSpec } from './svg';

export const playbackIcons = {
  shuffle: {
    svg: line(),
    body: (
      <>
        <path d="M3 7h3c1.7 0 3.2.9 4 2.3l3.9 5.4c.8 1.4 2.3 2.3 4 2.3H21" />
        <path d="M3 17h3c1.4 0 2.6-.6 3.4-1.6M14.6 8.6c.8-1 2-1.6 3.4-1.6h3" />
        <path d="m18.5 4.5 2.5 2.5-2.5 2.5M18.5 14.5 21 17l-2.5 2.5" />
      </>
    ),
  },
  repeat: {
    svg: line(),
    body: (
      <>
        <path d="M4 11V9.5A2.5 2.5 0 0 1 6.5 7H20" />
        <path d="M20 13v1.5a2.5 2.5 0 0 1-2.5 2.5H4" />
        <path d="M17.5 4.5 20 7l-2.5 2.5M6.5 14.5 4 17l2.5 2.5" />
      </>
    ),
  },
  repeatTrack: {
    svg: line(),
    body: (
      <>
        <path d="M4 11V9.5A2.5 2.5 0 0 1 6.5 7H20" />
        <path d="M20 13v1.5a2.5 2.5 0 0 1-2.5 2.5H4" />
        <path d="M17.5 4.5 20 7l-2.5 2.5M6.5 14.5 4 17l2.5 2.5" />
        <path d="M11 11l1.2-1v4.5" />
      </>
    ),
  },
  play: {
    svg: solid,
    body: (
      <path d="M7 5.5v13a.5.5 0 0 0 .77.42l10.5-6.5a.5.5 0 0 0 0-.84L7.77 5.08A.5.5 0 0 0 7 5.5Z" />
    ),
  },
  pause: {
    svg: solid,
    body: (
      <>
        <rect x="6.5" y="5" width="4" height="14" rx="1" />
        <rect x="13.5" y="5" width="4" height="14" rx="1" />
      </>
    ),
  },
  stop: {
    svg: solid,
    body: <rect x="6" y="6" width="12" height="12" rx="1.5" />,
  },
  heart: {
    svg: {
      viewBox: '0 0 24 24',
      fill: 'none',
      stroke: 'currentColor',
      strokeWidth: '1.6',
      strokeLinejoin: 'round',
    },
    body: (
      <path d="M12 20s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7.2a4.3 4.3 0 0 1 7.5 2.6C19.500 15.400 12 20 12 20z" />
    ),
  },
  more: {
    svg: solid,
    body: (
      <>
        <circle cx="6" cy="12" r="1.6" />
        <circle cx="12" cy="12" r="1.6" />
        <circle cx="18" cy="12" r="1.6" />
      </>
    ),
  },
  volume: {
    svg: line(),
    body: (
      <>
        <path d="M4 10v4h3l4 3.5V6.5L7 10H4Z" />
        <path d="M15 9.5a3.5 3.5 0 0 1 0 5" />
      </>
    ),
  },
  mute: {
    svg: line(),
    body: (
      <>
        <path d="M4 10v4h3l4 3.5V6.5L7 10H4Z" />
        <path d="m15 10 5 4M20 10l-5 4" />
      </>
    ),
  },
  dots: {
    svg: solid,
    body: (
      <>
        <circle cx="12" cy="5.5" r="1.7" />
        <circle cx="12" cy="12" r="1.7" />
        <circle cx="12" cy="18.5" r="1.7" />
      </>
    ),
  },
  skipPrev: {
    svg: {
      viewBox: '0 0 24 24',
      fill: 'currentColor',
      stroke: 'currentColor',
      strokeWidth: '1.5',
      strokeLinejoin: 'round',
    },
    body: <path d="M6 5v14M18 5.5v13L8.5 12Z" />,
  },
  skipNext: {
    svg: {
      viewBox: '0 0 24 24',
      fill: 'currentColor',
      stroke: 'currentColor',
      strokeWidth: '1.5',
      strokeLinejoin: 'round',
    },
    body: <path d="M18 5v14M6 5.5v13L15.5 12Z" />,
  },
} satisfies Record<string, IconSpec>;
