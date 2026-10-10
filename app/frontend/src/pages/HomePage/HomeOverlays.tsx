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
import type { useErrorReporter } from '../../hooks/useErrorReporter';
import type { usePlayback } from '../../hooks/usePlayback';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import { useI18n } from '../../lib/i18n';
import type { Layers } from './useLayers';

// Settings is the largest part of the UI and the least visited: it loads when first opened.
const SettingsSheet = lazy(() =>
  import('../../components/organisms/settings/SettingsSheet').then((m) => ({
    default: m.SettingsSheet,
  }))
);

interface HomeOverlaysProps {
  layers: Layers;
  selected: SelectedRoom;
  errors: ReturnType<typeof useErrorReporter>;
  playback: ReturnType<typeof usePlayback>;
  // Re-reads what the speaker's settings changed (a rename).
  reloadInfo: () => void;
  // The layers of the selected room's player.
  children: ReactNode;
}

// Everything above the page: the connection banner and toast, the menu, the settings and
// the player's layers.
export function HomeOverlays({
  layers,
  selected,
  errors,
  playback,
  reloadInfo,
  children,
}: HomeOverlaysProps) {
  const { t } = useI18n();
  const { open, hide, search, settingsFor, setSearch, setAllRecent, showSettings, openSearch } =
    layers;
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
      <TopLayer>
        {live.unreachable && (
          <ConnectionBanner message={t('connectionLost', { name: lead?.name || 'SoundTap' })} />
        )}
        {errors.message && (
          <Toast
            message={errors.message}
            onDismiss={errors.dismiss}
            onUndo={errors.undo ?? undefined}
          />
        )}
      </TopLayer>
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
              onStream={(station) => void playback.pick(station, null)}
            />
          </Suspense>
        )}
      </SheetModal>
      {children}
    </>
  );
}
