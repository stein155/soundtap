// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { lazy, Suspense, useCallback, useRef, type ReactNode } from 'react';
import { ConnectionBanner } from '../../components/molecules/ConnectionBanner';
import { SheetModal } from '../../components/molecules/SheetModal';
import { ErrorPage } from '../../components/molecules/ErrorPage';
import { Toast } from '../../components/molecules/Toast';
import { TopLayer } from '../../components/molecules/TopLayer';
import { DrawerMenu } from '../../components/organisms/DrawerMenu';
import { SheetLoading } from '../../components/molecules/SheetLoading';
import { Sheet } from '../../components/molecules/Sheet';
import type { useErrorReporter } from '../../hooks/useErrorReporter';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import { useI18n } from '../../lib/i18n';
import type { Layers } from './useLayers';

// Settings is the largest part of the UI and the least visited: it loads when first opened,
// as do the media servers.
const SettingsSheet = lazy(() =>
  import('../../components/organisms/settings/SettingsSheet').then((m) => ({
    default: m.SettingsSheet,
  }))
);
const MediaServers = lazy(() =>
  import('../../components/organisms/settings/MediaServers').then((m) => ({
    default: m.MediaServers,
  }))
);

interface HomeOverlaysProps {
  layers: Layers;
  selected: SelectedRoom;
  errors: ReturnType<typeof useErrorReporter>;
  // Re-reads what the speaker's settings changed (a rename).
  reloadInfo: () => void;
  // The layers of the selected room's player.
  children: ReactNode;
}

// The banner when the speaker is out of reach (lost: its name), and the toast.
function Notices({
  lost,
  errors,
}: {
  lost: string | null;
  errors: ReturnType<typeof useErrorReporter>;
}) {
  const { t } = useI18n();
  return (
    <TopLayer>
      {lost !== null && <ConnectionBanner message={t('connectionLost', { name: lost })} />}
      {errors.message && (
        <Toast
          message={errors.message}
          onDismiss={errors.dismiss}
          onUndo={errors.undo ?? undefined}
        />
      )}
    </TopLayer>
  );
}

// The media servers on the network, a source of their own from the menu.
function MediaServersPage({
  open,
  onClose,
  speaker,
}: {
  open: boolean;
  onClose: () => void;
  speaker: string;
}) {
  const { t } = useI18n();
  return (
    <SheetModal open={open} onClose={onClose} label={t('page_mediaservers')} variant="page">
      {(close) => (
        <Suspense fallback={<SheetLoading />}>
          <Sheet onExit={close} headline={t('page_mediaservers')} label={t('page_mediaservers')}>
            <MediaServers speaker={speaker} />
          </Sheet>
        </Suspense>
      )}
    </SheetModal>
  );
}

// Everything above the page: the connection banner and toast, the menu, the settings and
// the player's layers.
export function HomeOverlays({
  layers,
  selected,
  errors,
  reloadInfo,
  children,
}: HomeOverlaysProps) {
  const { t } = useI18n();
  const {
    open,
    show,
    hide,
    search,
    settingsFor,
    setSearch,
    setAllRecent,
    showSettings,
    openSearch,
  } = layers;
  const { rooms, lead, live, roomsState } = selected;
  // Every way out of Settings (its X, Escape, the phone's back, a tap outside) asks before
  // dropping unsaved edits, as its own back button does.
  const settingsDirty = useRef(false);
  const onSettingsDirty = useCallback((d: boolean) => {
    settingsDirty.current = d;
  }, []);
  const closeSettings = () => {
    if (settingsDirty.current && !window.confirm(t('discardChanges'))) return;
    settingsDirty.current = false;
    hide('settings');
  };
  return (
    <>
      <Notices lost={live.unreachable ? lead?.name || 'SoundTap' : null} errors={errors} />
      <ErrorPage
        open={errors.blocked.open}
        message={errors.blocked.text}
        onClose={errors.dismissBlocked}
      />
      <DrawerMenu
        open={open.drawer}
        onClose={() => hide('drawer')}
        at={search ? search.service || 'tunein' : undefined}
        onRecent={() => {
          setSearch(null);
          setAllRecent(false);
        }}
        onSpotify={() => openSearch({ service: 'spotify' })}
        onTuneIn={() => openSearch({})}
        onMediaServers={() => show('mediaServers')}
        onSettings={() => showSettings(null)}
      />
      <SheetModal open={open.settings} onClose={closeSettings} label={t('settings')} variant="page">
        {(close) => (
          <Suspense fallback={<SheetLoading />}>
            <SettingsSheet
              rooms={rooms}
              speakerId={settingsFor}
              onRenamed={() => {
                void roomsState.refresh();
                reloadInfo();
              }}
              onExit={close}
              onDirty={onSettingsDirty}
            />
          </Suspense>
        )}
      </SheetModal>
      <MediaServersPage
        open={open.mediaServers}
        onClose={() => hide('mediaServers')}
        speaker={selected.speaker}
      />
      {children}
    </>
  );
}
