// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { lazy, Suspense } from 'react';
import { AppShell } from '../../components/templates/AppShell';
import { HomeContent } from '../../components/organisms/HomeContent';
import type { PresetStripProps } from '../../components/organisms/HomeContent/PresetStrip';
import { SheetLoading } from '../../components/molecules/SheetLoading';
import { TopBar } from '../../components/organisms/TopBar';
import { useSelectedRoom } from '../../hooks/useSelectedRoom';
import { useFailedPlay } from '../../hooks/useFailedPlay';
import { useSpeakerLanguage } from '../../hooks/useSpeakerLanguage';
import { usePlayback } from '../../hooks/usePlayback';
import { useGrouping } from '../../hooks/useGrouping';
import { useRoomVolume } from '../../hooks/useRoomVolume';
import { useMute } from '../../hooks/useMute';
import { useSleepTimer } from '../../hooks/useSleepTimer';
import { useFavorites } from '../../hooks/useFavorites';
import { useRecent } from '../../hooks/useRecent';
import type { RecentItem } from '../../lib/recent';
import { useSpeakerInfo } from '../../hooks/useSpeakerInfo';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useErrorReporter } from '../../hooks/useErrorReporter';
import { useI18n } from '../../lib/i18n';
import { describePlayback } from '../../lib/playback';
import { speakerArg } from '../../hooks/useRooms';
import { HomeOverlays } from './HomeOverlays';
import { PlayerDock } from './PlayerDock';
import { PlayerLayers } from './PlayerLayers';
import { useLayers } from './useLayers';
import { useVolumeFailures } from './useVolumeFailures';
import './home.css';

// The search and the whole recent list load when first opened, not with the home page.
const SourcePage = lazy(() => import('./SourcePage').then((m) => ({ default: m.SourcePage })));

// The six presets on the start page: the selected room's, played on a tap.
const presetStrip = (
  { live, speaker }: ReturnType<typeof useSelectedRoom>,
  playback: ReturnType<typeof usePlayback>,
  onOpen: () => void
): PresetStripProps => ({
  presets: live.presets,
  player: live.player,
  speaker,
  loading: live.loading,
  onPlay: (slot) => void playback.playSlot(slot),
  onOpen,
});

// The home page: the start page under a top bar, with the selected room's player
// and speaker docked at the bottom; the presets, the menu, the
// speaker's card and now playing are layers on top. The behaviour lives in the hooks;
// this page only wires them together.
export function HomePage() {
  const { t } = useI18n();
  const selected = useSelectedRoom();
  const { lead, speaker, live } = selected;
  useSpeakerLanguage(selected.rooms);
  const errors = useErrorReporter();
  useFailedPlay(speaker, lead?.now, errors.notify);
  const info = useSpeakerInfo(speaker);
  const playback = usePlayback(selected, errors.failed, errors.notify, errors.block);
  const grouping = useGrouping(selected, errors.failed);
  const volume = useRoomVolume(selected, !!info.settings?.multiroom.syncVolume, errors.failed);
  const toggleMute = useMute(volume.setRoomVolume, volume.volumeStamp);
  const layers = useLayers();
  const { open, show } = layers;
  const leadName = lead?.name || '';
  const sleep = useSleepTimer(speaker, open.nowPlaying || open.speaker, () =>
    errors.failed(leadName)()
  );
  useVolumeFailures(live.volumeFailures, errors.failed, leadName);
  const { items: recent, remove: removeRecent } = useRecent(
    speaker,
    live.player.station?.name || '',
    errors.failed(leadName)
  );
  const favorites = useFavorites(speaker, errors.notify, errors.failed(leadName));
  useDocumentTitle(leadName);

  const starting = live.player.status === 'starting' || live.player.status === 'buffering';
  const view = lead ? describePlayback(lead.now, t, speakerArg(lead)) : null;
  const ctx = { layers, selected, playback };
  const ready = !!view;
  const onRemoveRecent = (item: RecentItem) => void removeRecent(item);

  return (
    <AppShell
      page={
        layers.allRecent || layers.search ? (
          <Suspense fallback={<SheetLoading />}>
            <SourcePage {...ctx} recent={recent} onRemoveRecent={onRemoveRecent} canHold={ready} />
          </Suspense>
        ) : null
      }
      header={<TopBar onMenu={() => show('drawer')} onPresets={() => show('presets')} />}
      footer={
        lead && <PlayerDock {...ctx} starting={starting} volume={volume} onMute={toggleMute} />
      }
      overlays={
        <HomeOverlays {...ctx} errors={errors} reloadInfo={info.reload}>
          {ready && (
            <PlayerLayers
              {...ctx}
              grouping={grouping}
              volume={volume}
              sleep={sleep}
              favorites={favorites}
              starting={starting}
              onMute={toggleMute}
            />
          )}
        </HomeOverlays>
      }
    >
      <HomeContent
        recent={recent}
        speaker={speaker}
        speakerName={leadName}
        onPlayRecent={(item) => void playback.playRecent(item)}
        onRemoveRecent={onRemoveRecent}
        spotifyReady={info.spotifyReady}
        onAllRecent={() => layers.setAllRecent(true)}
        onSearch={layers.openSearch}
        presets={lead ? presetStrip(selected, playback, () => show('presets')) : undefined}
      />
    </AppShell>
  );
}
