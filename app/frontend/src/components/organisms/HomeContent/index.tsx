// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import {
  Button,
  ListBox,
  ListBoxItem,
  Menu,
  MenuItem,
  MenuTrigger,
  Popover,
} from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Art } from '../../molecules/Art';
import { useI18n } from '../../../lib/i18n';
import { logoURL } from '../../../lib/artwork';
import type { RecentItem } from '../../../lib/recent';
import type { SearchMode } from '../SearchSheet';

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
  onAllRecent,
  onSearch,
}: Props) {
  const { t } = useI18n();
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
              <li key={item.key} className="card">
                <Button className="card-play" onPress={() => onPlayRecent(item)}>
                  <Art
                    src={logoURL({ logo: item.art, tuneInId: item.tuneInId }, speaker)}
                    name={item.name}
                    className="card-art"
                  />
                  <span className="card-label">
                    <span>{item.name}</span>
                  </span>
                </Button>
                <MenuTrigger>
                  <Button className="card-more" aria-label={t('moreFor', { name: item.name })}>
                    <Icon.dots />
                  </Button>
                  <Popover className="sl-pop" placement="bottom end">
                    <Menu
                      className="sl-menu"
                      aria-label={t('moreFor', { name: item.name })}
                      onAction={() => onRemoveRecent(item)}
                    >
                      <MenuItem id="remove" className="sl-item danger">
                        {t('remove')}
                      </MenuItem>
                    </Menu>
                  </Popover>
                </MenuTrigger>
              </li>
            ))}
          </ul>
        </section>
      )}

      <Services spotifyReady={spotifyReady} speakerName={speakerName} onSearch={onSearch} />
    </div>
  );
}
