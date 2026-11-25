// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Sheet } from '../../components/molecules/Sheet';
import { MediaServers } from '../../components/organisms/settings/MediaServers';
import { SettingsSheet } from '../../components/organisms/settings/SettingsSheet';
import { useDiscardConfirm } from '../../components/molecules/ConfirmSheet';
import { useState } from 'react';
import { TopBar } from '../../components/organisms/TopBar';
import { useEscape } from '../../hooks/useEscape';
import { useI18n } from '../../lib/i18n';
import { RecentPage } from '../../components/organisms/HomeContent/RecentPage';
import { SearchSheet } from '../../components/organisms/SearchSheet';
import type { usePlayback } from '../../hooks/usePlayback';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import { spotifyEntry, stationEntry } from '../../lib/favorites';
import type { RecentItem } from '../../lib/recent';
import { holdRecent } from './holdRecent';
import type { Layers } from './useLayers';

interface SourcePageProps {
  layers: Layers;
  selected: SelectedRoom;
  playback: ReturnType<typeof usePlayback>;
  recent: RecentItem[];
  onRemoveRecent: (item: RecentItem) => void;
  // Whether a long press can hold a result for a preset: there is a room to store it on.
  canHold: boolean;
  // A speaker was renamed in Settings: the lists and the page's own name read again.
  onRenamed: () => void;
}

// The media servers as a main page, as Spotify and TuneIn are: the bars stay below it.
function MediaServersPage({ layers, speaker }: { layers: Layers; speaker: string }): ReactElement {
  const { t } = useI18n();
  const back = () => layers.setMediaServers(false);
  useEscape(true, back);
  return (
    <Sheet
      label={t('page_mediaservers')}
      bar={
        <TopBar
          onMenu={() => layers.show('drawer')}
          onBack={back}
          onPresets={() => layers.show('presets')}
          title={t('page_mediaservers')}
        />
      }
    >
      <MediaServers speaker={speaker} onExit={back} />
    </Sheet>
  );
}

// Settings as a main page: the bars stay below it. Leaving with unsaved edits asks first.
function SettingsPage({
  layers,
  selected,
  onRenamed,
}: {
  layers: Layers;
  selected: SelectedRoom;
  onRenamed: () => void;
}): ReactElement {
  const { t } = useI18n();
  const [dirty, setDirty] = useState(false);
  const discard = useDiscardConfirm(dirty);
  const exit = () => {
    discard.ask(() => {
      setDirty(false);
      layers.hide('settings');
    });
  };
  useEscape(true, exit);
  return (
    <>
      <SettingsSheet
        rooms={selected.rooms}
        speakerId={layers.settingsFor}
        onRenamed={onRenamed}
        onExit={exit}
        onDirty={setDirty}
        bar={
          <TopBar
            onMenu={() => layers.show('drawer')}
            onPresets={() => layers.show('presets')}
            title={t('settings')}
          />
        }
      />
      {discard.sheet}
    </>
  );
}

// The main page the sources open: the whole recently played list, the media servers, or a
// search / browse.
export function SourcePage({
  layers,
  selected,
  playback,
  recent,
  onRemoveRecent,
  canHold,
  onRenamed,
}: SourcePageProps): ReactElement | null {
  const { speaker, live } = selected;
  const { search, allRecent, show, setAllRecent, setSearch, holdForPreset } = layers;
  if (layers.open.settings)
    return <SettingsPage layers={layers} selected={selected} onRenamed={onRenamed} />;
  if (layers.mediaServers) return <MediaServersPage layers={layers} speaker={speaker} />;
  if (allRecent) {
    return (
      <RecentPage
        recent={recent}
        speaker={speaker}
        onPlayRecent={(item) => void playback.playRecent(item)}
        onRemoveRecent={onRemoveRecent}
        onHoldRecent={canHold ? (item) => holdRecent(layers, playback, item) : undefined}
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
