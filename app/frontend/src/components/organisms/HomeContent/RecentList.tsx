// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { SheetEmpty } from '../../molecules/SheetEmpty';
import { useState, type ReactElement } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Art } from '../../molecules/Art';
import { useLongPress } from '../../../hooks/useLongPress';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import type { RecentItem } from '../../../lib/recent';
import { RecentActions, recentLine } from './RecentActions';

interface Props {
  readonly recent: RecentItem[];
  readonly speaker: string;
  readonly onPlayRecent: (item: RecentItem) => void;
  readonly onRemoveRecent: (item: RecentItem) => void;
  // Puts an entry on a preset; left out while there is no room to store it on.
  readonly onHoldRecent?: ((item: RecentItem) => void) | undefined;
}

interface RowProps {
  readonly item: RecentItem;
  readonly speaker: string;
  readonly onPlay: () => void;
  readonly onMore: () => void;
}

// One entry: a tap plays it again, a long press or the ⋯ opens what else can be done.
function RecentRow({ item, speaker, onPlay, onMore }: RowProps) {
  const { t, lang } = useI18n();
  const press = useLongPress(onMore, onPlay);
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
          <small>{recentLine(item, lang)}</small>
        </span>
      </button>
      <Button
        className="recent-more"
        aria-label={t('moreFor', { name: item.name })}
        aria-haspopup="dialog"
        onPress={onMore}
      >
        <Icon.more />
      </Button>
    </li>
  );
}

// Everything played recently, newest first.
export function RecentList({
  recent,
  speaker,
  onPlayRecent,
  onRemoveRecent,
  onHoldRecent,
}: Props): ReactElement {
  const { t } = useI18n();
  const [actionsFor, setActionsFor] = useState<RecentItem | null>(null);
  if (recent.length === 0) return <SheetEmpty>{t('recentlyPlayedEmpty')}</SheetEmpty>;
  return (
    <>
      <ul className="recent" aria-label={t('recentlyPlayed')}>
        {recent.map((item) => (
          <RecentRow
            key={item.key}
            item={item}
            speaker={speaker}
            onPlay={() => onPlayRecent(item)}
            onMore={() => setActionsFor(item)}
          />
        ))}
      </ul>
      <RecentActions
        item={actionsFor}
        speaker={speaker}
        onClose={() => setActionsFor(null)}
        onPlay={onPlayRecent}
        onHold={onHoldRecent}
        onRemove={onRemoveRecent}
      />
    </>
  );
}
