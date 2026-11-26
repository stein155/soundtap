// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type ReactNode, type ReactElement } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { SheetModal } from '../../molecules/SheetModal';
import { exitDuration } from '../../molecules/SheetModal/motion';
import { useI18n } from '../../../lib/i18n';

function Row({
  icon,
  color,
  active,
  onPress,
  children,
}: {
  readonly icon: ReactNode;
  // A source's own colour, for the bar of the active row.
  readonly color?: string;
  readonly active?: boolean;
  readonly onPress: () => void;
  readonly children: string;
}) {
  return (
    <Button
      className="mrow"
      // React Aria's Button takes no undefined style or aria-current: a prop it does not
      // get stays off, as undefined would.
      {...(color && { style: { '--source': color } })}
      data-active={active === true || undefined}
      {...(active === true && { 'aria-current': 'page' as const })}
      onPress={onPress}
    >
      <span className="mico">{icon}</span>
      {children}
    </Button>
  );
}

// The main pages the menu goes to, besides the start page (Just for You).
export type DrawerPage = 'spotify' | 'tunein' | 'mediaservers' | 'settings';

interface Props {
  readonly open: boolean;
  readonly onClose: () => void;
  // The main page showing: the start page unless a source or the settings are open.
  readonly at?: DrawerPage | undefined;
  readonly onRecent: () => void;
  readonly onSpotify: () => void;
  readonly onTuneIn: () => void;
  // The DLNA/UPnP servers on the network, a source like the others.
  readonly onMediaServers: () => void;
  readonly onSettings: () => void;
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
}: Props): ReactElement {
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
    // As long as the drawer's exit (--dur-exit), and no wait with less motion.
    pending.current = setTimeout(action, exitDuration());
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
        <Row
          icon={<Icon.folder />}
          active={at === 'mediaservers'}
          onPress={() => go(onMediaServers)}
        >
          {t('page_mediaservers')}
        </Row>
        <hr />
        <Row icon={<Icon.settings />} active={at === 'settings'} onPress={() => go(onSettings)}>
          {t('settings')}
        </Row>
      </nav>
    </SheetModal>
  );
}
