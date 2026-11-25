// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Station name matching. The speaker does not expose the TuneIn id of what's
// live (getNowPlaying returns tuneInId: null), so the UI matches what's playing
// against presets/pending targets by name. Names can differ slightly between
// TuneIn search results and the speaker's stationName (e.g. a "(London)" suffix),
// so the match is loose: equal after normalising, or one clearly contains the other.

import type { Preset, PlayerStation } from './types';

const normName = (s?: string | null): string => (s || '').trim().toLowerCase();

export function sameStation(a?: string | null, b?: string | null): boolean {
  const x = normName(a);
  const y = normName(b);
  if (!x || !y) return false;
  if (x === y) return true;
  if (x.length <= 2 || y.length <= 2) return false;
  return containsWord(x, y) || containsWord(y, x);
}

// Containment counts only on word boundaries, so "radio 1" matches
// "npo radio 1" but never "radio 10".
const alnum = /[a-z0-9]/;
function containsWord(long: string, short: string): boolean {
  for (let i = long.indexOf(short); i !== -1; i = long.indexOf(short, i + 1)) {
    const okBefore = i === 0 || !alnum.test(long.charAt(i - 1));
    const okAfter = i + short.length === long.length || !alnum.test(long.charAt(i + short.length));
    if (okBefore && okAfter) return true;
  }
  return false;
}

// activePresetIndex returns the index of the single preset that is playing, or
// -1. sameStation is deliberately loose (word-boundary containment), so naming
// it per-tile lit up several tiles at once — e.g. both "Radio 1" and "NPO Radio
// 1" match while either plays. Resolving to ONE index here fixes that: prefer an
// exact TuneIn-id match, then an exact name, then a loose match only when it is
// unambiguous (a single preset matches). Ambiguous -> none, never several.
export function activePresetIndex(
  presets: (Preset | null)[],
  station: PlayerStation | null
): number {
  if (!station || !Array.isArray(presets)) return -1;
  if (station.tuneInId) {
    // station.tuneInId is set here, so equal ids are set ids.
    const byId = presets.findIndex((p) => p?.tuneInId === station.tuneInId);
    if (byId >= 0) return byId;
  }
  // Names only stand in for identity that is missing: a preset whose known id differs
  // from the playing station's is a different station, whatever it is called.
  const named = (p: Preset | null): p is Preset =>
    !!p && !(station.tuneInId && p.tuneInId && p.tuneInId !== station.tuneInId);
  const target = normName(station.name);
  const byExact = presets.findIndex(
    (p) => named(p) && normName(p.name) === target && target !== ''
  );
  if (byExact >= 0) return byExact;
  let loose = -1;
  for (const [i, p] of presets.entries()) {
    if (named(p) && sameStation(p.name, station.name)) {
      if (loose >= 0) return -1; // more than one loose match — don't guess
      loose = i;
    }
  }
  return loose;
}

// Whether the user typed an http(s) URL with a host: what the speaker can stream.
export function isStreamUrl(url: string): boolean {
  try {
    const { protocol, hostname } = new URL(url);
    return (protocol === 'http:' || protocol === 'https:') && !!hostname;
  } catch {
    return false;
  }
}
