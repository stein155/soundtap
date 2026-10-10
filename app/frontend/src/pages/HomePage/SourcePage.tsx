// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { RecentPage } from '../../components/organisms/HomeContent/RecentPage';
import { SearchSheet } from '../../components/organisms/SearchSheet';
import type { usePlayback } from '../../hooks/usePlayback';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import { spotifyEntry, stationEntry } from '../../lib/favorites';
import type { RecentItem } from '../../lib/recent';
import type { Layers } from './useLayers';

interface SourcePageProps {
  layers: Layers;
  selected: SelectedRoom;
  playback: ReturnType<typeof usePlayback>;
  recent: RecentItem[];
  onRemoveRecent: (item: RecentItem) => void;
  // Whether a long press can hold a result for a preset: there is a room to store it on.
  canHold: boolean;
}

// The main page the sources open: the whole recently played list, or a search / browse.
export function SourcePage({
  layers,
  selected,
  playback,
  recent,
  onRemoveRecent,
  canHold,
}: SourcePageProps) {
  const { speaker, live } = selected;
  const { search, allRecent, show, setAllRecent, setSearch, holdForPreset } = layers;
  if (allRecent) {
    return (
      <RecentPage
        recent={recent}
        speaker={speaker}
        onPlayRecent={(item) => void playback.playRecent(item)}
        onRemoveRecent={onRemoveRecent}
        onBack={() => setAllRecent(false)}
        onPresets={() => show('presets')}
      />
    );
  }
  if (!search) return null;
  return (
    // Keyed by speaker: Spotify's library and answers still on their way belong to one room.
    <SearchSheet
      key={speaker}
      mode={search}
      onMenu={() => show('drawer')}
      onPresets={() => show('presets')}
      speaker={speaker}
      onClose={() => setSearch(null)}
      onPick={(station) => {
        setSearch(null);
        void playback.pick(station, null);
      }}
      onHoldStation={
        canHold
          ? (station) =>
              holdForPreset(stationEntry(station), (slot) => void playback.pick(station, slot))
          : undefined
      }
      onHoldSpotify={
        canHold
          ? (item) =>
              holdForPreset(spotifyEntry(item), (slot) => void playback.storeSpotify(slot, item))
          : undefined
      }
      onSpotifyPlayed={() => {
        live.nudge();
        void live.refreshPresets();
      }}
    />
  );
}
