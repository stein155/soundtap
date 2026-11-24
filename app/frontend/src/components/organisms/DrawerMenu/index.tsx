// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type CSSProperties, type ReactNode } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';

function Row({
  icon,
  color,
  active,
  onPress,
  children,
}: {
  icon: ReactNode;
  // A source's own colour, for the bar of the active row.
  color?: string;
  active?: boolean;
  onPress: () => void;
  children: string;
}) {
  return (
    <Button
      className="mrow"
      style={color ? ({ '--source': color } as CSSProperties) : undefined}
      data-active={active || undefined}
      aria-current={active ? 'page' : undefined}
      onPress={onPress}
    >
      <span className="mico">{icon}</span>
      {children}
    </Button>
  );
}

// The drawer's exit animation (home.css).
const DRAWER_EXIT_MS = 200;

interface Props {
  open: boolean;
  onClose: () => void;
  // The main page showing: the start page unless a source is open.
  at?: 'spotify' | 'tunein';
  onRecent: () => void;
  onSpotify: () => void;
  onTuneIn: () => void;
  // The DLNA/UPnP servers on the network, a source like the others.
  onMediaServers: () => void;
  onSettings: () => void;
}

// The menu from the left: where else the app goes. Each row closes the menu, then goes; the
// home page is Just for You, so that row only goes back to it. A source's icon is grey
// until its row is the active one.
export function DrawerMenu({
  open,
  onClose,
  at,
  onRecent,
  onSpotify,
  onTuneIn,
  onMediaServers,
  onSettings,
}: Props) {
  const { t } = useI18n();
  // The drawer leaves first: a page swapped in under it, or a settings page opened over it,
  // cut across its slide. Only the latest pick goes: reopening the menu, or another row,
  // drops one still waiting.
  const pending = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => {
    if (open) clearTimeout(pending.current);
  }, [open]);
  useEffect(() => () => clearTimeout(pending.current), []);
  const go = (action: () => void) => {
    onClose();
    clearTimeout(pending.current);
    pending.current = setTimeout(action, DRAWER_EXIT_MS);
  };
  return (
    <SheetModal open={open} onClose={onClose} label={t('menu')} variant="drawer">
      <nav className="menu">
        <Row icon={<Icon.clock />} active={!at} onPress={() => go(onRecent)}>
          {t('justForYou')}
        </Row>
        <Row
          icon={<Icon.spotifyMark />}
          color="#1ed760"
          active={at === 'spotify'}
          onPress={() => go(onSpotify)}
        >
          Spotify
        </Row>
        <Row
          icon={<Icon.tuneinMark />}
          color="#1c203c"
          active={at === 'tunein'}
          onPress={() => go(onTuneIn)}
        >
          TuneIn
        </Row>
        <Row icon={<Icon.folder />} onPress={() => go(onMediaServers)}>
          {t('page_mediaservers')}
        </Row>
        <hr />
        <Row icon={<Icon.cog />} onPress={() => go(onSettings)}>
          {t('settings')}
        </Row>
      </nav>
    </SheetModal>
  );
}
