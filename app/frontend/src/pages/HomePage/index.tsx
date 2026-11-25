// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { lazy, Suspense, type ComponentProps, type ReactElement } from 'react';
import { AppShell } from '../../components/templates/AppShell';
import { HomeContent } from '../../components/organisms/HomeContent';
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
import { holdRecent } from './holdRecent';
import { useLayers, type Layers } from './useLayers';
import { NotifyContext } from '../../hooks/useNotify';
import { useVolumeFailures } from './useVolumeFailures';
import './home.css';

// The search and the whole recent list load when first opened, not with the home page.
const SourcePage = lazy(async () =>
  import('./SourcePage').then((m) => ({ default: m.SourcePage }))
);

// The home page: the start page under a top bar, with the selected room's player
// and speaker docked at the bottom; the presets, the menu, the
// speaker's card and now playing are layers on top. The behaviour lives in the hooks;
// this page only wires them together.
// A source page is open in place of the start page: recently played, a search or browse, or
// the media servers.
const mainPage = (layers: Layers): boolean =>
  layers.allRecent || layers.search !== null || layers.mediaServers || layers.open.settings;

// The source page in place of the start page, loaded when first opened. A rename in
// Settings reads the speakers and this page's own name again.
function MainPage({
  reloadInfo,
  ...props
}: Omit<ComponentProps<typeof SourcePage>, 'onRenamed'> & {
  reloadInfo: () => void;
}): ReactElement {
  return (
    <Suspense fallback={<SheetLoading />}>
      <SourcePage
        {...props}
        onRenamed={() => {
          void props.selected.roomsState.refresh();
          reloadInfo();
        }}
      />
    </Suspense>
  );
}

export function HomePage(): ReactElement {
  const { t } = useI18n();
  const selected = useSelectedRoom();
  const { lead, speaker, live } = selected;
  useSpeakerLanguage(selected.rooms);
  const errors = useErrorReporter();
  useFailedPlay(speaker, lead?.now, errors.notify);
  const info = useSpeakerInfo(speaker);
  const playback = usePlayback(selected, errors.failed, errors.notify, errors.block);
  const grouping = useGrouping(selected, errors.failed);
  const volume = useRoomVolume(
    selected,
    info.settings?.multiroom.syncVolume === true,
    errors.failed
  );
  const toggleMute = useMute(volume.setRoomVolume, volume.volumeStamp);
  const layers = useLayers();
  const { open, show } = layers;
  const leadName = lead?.name || '';
  const sleep = useSleepTimer(speaker, open.nowPlaying || open.speakers, () =>
    errors.failed(leadName)()
  );
  useVolumeFailures(live.volumeFailures, errors.failed, leadName);
  const { items: recent, remove: removeRecent } = useRecent(
    speaker,
    live.player.station?.name || '',
    errors.failed(leadName)
  );
  const favorites = useFavorites(speaker, errors.failed(leadName));
  useDocumentTitle(leadName);

  const starting = live.player.status === 'starting' || live.player.status === 'buffering';
  const view = lead ? describePlayback(lead.now, t, speakerArg(lead)) : null;
  const ctx = { layers, selected, playback };
  const ready = !!view;
  const onRemoveRecent = (item: RecentItem) => void removeRecent(item);
  const main = { recent, onRemoveRecent, canHold: ready, reloadInfo: info.reload };

  return (
    <AppShell
      page={
        mainPage(layers) ? (
          <NotifyContext.Provider value={errors.notify}>
            <MainPage {...ctx} {...main} />
          </NotifyContext.Provider>
        ) : null
      }
      header={<TopBar onMenu={() => show('drawer')} onPresets={() => show('presets')} />}
      footer={
        lead && <PlayerDock {...ctx} starting={starting} volume={volume} onMute={toggleMute} />
      }
      overlays={
        <HomeOverlays {...ctx} errors={errors}>
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
        onHoldRecent={ready ? (item) => holdRecent(layers, playback, item) : undefined}
        spotifyReady={info.spotifyReady}
        onAllRecent={() => layers.setAllRecent(true)}
        onSearch={layers.openSearch}
      />
    </AppShell>
  );
}
