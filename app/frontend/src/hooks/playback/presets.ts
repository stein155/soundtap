// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { getPresets, storePreset, storeSpotifyPreset } from '../../lib/api';
import { playingEntry } from '../../lib/favorites';
import type { Preset } from '../../lib/types';
import type { PlaybackContext } from './context';

// How a preset goes back on its slot: a Spotify list as Spotify, a TuneIn station by its id.
// null for a kind the page cannot store.
function putBack(slot: number, prev: Preset, speaker: string): (() => Promise<unknown>) | null {
  if (prev.source === 'SPOTIFY') {
    return async () => storeSpotifyPreset(slot, { ...prev, uri: prev.location }, speaker);
  }
  const tuneInId = prev.tuneInId;
  if (!tuneInId) return null;
  return async () => storePreset(slot, { tuneInId, name: prev.name, logo: prev.logo }, speaker);
}

export interface PresetStores {
  storeSpotify: (
    slot: number,
    item: { uri: string; name: string; artwork?: string | undefined }
  ) => Promise<void>;
  canStore: boolean;
  storeCurrent: (slot: number) => Promise<void>;
}

// Storing on a preset: a Spotify item held onto a slot, or what is playing, with Undo.
export function presetStores({
  live,
  speaker,
  notify,
  t,
  leadFailed,
}: PlaybackContext): PresetStores {
  const storeSpotify = async (
    slot: number,
    item: { uri: string; name: string; artwork?: string | undefined }
  ) => {
    await storeSpotifyPreset(slot, { ...item, logo: item.artwork }, speaker).catch(leadFailed);
    await live.refreshPresets();
  };

  // Store what is playing on a preset: a TuneIn station, the one thing the page knows
  // how to put back on a key.
  const canStore = live.player.status === 'playing' && !!live.player.station?.tuneInId;
  // Puts a preset back as it was, for "Undo". Only kinds the page can store.
  // `now` is what the edit left in the slot (a station id, null for empty): if the slot
  // holds something else by the time Undo is tapped, someone changed it and it stays.
  const restore = (slot: number, prev: Preset | null, now: string | null) => {
    if (!prev) return undefined;
    const put = putBack(slot, prev, speaker);
    if (!put) return undefined;
    const undo = async () => {
      const cur = (await getPresets(speaker))?.[slot - 1] ?? null;
      if (now === null ? cur !== null : (cur?.tuneInId ?? cur?.location) !== now) {
        notify?.(t('presetChanged', { slot }));
      } else {
        await put().catch(leadFailed);
      }
      await live.refreshPresets();
    };
    return () => void undo();
  };

  const storeCurrent = async (slot: number) => {
    const entry = playingEntry(live.player.station);
    if (!entry) return;
    const prev = live.presets[slot - 1] ?? null;
    try {
      // A Spotify list goes in as Spotify: sent as a TuneIn id the speaker refused it.
      await (entry.source === 'SPOTIFY'
        ? storeSpotifyPreset(
            slot,
            { uri: entry.location, name: entry.name, logo: entry.art },
            speaker
          )
        : storePreset(slot, { tuneInId: entry.location, name: entry.name, logo: '' }, speaker));
      // A long-press replaces at once; say so (iOS has no vibration), with a way back.
      notify?.(t('presetStored', { slot }), restore(slot, prev, entry.location));
    } catch {
      leadFailed();
    }
    await live.refreshPresets();
  };

  return { storeSpotify, canStore, storeCurrent };
}
