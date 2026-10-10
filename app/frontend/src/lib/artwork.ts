// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiURL } from './base';

// Station artwork as the UI shows it: a colour, a lettermark, and the logo image.

// The design gives every station its own muted colour. Real TuneIn ids are unbounded,
// so the colour comes from the design's palette by a stable hash: the same station gets
// the same colour on every speaker and every reload.
const PALETTE = [
  '#7a9fd0',
  '#e0a85a',
  '#d97a8e',
  '#a89cd0',
  '#c0a07c',
  '#d97878',
  '#dba070',
  '#86b0d6',
  '#cd7878',
  '#6b6f8a',
  '#9385c4',
  '#7ac0a8',
  '#d77aa0',
  '#d9926e',
  '#d99e80',
  '#7ab59a',
  '#d6b070',
  '#c87aa6',
  '#c87878',
  '#88a8d4',
  '#d088a8',
  '#b0a8d6',
  '#d68876',
  '#7280b0',
];
const FALLBACK_COLOR = '#d9926e';

export function stationColor(key?: string | null): string {
  if (!key) return FALLBACK_COLOR;
  let hash = 0;
  for (let i = 0; i < key.length; i++) hash = (hash * 31 + key.charCodeAt(i)) | 0;
  return PALETTE[Math.abs(hash) % PALETTE.length];
}

// Two letters for a lettermark, from the first letters of its words: "NPO Radio 2" -> "NR",
// "Studio Brussel" -> "SB", "kink" -> "KI".
export function initials(name: string): string {
  const [first = name, second] = name.match(/[A-Za-z]\w*/g) ?? [];
  if (second) return (first[0] + second[0]).toUpperCase();
  return first.slice(0, 2).toUpperCase() || '?';
}

// The image URL for a station's logo. A CDN logo loads straight from the CDN: an <img>
// needs no CORS, the browser caches it, and it does not take one of the browser's six
// connections to the speaker. Through /api/logo every page load refetched every logo via
// the speaker, and a preset tap queued behind them until it timed out ("not responding").
// Our own artwork (/api/...) lives on the speaker that played it. Without an explicit
// logo, TuneIn's CDN has one per guide id.
export function logoURL(
  station: { logo?: string; tuneInId?: string | null },
  speaker?: string
): string | null {
  // Only something a browser can fetch: a file:// display icon would only 400.
  const logo = /^(https?:\/\/|\/api\/)/.test(station.logo || '') ? station.logo : '';
  const absolute =
    logo ||
    (station.tuneInId && !station.tuneInId.includes('://')
      ? `http://cdn-radiotime-logos.tunein.com/${station.tuneInId}g.png`
      : '');
  if (!absolute) return null;
  return absolute.startsWith('/api/') ? apiURL(absolute, speaker) : absolute;
}
