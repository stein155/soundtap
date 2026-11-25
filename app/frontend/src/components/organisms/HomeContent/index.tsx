// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { Button, ListBox, ListBoxItem } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Art } from '../../molecules/Art';
import { useLongPress } from '../../../hooks/useLongPress';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import type { RecentItem } from '../../../lib/recent';
import type { SearchMode } from '../SearchSheet';
import { RecentActions, serviceName } from './RecentActions';

// The rail shows the newest few; the heading opens them all.
const RAIL_ITEMS = 10;

interface Props {
  recent: RecentItem[];
  speaker: string;
  // The room's name, for the "pick it in the Spotify app" hint.
  speakerName: string;
  spotifyReady: boolean;
  onPlayRecent: (item: RecentItem) => void;
  onRemoveRecent: (item: RecentItem) => void;
  // Puts an entry on a preset; left out while there is no room to store it on.
  onHoldRecent?: ((item: RecentItem) => void) | undefined;
  onAllRecent: () => void;
  onSearch: (mode: SearchMode) => void;
}

// The two music services, each a way into its page.
function Services({
  spotifyReady,
  speakerName,
  onSearch,
}: Pick<Props, 'spotifyReady' | 'speakerName' | 'onSearch'>) {
  const { t } = useI18n();
  return (
    <section aria-labelledby="home-services">
      <h2 className="h-row" id="home-services">
        {t('services')}
      </h2>
      <ListBox
        className="svc-list"
        aria-labelledby="home-services"
        layout="stack"
        selectionMode="none"
        onAction={(key) => onSearch(key === 'spotify' ? { service: 'spotify' } : {})}
      >
        <ListBoxItem id="spotify" textValue="Spotify" className="svc-row">
          <span className="svc spotify" aria-hidden="true">
            <Icon.spotifyMark />
          </span>
          <span className="svc-text">
            Spotify
            <small>
              {spotifyReady ? t('spBrowseSub') : t('spConnectShort', { name: speakerName })}
            </small>
          </span>
          <Icon.chevron aria-hidden="true" />
        </ListBoxItem>
        <ListBoxItem id="tunein" textValue={t('findNew')} className="svc-row">
          <span className="svc tunein" aria-hidden="true">
            <Icon.tuneinMark />
          </span>
          <span className="svc-text">
            {t('findNew')}
            <small>{t('findNewSub')}</small>
          </span>
          <Icon.chevron aria-hidden="true" />
        </ListBoxItem>
      </ListBox>
    </section>
  );
}

// One tile of the rail: a tap plays it again, a long press (or the ⋯ the keyboard finds)
// opens what else can be done.
function RecentCard({
  item,
  speaker,
  onPlay,
  onMore,
}: {
  item: RecentItem;
  speaker: string;
  onPlay: () => void;
  onMore: () => void;
}) {
  const { t } = useI18n();
  const press = useLongPress(onMore, onPlay);
  return (
    <li className="card">
      <button type="button" className="card-play" {...press}>
        <Art
          src={logoURL({ logo: item.art, tuneInId: item.tuneInId }, speaker)}
          name={item.name}
          className="card-art"
        />
        <span className="card-label">
          <span>{item.name}</span>
          <small>{serviceName(item)}</small>
        </span>
      </button>
      <Button
        className="card-more"
        aria-label={t('moreFor', { name: item.name })}
        aria-haspopup="dialog"
        onPress={onMore}
      >
        <Icon.dots />
      </Button>
    </li>
  );
}

// The start page, stock's Just for You: what was played recently (tap to play it again; the
// heading opens the whole list) and the two music services. The presets are behind the top
// bar's button.
export function HomeContent({
  recent,
  speaker,
  speakerName,
  spotifyReady,
  onPlayRecent,
  onRemoveRecent,
  onHoldRecent,
  onAllRecent,
  onSearch,
}: Props): ReactElement {
  const { t } = useI18n();
  const [actionsFor, setActionsFor] = useState<RecentItem | null>(null);
  return (
    <div className="home">
      {recent.length > 0 && (
        <section aria-labelledby="home-recent">
          <h2 className="h-link" id="home-recent">
            <Button className="h-row" onPress={onAllRecent}>
              {t('recentlyPlayed')} <Icon.chevron />
            </Button>
          </h2>
          {/* A list of its own, not a ListBox: each card holds two buttons, play and ⋯. */}
          <ul className="rail" aria-labelledby="home-recent">
            {recent.slice(0, RAIL_ITEMS).map((item) => (
              <RecentCard
                key={item.key}
                item={item}
                speaker={speaker}
                onPlay={() => onPlayRecent(item)}
                onMore={() => setActionsFor(item)}
              />
            ))}
          </ul>
        </section>
      )}

      <Services spotifyReady={spotifyReady} speakerName={speakerName} onSearch={onSearch} />
      <RecentActions
        item={actionsFor}
        speaker={speaker}
        onClose={() => setActionsFor(null)}
        onPlay={onPlayRecent}
        onHold={onHoldRecent}
        onRemove={onRemoveRecent}
      />
    </div>
  );
}
