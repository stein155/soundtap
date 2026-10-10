// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { RecentEntry } from './types';

// A recently-played entry that can be played again: a station (by its guide id, or its URL
// when it is a stream of the user's own) or a Spotify list or track started here (by its URI). Nothing else is offered: AirPlay,
// Bluetooth and Spotify from a phone leave no location, and a UPnP controller such as
// Music Assistant streams from a URL that is gone once it stops.
export interface RecentItem {
  key: string;
  name: string;
  art: string;
  tuneInId: string | null;
  spotifyUri: string | null;
  // The speaker's own entries this item stands for, to take them off its list.
  source: string;
  stoppedAt: string;
  locations: string[];
}

function stationOf(location: string): string | null {
  // A favorite holds the bare guide id, whatever its kind; history holds a playback path.
  if (/^https?:\/\//.test(location) || /^[A-Za-z0-9]+$/.test(location)) return location;
  return location.match(/(?:^|[/=])(s\d+)/)?.[1] ?? null;
}

export function recentItems(list: RecentEntry[]): RecentItem[] {
  const seen = new Map<string, RecentItem>();
  const out: RecentItem[] = [];
  for (const entry of list) {
    const name = entry.name.trim();
    if (!name || !entry.location) continue;
    const tuneInId = entry.source === 'TUNEIN' ? stationOf(entry.location) : null;
    const spotifyUri =
      entry.source === 'SPOTIFY' && entry.location.startsWith('spotify:') ? entry.location : null;
    // What plays, not what it is called: two stations can share a name.
    const key = tuneInId ? `tunein:${tuneInId}` : `spotify:${spotifyUri}`;
    if (!tuneInId && !spotifyUri) continue;
    const known = seen.get(key);
    if (known) {
      known.locations.push(entry.location);
      continue;
    }
    const item = {
      key,
      name,
      art: entry.art,
      tuneInId,
      spotifyUri,
      source: entry.source,
      stoppedAt: entry.stoppedAt,
      locations: [entry.location],
    };
    seen.set(key, item);
    out.push(item);
  }
  return out;
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
  ['second', 1],
];

// "2 hours ago" in the page's language, in the largest unit that fits.
export function timeAgo(stoppedAt: string, lang: string, now = Date.now()): string {
  const seconds = Math.max(0, Math.round((now - Date.parse(stoppedAt)) / 1000));
  if (Number.isNaN(seconds)) return '';
  const [unit, size] = UNITS.find(([, s]) => seconds >= s) ?? UNITS[UNITS.length - 1];
  return new Intl.RelativeTimeFormat(lang).format(-Math.floor(seconds / size), unit);
}
