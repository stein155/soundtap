// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { NowPlayingSheet } from '../../components/organisms/NowPlayingSheet';
import { PresetsSheet } from '../../components/organisms/PresetsSheet';
import { SpeakersSheet } from '../../components/organisms/SpeakersSheet';
import { GroupSheet } from '../../components/organisms/GroupSheet';
import type { RecentItem } from '../../lib/recent';
import type { useFavorites } from '../../hooks/useFavorites';
import type { useGrouping } from '../../hooks/useGrouping';
import type { usePlayback } from '../../hooks/usePlayback';
import type { useRoomVolume } from '../../hooks/useRoomVolume';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import type { useSleepTimer } from '../../hooks/useSleepTimer';
import type { Room } from '../../hooks/useRooms';
import { playingEntry } from '../../lib/favorites';
import type { Layers } from './useLayers';

interface PlayerLayersProps {
  readonly layers: Layers;
  readonly selected: SelectedRoom;
  readonly playback: ReturnType<typeof usePlayback>;
  readonly grouping: ReturnType<typeof useGrouping>;
  readonly volume: ReturnType<typeof useRoomVolume>;
  readonly sleep: ReturnType<typeof useSleepTimer>;
  readonly favorites: ReturnType<typeof useFavorites>;
  readonly starting: boolean;
  readonly onMute: (room: Room) => void;
}

// The favorites under the presets: add what is held or plays, play one, remove one.
const favoriteActions = (
  favorites: PlayerLayersProps['favorites'],
  playback: PlayerLayersProps['playback'],
  entry: () => ReturnType<typeof playingEntry> | undefined
) => ({
  items: favorites.items,
  onAdd: () => {
    const e = entry();
    if (e) void favorites.add(e);
  },
  onPlay: (item: RecentItem) => {
    void playback.playRecent(item);
  },
  onRemove: (item: RecentItem) => {
    void favorites.remove(item);
  },
});

// The presets sheet, with a station held to be stored and the favorites under it.
function PlayerPresets({
  layers,
  selected,
  playback,
  favorites,
}: Pick<PlayerLayersProps, 'layers' | 'selected' | 'playback' | 'favorites'>): ReactElement {
  const { open, hide, saving, setSaving } = layers;
  const { speaker, live } = selected;
  return (
    <PresetsSheet
      open={open.presets}
      onClose={() => {
        hide('presets');
        setSaving(null);
      }}
      presets={live.presets}
      player={live.player}
      loading={live.loading}
      speaker={speaker}
      canStore={playback.canStore}
      onPlay={(slot) => {
        void playback.playSlot(slot);
      }}
      saving={saving?.entry.name ?? null}
      onSave={(slot) => saving?.store(slot)}
      onStore={(slot) => {
        void playback.storeCurrent(slot);
      }}
      favorites={favoriteActions(
        favorites,
        playback,
        () => saving?.entry ?? playingEntry(live.player.station)
      )}
    />
  );
}

// The layers of the selected room's player: its presets, its card and now playing.
export function PlayerLayers({
  layers,
  selected,
  playback,
  grouping,
  volume,
  sleep,
  favorites,
  starting,
  onMute,
}: PlayerLayersProps): ReactElement | null {
  const { open, show, hide, showSettings, openSearch } = layers;
  const { lead, members } = selected;
  if (!lead) return null;
  const group = [lead, ...members];

  // The transport of a room that plays nothing starts its first preset; with no preset
  // to start it offers a station instead of doing nothing.
  const start = () => {
    void playback.roomTransport(lead).then((result) => {
      if (result === 'empty') openSearch({});
    });
  };

  return (
    <>
      <PlayerPresets
        layers={layers}
        selected={selected}
        playback={playback}
        favorites={favorites}
      />
      <SpeakersSheet
        open={open.speakers}
        onClose={() => hide('speakers')}
        selected={selected}
        playback={playback}
        onSettings={(room) => showSettings(room.id)}
      />
      <GroupSheet
        open={open.group}
        onClose={() => hide('group')}
        selected={selected}
        grouping={grouping}
        volume={volume}
      />
      <NowPlayingSheet
        open={open.nowPlaying}
        onClose={() => hide('nowPlaying')}
        lead={lead}
        members={members}
        starting={starting}
        sleepTimer={sleep.timer}
        onTransport={() => {
          void playback.transport();
        }}
        onSkip={(dir) => {
          void playback.skip(dir);
        }}
        onStart={start}
        onSleep={(minutes) => {
          void sleep.start(minutes);
        }}
        onVolume={group.length > 1 ? volume.setGroupVolume : (v) => volume.setRoomVolume(lead, v)}
        onMute={group.length > 1 ? undefined : () => onMute(lead)}
        onPresets={() => show('presets')}
        onSpeaker={() => show('speakers')}
        onGroup={() => show('group')}
      />
    </>
  );
}
