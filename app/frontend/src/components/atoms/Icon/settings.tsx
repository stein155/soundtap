// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { capped, line, solid, type IconSpec } from './svg';

export const settingsIcons = {
  settings: {
    svg: line(),
    body: (
      <>
        <circle cx="12" cy="12" r="3" />
        <path d="M19.1 12.9a7.6 7.6 0 0 0 .06-1.8l1.86-1.45-1.86-3.22-2.2.88a7.5 7.5 0 0 0-1.56-.9L15 4h-3.7l-.4 2.4a7.5 7.5 0 0 0-1.56.9l-2.2-.88-1.86 3.22 1.86 1.45a7.6 7.6 0 0 0 0 1.82l-1.86 1.45 1.86 3.22 2.2-.88c.48.37 1 .67 1.56.9L11.3 20H15l.4-2.4c.56-.23 1.08-.53 1.56-.9l2.2.88 1.86-3.22-1.86-1.45Z" />
      </>
    ),
  },
  clock: {
    svg: capped('1.8'),
    body: (
      <>
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7v5.5l3 2" />
      </>
    ),
  },
  cog: {
    svg: solid,
    body: (
      <path
        fillRule="evenodd"
        d="M10.5 2h3l.5 2.6 1.6.7 2.3-1.5 2.1 2.1-1.5 2.3.7 1.6L22 10.5v3l-2.6.5-.7 1.6 1.5 2.3-2.1 2.1-2.3-1.5-1.6.7L13.5 22h-3l-.5-2.6-1.6-.7-2.3 1.5-2.1-2.1 1.5-2.3-.7-1.6L2 13.5v-3l2.6-.5.7-1.6-1.5-2.3 2.1-2.1 2.3 1.5 1.6-.7ZM12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Z"
      />
    ),
  },
  layers: {
    svg: line(),
    body: (
      <>
        <path d="m12 4 9 5-9 5-9-5 9-5Z" />
        <path d="m3 14 9 5 9-5" />
      </>
    ),
  },
  wifi: {
    svg: line(),
    body: (
      <>
        <path d="M3 9.5a13 13 0 0 1 18 0" />
        <path d="M6 13a8.5 8.5 0 0 1 12 0" />
        <path d="M9 16.5a4 4 0 0 1 6 0" />
        <circle cx="12" cy="19.5" r=".8" fill="currentColor" />
      </>
    ),
  },
  globe: {
    svg: line(),
    body: (
      <>
        <circle cx="12" cy="12" r="8.5" />
        <path d="M3.5 12h17" />
        <path d="M12 3.5c2.5 2.6 3.5 5.4 3.5 8.5s-1 5.9-3.5 8.5c-2.5-2.6-3.5-5.4-3.5-8.5s1-5.9 3.5-8.5Z" />
      </>
    ),
  },
  home: {
    svg: line(),
    body: <path d="M3 11l9-7 9 7v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z" />,
  },
  download: {
    svg: line(),
    body: (
      <>
        <path d="M12 4v11" />
        <path d="m7.5 10.5 4.5 4.5 4.5-4.5" />
        <path d="M5 20h14" />
      </>
    ),
  },
  upload: {
    svg: line('1.8'),
    body: (
      <>
        <path d="M12 16V4M7 9l5-5 5 5" />
        <path d="M4 20h16" />
      </>
    ),
  },
  // Back the way it came: the way back to Bose.
  undo: {
    svg: line('1.8'),
    body: (
      <>
        <path d="M9 14 4 9l5-5" />
        <path d="M4 9h11a5 5 0 0 1 0 10h-3" />
      </>
    ),
  },
  alarm: {
    svg: line(),
    body: (
      <>
        <circle cx="12" cy="13" r="7.5" />
        <path d="M12 9.5V13l2.5 1.5M4.5 5.5l3-2.5M19.5 5.5l-3-2.5" />
      </>
    ),
  },
  moon: {
    svg: line(),
    body: <path d="M19.5 14.5A8 8 0 0 1 9.5 4.5a8 8 0 1 0 10 10Z" />,
  },
  refresh: {
    svg: line('1.7'),
    body: (
      <>
        <path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" />
        <path d="M19.5 4.5v4h-4" />
      </>
    ),
  },
  power: {
    svg: capped('1.8'),
    body: (
      <>
        <path d="M12 3.5v8" />
        <path d="M6.6 6.8a7.5 7.5 0 1 0 10.8 0" />
      </>
    ),
  },
  shield: {
    svg: line(),
    body: <path d="M12 3.5 5 6v5.5c0 4.3 3 7.6 7 9 4-1.4 7-4.7 7-9V6l-7-2.5Z" />,
  },
  // A paired phone or tablet, on the Bluetooth page.
  phone: {
    svg: line(),
    body: (
      <>
        <rect x="6" y="2" width="12" height="20" rx="2.5" />
        <path d="M11 18h2" />
      </>
    ),
  },
  lock: {
    svg: line(),
    body: (
      <>
        <rect x="5" y="10.5" width="14" height="10" rx="2" />
        <path d="M8 10.5V8a4 4 0 0 1 8 0v2.5" />
      </>
    ),
  },
  display: {
    svg: line(),
    body: (
      <>
        <rect x="3.5" y="5" width="17" height="11.5" rx="2" />
        <path d="M9 20h6M12 16.5V20" />
      </>
    ),
  },
  folder: {
    svg: line(),
    body: (
      <path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z" />
    ),
  },
  pencil: {
    svg: line('1.8'),
    body: <path d="M4 20h4L19 9l-4-4L4 16Z" />,
  },
  help: {
    svg: line('1.8'),
    body: (
      <>
        <circle cx="12" cy="12" r="9" />
        <path d="M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6v.6M12 17h.01" />
      </>
    ),
  },
  link: {
    svg: line('1.8'),
    body: (
      <>
        <path d="M10 14a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1" />
        <path d="M14 10a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1" />
      </>
    ),
  },
  // A mains plug: is the speaker plugged in.
  plug: {
    svg: capped('1.8'),
    body: (
      <>
        <path d="M12 2v6M8 4v4M16 4v4" />
        <rect x="6" y="8" width="12" height="7" rx="2" />
        <path d="M12 15v7" />
      </>
    ),
  },
  // A hand pressing: the speaker asks for a press on its button.
  press: {
    svg: { ...capped('1.8'), strokeLinejoin: 'round' },
    body: (
      <>
        <path d="M9 11V5.5a1.5 1.5 0 0 1 3 0V11" />
        <path d="M12 10.5V4.5a1.5 1.5 0 0 1 3 0V11" />
        <path d="M15 10.5V6.5a1.5 1.5 0 0 1 3 0V14a7 7 0 0 1-7 7h-.5a6 6 0 0 1-4.6-2.2L3 15.3a1.5 1.5 0 0 1 2.3-1.9L9 16V11" />
      </>
    ),
  },
  // Two speakers of different heights: play together (now playing's Group).
  group: {
    svg: line('2'),
    body: (
      <>
        <rect x="2" y="7" width="8" height="13" rx="1.5" />
        <rect x="14" y="4" width="8" height="16" rx="1.5" />
      </>
    ),
  },
  stereo: {
    svg: line('1.8'),
    body: (
      <>
        <rect x="2" y="7" width="8" height="13" rx="1.5" />
        <rect x="14" y="7" width="8" height="13" rx="1.5" />
      </>
    ),
  },
  code: {
    svg: line('1.8'),
    body: <path d="m8 8-4 4 4 4M16 8l4 4-4 4M13.5 5l-3 14" />,
  },
  // Take care: a triangle with an exclamation mark.
  warning: {
    svg: line('2'),
    body: <path d="M12 3 22 21H2ZM12 10v5M12 18h.01" />,
  },
} satisfies Record<string, IconSpec>;
