// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { NowPlayingSheet } from '../../components/organisms/NowPlayingSheet';
import { PresetsSheet } from '../../components/organisms/PresetsSheet';
import { SpeakerCard } from '../../components/organisms/SpeakerCard';
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
  layers: Layers;
  selected: SelectedRoom;
  playback: ReturnType<typeof usePlayback>;
  grouping: ReturnType<typeof useGrouping>;
  volume: ReturnType<typeof useRoomVolume>;
  sleep: ReturnType<typeof useSleepTimer>;
  favorites: ReturnType<typeof useFavorites>;
  starting: boolean;
  onMute: (room: Room) => void;
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
}: PlayerLayersProps) {
  const { open, show, hide, saving, setSaving, showSettings, openSearch } = layers;
  const { lead, members, speaker, live } = selected;
  if (!lead) return null;
  const group = [lead, ...members];

  // The transport of a room that plays nothing starts its first preset; with no preset
  // to start it offers a station instead of doing nothing.
  const start = () =>
    void playback.roomTransport(lead).then((result) => {
      if (result === 'empty') openSearch({});
    });

  return (
    <>
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
        onPlay={(slot) => void playback.playSlot(slot)}
        saving={saving?.entry.name ?? null}
        onSave={(slot) => saving?.store(slot)}
        onStore={(slot) => void playback.storeCurrent(slot)}
        favorites={{
          items: favorites.items,
          onAdd: () => {
            const entry = saving?.entry ?? playingEntry(live.player.station);
            if (entry) void favorites.add(entry);
          },
          onPlay: (item) => void playback.playRecent(item),
          onRemove: (item) => void favorites.remove(item),
        }}
      />
      <SpeakerCard
        open={open.speaker}
        onClose={() => hide('speaker')}
        selected={selected}
        playback={playback}
        grouping={grouping}
        volume={volume}
        sleep={sleep}
        onMute={onMute}
        onSettings={(room) => showSettings(room.id)}
      />
      <NowPlayingSheet
        open={open.nowPlaying}
        onClose={() => hide('nowPlaying')}
        lead={lead}
        members={members}
        starting={starting}
        sleepTimer={sleep.timer}
        onTransport={() => void playback.transport()}
        onSkip={(dir) => void playback.skip(dir)}
        onStart={start}
        onSleep={(minutes) => void sleep.start(minutes)}
        onVolume={group.length > 1 ? volume.setGroupVolume : (v) => volume.setRoomVolume(lead, v)}
        onMute={group.length > 1 ? undefined : () => onMute(lead)}
        onPresets={() => show('presets')}
        onSpeaker={() => show('speaker')}
      />
    </>
  );
}
