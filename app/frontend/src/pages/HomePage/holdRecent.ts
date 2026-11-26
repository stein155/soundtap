// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { usePlayback } from '../../hooks/usePlayback';
import { spotifyEntry, stationEntry } from '../../lib/favorites';
import type { RecentItem } from '../../lib/recent';
import type { Layers } from './useLayers';

// "Put on a preset" for a recently played entry: held as a long press in a browse list holds
// a result, so the presets open and a tap on a slot stores it there.
export function holdRecent(
  layers: Pick<Layers, 'holdForPreset'>,
  playback: Pick<ReturnType<typeof usePlayback>, 'pick' | 'storeSpotify'>,
  item: RecentItem
): void {
  if (item.tuneInId) {
    const station = {
      tuneInId: item.tuneInId,
      name: item.name,
      logo: item.art,
      tagline: '',
      genre: '',
      country: '',
    };
    layers.holdForPreset(stationEntry(station), (slot) => {
      void playback.pick(station, slot);
    });
    return;
  }
  if (item.spotifyUri) {
    const spotify = { uri: item.spotifyUri, name: item.name, artwork: item.art };
    layers.holdForPreset(spotifyEntry(spotify), (slot) => {
      void playback.storeSpotify(slot, spotify);
    });
  }
}
