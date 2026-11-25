// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { clean } from '../text';
import { PRESET_COUNT } from '../limits';
import { getJSON, send, text } from './http';
import { likedName } from './playback';
import type { ApiPreset, Preset, FavoriteEntry, RecentEntry } from '../types';

// Presets: soundtapd returns the stored presets
// [{slot,name,stationId,location,logo}]. Expanded to a fixed 6-slot array with
// the tuneInId the UI keys off. null when the read failed: that is not "no presets", and the
// caller keeps what it showed.
export async function getPresets(speaker?: string): Promise<(Preset | null)[] | null> {
  const slots: (Preset | null)[] = Array<Preset | null>(PRESET_COUNT).fill(null);
  try {
    const list = await getJSON<ApiPreset[] | null>('/api/presets', speaker);
    for (const p of list ?? []) {
      const slot = p.slot;
      if (slot >= 1 && slot <= PRESET_COUNT) {
        const source = text(p.source) || 'TUNEIN';
        const tuneInId =
          source === 'TUNEIN'
            ? p.stationId || /station\/(s\d+)/.exec(text(p.location))?.[1] || null
            : null;
        slots[slot - 1] = {
          slot,
          source,
          name: likedName(p.location, clean(p.name)),
          tuneInId,
          location: p.location || '',
          logo: p.logo || '',
        };
      }
    }
  } catch {
    return null;
  }
  return slots;
}

// Recently played, newest first. location is empty for what cannot be replayed.
// null when the read failed: an unknown history is not an empty one.
export async function getRecent(speaker?: string): Promise<RecentEntry[] | null> {
  try {
    const list = (await getJSON<RecentEntry[] | null>('/api/recent', speaker)) ?? [];
    return list.map((e) => ({ ...e, name: likedName(e.location, e.name) }));
  } catch {
    return null;
  }
}

// The speaker's favorites, in the order they were saved; null when the read failed.
export async function getFavorites(speaker?: string): Promise<RecentEntry[] | null> {
  try {
    const list = (await getJSON<FavoriteEntry[] | null>('/api/favorites', speaker)) ?? [];
    return list.map((e) => ({ ...e, stoppedAt: '' }));
  } catch {
    return null;
  }
}

export async function addFavorite(entry: FavoriteEntry, speaker?: string): Promise<unknown> {
  return send('/api/favorites', 'POST', entry, speaker);
}

export async function removeFavorite(
  source: string,
  location: string,
  speaker?: string
): Promise<unknown> {
  return send('/api/favorites', 'DELETE', { source, location }, speaker);
}

// Takes one entry off the speaker's recently played list.
export async function removeRecent(
  source: string,
  location: string,
  speaker?: string
): Promise<unknown> {
  return send('/api/recent', 'DELETE', { source, location }, speaker);
}

// The inputs a speaker has (BLUETOOTH, AUX); null when it cannot say, which is not the
// same as having none: the caller asks again.
export async function getUserSources(speaker?: string): Promise<string[] | null> {
  try {
    return await getJSON<string[]>('/api/user-sources', speaker);
  } catch {
    return null;
  }
}

export async function selectUserSource(source: string, speaker?: string): Promise<unknown> {
  return send('/api/user-source', 'POST', { source }, speaker);
}

// Save a station to a preset slot.
// A Spotify preset holds only the URI (track, album, artist, playlist or Liked Songs);
// the speaker's own Spotify session plays it when the preset key is pressed.
export async function storeSpotifyPreset(
  slot: number,
  { uri, name, logo }: { uri: string; name: string; logo?: string | undefined },
  speaker?: string
): Promise<unknown> {
  return send(
    `/api/presets/${slot}`,
    'PUT',
    { source: 'SPOTIFY', location: uri, name: clean(name), logo: logo || '' },
    speaker
  );
}
export async function storePreset(
  slot: number,
  { tuneInId, name, logo }: { tuneInId: string; name: string; logo?: string },
  speaker?: string
): Promise<unknown> {
  return send(
    `/api/presets/${slot}`,
    'PUT',
    { stationId: tuneInId, name: clean(name), logo: logo || '' },
    speaker
  );
}
