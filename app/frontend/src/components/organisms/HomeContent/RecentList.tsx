// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { SheetEmpty } from '../../molecules/SheetEmpty';
import { useState } from 'react';
import { Button, Menu, MenuItem, MenuTrigger, Popover } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Art } from '../../molecules/Art';
import { useLongPress } from '../../../hooks/useLongPress';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import { timeAgo, type RecentItem } from '../../../lib/recent';

const SERVICE: Record<string, string> = { TUNEIN: 'TuneIn', SPOTIFY: 'Spotify' };

interface Props {
  recent: RecentItem[];
  speaker: string;
  onPlayRecent: (item: RecentItem) => void;
  onRemoveRecent: (item: RecentItem) => void;
}

interface RowProps {
  item: RecentItem;
  speaker: string;
  onPlay: () => void;
  onRemove: () => void;
}

// One entry: a tap plays it again, a long press opens the menu to remove it, as in stock.
// The menu's button stays out of sight until the keyboard reaches it.
function RecentRow({ item, speaker, onPlay, onRemove }: RowProps) {
  const { t, lang } = useI18n();
  const [menuOpen, setMenuOpen] = useState(false);
  const press = useLongPress(() => setMenuOpen(true), onPlay);
  return (
    <li className="recent-row">
      <button type="button" className="recent-play" {...press}>
        <Art
          src={logoURL({ logo: item.art, tuneInId: item.tuneInId }, speaker)}
          name={item.name}
          className="recent-art"
        />
        <span className="recent-text">
          <span>{item.name}</span>
          <small>
            {[timeAgo(item.stoppedAt, lang), SERVICE[item.source]].filter(Boolean).join(' · ')}
          </small>
        </span>
      </button>
      <MenuTrigger isOpen={menuOpen} onOpenChange={setMenuOpen}>
        <Button className="recent-more" aria-label={t('moreFor', { name: item.name })}>
          <Icon.dots />
        </Button>
        <Popover className="sl-pop" placement="bottom end">
          <Menu
            className="sl-menu"
            aria-label={t('moreFor', { name: item.name })}
            onAction={onRemove}
          >
            <MenuItem id="remove" className="sl-item danger">
              {t('remove')}
            </MenuItem>
          </Menu>
        </Popover>
      </MenuTrigger>
    </li>
  );
}

// Everything played recently, newest first.
export function RecentList({ recent, speaker, onPlayRecent, onRemoveRecent }: Props) {
  const { t } = useI18n();
  if (!recent.length) return <SheetEmpty>{t('recentlyPlayedEmpty')}</SheetEmpty>;
  return (
    <ul className="recent" aria-label={t('recentlyPlayed')}>
      {recent.map((item) => (
        <RecentRow
          key={item.key}
          item={item}
          speaker={speaker}
          onPlay={() => onPlayRecent(item)}
          onRemove={() => onRemoveRecent(item)}
        />
      ))}
    </ul>
  );
}
