// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Icon } from '../../atoms/Icon';
import { Art } from '../../molecules/Art';
import { ConfirmAction } from '../../molecules/ConfirmAction';
import { ListRow } from '../../molecules/ListRow';
import { EntryRow, SearchEntry } from '../../molecules/SearchEntry';
import { SheetGroup } from '../../molecules/SheetGroup';
import { SheetLoading } from '../../molecules/SheetLoading';
import { useLongPress } from '../../../hooks/useLongPress';
import type { SpotifyLibrary as Library } from '../../../hooks/useSpotifyLibrary';
import { useI18n } from '../../../lib/i18n';
import { SPOTIFY_SEARCH_URL, artworkURL } from '../../../lib/spotify';
import { Account, AccountName, LinkButton } from './styled';

interface TileItem {
  uri: string;
  name: string;
  artwork?: string;
}

function Tile({
  item,
  sub,
  onOpen,
  onHold,
}: {
  item: TileItem;
  sub?: string;
  onOpen: () => void;
  onHold?: () => void;
}) {
  const press = useLongPress(onHold ?? null, onOpen);
  return (
    <li className="card">
      <button type="button" className="card-play" {...press}>
        <Art src={artworkURL(item.artwork)} name={item.name} className="card-art" />
        <span className="card-label">
          <span>
            {item.name}
            {sub && <small>{sub}</small>}
          </span>
        </span>
      </button>
    </li>
  );
}

// A titled row of tiles that scrolls sideways, as Stockholm shows each part of Spotify.
export function Ribbon<T extends TileItem>({
  title,
  items,
  sub,
  onOpen,
  onHold,
}: {
  title: string;
  items: T[];
  sub?: (item: T) => string | undefined;
  onOpen: (item: T) => void;
  onHold?: (item: T) => void;
}) {
  if (!items.length) return null;
  return (
    <section className="ribbon">
      <h2 className="h-row">{title}</h2>
      <ul className="rail" aria-label={title}>
        {items.map((item) => (
          <Tile
            key={item.uri}
            item={item}
            sub={sub?.(item)}
            onOpen={() => onOpen(item)}
            onHold={onHold && (() => onHold(item))}
          />
        ))}
      </ul>
    </section>
  );
}

// The library's top level, laid out as Stockholm's: the search bar, a ribbon per part, the
// songs as a row under them. Stockholm's featured, genre and chart ribbons need Spotify's
// catalogue, which this session cannot read.
export function Home({ library }: { library: Library }) {
  const { t } = useI18n();
  const hold = (item: TileItem) =>
    library.hold?.({ uri: item.uri, name: item.name, artwork: item.artwork });
  const search = (
    <EntryRow>
      <SearchEntry label={t('searchSpotifyApp')} href={SPOTIFY_SEARCH_URL} />
    </EntryRow>
  );
  if (!library.homeLoaded)
    return (
      <>
        {search}
        <SheetLoading />
      </>
    );
  return (
    <>
      {search}
      <Ribbon
        title={t('spotifyRecent')}
        items={library.recent}
        onOpen={(item) => library.open({ kind: item.kind, uri: item.uri, title: item.name })}
        onHold={library.hold && hold}
      />
      <Ribbon
        title={t('spPlaylists')}
        items={library.playlists}
        sub={(playlist) => playlist.owner}
        onOpen={(item) => library.open({ kind: 'playlist', uri: item.uri, title: item.name })}
        onHold={library.hold && hold}
      />
      <Ribbon
        title={t('spotifyFollowing')}
        items={library.following}
        onOpen={(item) => library.open({ kind: 'artist', uri: item.uri, title: item.name })}
      />
      <SheetGroup>
        <ListRow
          art={<Icon.spotify width="22" height="22" />}
          title={t('spotifyLiked')}
          onClick={() => library.open({ kind: 'liked', title: t('spotifyLiked') })}
        />
      </SheetGroup>
      <ConfirmAction
        destructive
        label={t('spDisconnect')}
        warning={t('spotifyDisconnectConfirm')}
        confirmLabel={t('spDisconnect')}
        onConfirm={library.disconnect}
        trigger={(ask) => (
          <Account>
            <AccountName>
              <Icon.spotify width="16" height="16" />
              {t('spConnected')}
            </AccountName>
            <LinkButton onClick={ask}>{t('spDisconnect')}</LinkButton>
          </Account>
        )}
      />
    </>
  );
}
