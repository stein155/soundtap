// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactNode, type ReactElement } from 'react';
import { ConnectionBanner } from '../../components/molecules/ConnectionBanner';
import { ErrorPage } from '../../components/molecules/ErrorPage';
import { OfflinePage } from '../../components/organisms/OfflinePage';
import { useLongLost } from './useLongLost';
import { Toast } from '../../components/molecules/Toast';
import { TopLayer } from '../../components/molecules/TopLayer';
import { DrawerMenu, type DrawerPage } from '../../components/organisms/DrawerMenu';
import type { useErrorReporter } from '../../hooks/useErrorReporter';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import { useI18n } from '../../lib/i18n';
import type { Layers } from './useLayers';

interface HomeOverlaysProps {
  layers: Layers;
  selected: SelectedRoom;
  errors: ReturnType<typeof useErrorReporter>;
  // The layers of the selected room's player.
  children: ReactNode;
}

// The banner when the speaker is out of reach, the full page once it stays away, and the
// toast. The speakers sheet opened from that page covers it: the page waits behind.
function Notices({
  lead,
  lost,
  layers,
  errors,
}: {
  lead: SelectedRoom['lead'];
  lost: boolean;
  layers: Layers;
  errors: ReturnType<typeof useErrorReporter>;
}) {
  const { t } = useI18n();
  const longLost = useLongLost(lost);
  // Closed by hand: the banner stands in until the speaker answers, and the next loss shows
  // the page again.
  const [dismissed, setDismissed] = useState(false);
  if (!lost && dismissed) setDismissed(false);
  const fullPage = longLost && !dismissed;
  const name = lead?.name || 'SoundTap';
  return (
    <TopLayer>
      <OfflinePage
        open={longLost && !dismissed && !layers.open.speakers}
        onClose={() => setDismissed(true)}
        name={name}
        model={lead?.model}
        onOther={() => layers.show('speakers')}
      />
      {lost && !fullPage && <ConnectionBanner message={t('connectionLost', { name })} />}
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

// The main page on screen, in the order SourcePage picks it: the menu marks its row.
function drawerAt({ open, mediaServers, search }: Layers): DrawerPage | undefined {
  if (open.settings) return 'settings';
  if (mediaServers) return 'mediaservers';
  if (search) return search.service || 'tunein';
  return undefined;
}

// Everything above the page: the connection banner and toast, the menu and the player's
// layers. Settings and the sources are main pages (SourcePage).
export function HomeOverlays({
  layers,
  selected,
  errors,
  children,
}: HomeOverlaysProps): ReactElement {
  const {
    open,
    hide,
    setSearch,
    setAllRecent,
    setMediaServers,
    openMediaServers,
    showSettings,
    openSearch,
  } = layers;
  const { lead, live } = selected;
  return (
    <>
      <Notices lead={lead} lost={live.unreachable} layers={layers} errors={errors} />
      <ErrorPage
        open={errors.blocked.open}
        message={errors.blocked.text}
        onClose={errors.dismissBlocked}
      />
      <DrawerMenu
        open={open.drawer}
        onClose={() => hide('drawer')}
        at={drawerAt(layers)}
        onRecent={() => {
          hide('settings');
          setSearch(null);
          setAllRecent(false);
          setMediaServers(false);
        }}
        onSpotify={() => openSearch({ service: 'spotify' })}
        onTuneIn={() => openSearch({})}
        onMediaServers={openMediaServers}
        onSettings={() => showSettings(null)}
      />
      {children}
    </>
  );
}
