// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { line, type IconSpec } from './svg';

export const devicesIcons = {
  speaker: {
    svg: line(),
    body: (
      <>
        <rect x="6" y="3" width="12" height="18" rx="2.5" />
        <circle cx="12" cy="14" r="3" />
        <circle cx="12" cy="7.5" r=".7" fill="currentColor" />
      </>
    ),
  },
  // The three SoundTouch models, seen from the front: the perforated grille, and the
  // ST20's display window.
  st10: {
    svg: line('1.5'),
    body: (
      <>
        <rect x="5.5" y="2" width="13" height="20" rx="3" />
        <path
          d="M8.2 5.2h.01M10.4 5.2h.01M12.6 5.2h.01M14.8 5.2h.01M8.2 7.4h.01M10.4 7.4h.01M12.6 7.4h.01M14.8 7.4h.01M8.2 9.6h.01M10.4 9.6h.01M12.6 9.6h.01M14.8 9.6h.01M8.2 11.8h.01M10.4 11.8h.01M12.6 11.8h.01M14.8 11.8h.01M8.2 14.0h.01M10.4 14.0h.01M12.6 14.0h.01M14.8 14.0h.01M8.2 16.2h.01M10.4 16.2h.01M12.6 16.2h.01M14.8 16.2h.01"
          strokeWidth="1.2"
        />
        <path d="M10.8 18.9h2.4" strokeWidth="1.1" />
      </>
    ),
  },
  st20: {
    svg: line('1.5'),
    body: (
      <>
        <rect x="1.5" y="4.5" width="21" height="15" rx="3" />
        <rect x="9.6" y="7" width="4.8" height="2.2" rx=".5" fill="currentColor" stroke="none" />
        <path
          d="M4.4 11.2h.01M6.6 11.2h.01M8.8 11.2h.01M11.0 11.2h.01M13.2 11.2h.01M15.4 11.2h.01M17.6 11.2h.01M19.8 11.2h.01M4.4 13.4h.01M6.6 13.4h.01M8.8 13.4h.01M11.0 13.4h.01M13.2 13.4h.01M15.4 13.4h.01M17.6 13.4h.01M19.8 13.4h.01M4.4 15.6h.01M6.6 15.6h.01M8.8 15.6h.01M11.0 15.6h.01M13.2 15.6h.01M15.4 15.6h.01M17.6 15.6h.01M19.8 15.6h.01"
          strokeWidth="1.2"
        />
        <path d="M4.4 7.0h.01M6.6 7.0h.01M4.4 9.2h.01M6.6 9.2h.01" strokeWidth="1.2" />
        <path d="M17.6 7.0h.01M19.8 7.0h.01M17.6 9.2h.01M19.8 9.2h.01" strokeWidth="1.2" />
      </>
    ),
  },
  wla: {
    svg: line('1.5'),
    body: (
      <>
        <rect x="4" y="7" width="16" height="10" rx="3.5" />
        <circle cx="12" cy="13.6" r=".9" fill="currentColor" stroke="none" />
      </>
    ),
  },
  // The SoundTouch 300 soundbar: long and low, one row of grille.
  st300: {
    svg: line('1.5'),
    body: (
      <>
        <rect x="1" y="9" width="22" height="6" rx="2" />
        <path
          d="M4 12h.01M6.2 12h.01M8.4 12h.01M10.6 12h.01M13.4 12h.01M15.6 12h.01M17.8 12h.01M20 12h.01"
          strokeWidth="1.2"
        />
      </>
    ),
  },
} satisfies Record<string, IconSpec>;
