// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { FavoriteEntry, PlayerStation, Station } from './types';

// What each way of saving a favorite hands the speaker.
export const stationEntry = (
  station: Pick<Station, 'tuneInId' | 'name' | 'logo'>
): FavoriteEntry => ({
  source: 'TUNEIN',
  name: station.name,
  location: station.tuneInId,
  art: station.logo,
});

export const spotifyEntry = (item: {
  uri: string;
  name: string;
  artwork?: string | undefined;
}): FavoriteEntry => ({
  source: 'SPOTIFY',
  name: item.name,
  location: item.uri,
  art: item.artwork ?? '',
});

// What plays now, to save. A Spotify list is stored as Spotify, not as a TuneIn id; only
// a single track keeps the cover, since a station or list shows the song's cover, not its own.
export const playingEntry = (station: PlayerStation | null | undefined): FavoriteEntry | null => {
  if (!station?.tuneInId) return null;
  if (station.source === 'SPOTIFY') {
    const track = station.tuneInId.startsWith('spotify:track:');
    return spotifyEntry({
      uri: station.tuneInId,
      name: station.name,
      artwork: track ? station.art : '',
    });
  }
  return stationEntry({ ...station, tuneInId: station.tuneInId, logo: '' });
};
