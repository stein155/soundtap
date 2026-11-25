// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Icon } from '../../atoms/Icon';
import { FloatingHint } from '../../molecules/FloatingHint';
import { Button } from '../../atoms/Button';
import { Spinner } from '../../atoms/Spinner';
import { StationLogo } from '../../atoms/StationLogo';
import { ListRow } from '../../molecules/ListRow';
import { SheetGroup } from '../../molecules/SheetGroup';
import { SheetLoading } from '../../molecules/SheetLoading';
import { useI18n, type I18nKey } from '../../../lib/i18n';
import { initials } from '../../../lib/artwork';
import {
  artistNames,
  artistsOf,
  artworkURL,
  type SpotifyAlbum,
  type SpotifyView,
} from '../../../lib/spotify';
import type { LoadedPage, SpotifyLibrary as Library } from '../../../hooks/useSpotifyLibrary';
import { Home, Ribbon } from './Home';
import { Cover, Hero, HeroText, Heading, Invitation, LinkButton, Text } from './styled';

const NOT_READY: Record<Exclude<SpotifyView, 'ready'>, I18nKey> = {
  pair: 'spotifyPair',
  reauth: 'spotifyReauth',
  connecting: 'spotifyConnecting',
  unavailable: 'spotifyUnavailable',
};

// How many artists a page offers under its tracks.
const MAX_ARTISTS = 10;

const art = (name: string, path?: string) => (
  <StationLogo src={artworkURL(path) ?? null} initials={initials(name)} />
);

// A long press on a row saves that item, if the page offers saving.
const holdable = (
  library: Library,
  item: { uri: string; name: string; artwork?: string | undefined }
) =>
  library.hold && (() => library.hold?.({ uri: item.uri, name: item.name, artwork: item.artwork }));

// Why the library is not available yet, and what to do about it.
function NotReady({ view }: { view: Exclude<SpotifyView, 'ready'> }) {
  const { t } = useI18n();
  return (
    <Invitation>
      <Icon.spotify width="56" height="56" />
      {/* "Connect your account" over "Spotify isn't running" contradicted itself. */}
      <Heading>{t(view === 'unavailable' ? 'spUnavailableTitle' : 'spConnectTitle')}</Heading>
      <Text>{t(NOT_READY[view])}</Text>
      {view === 'connecting' && <Spinner />}
    </Invitation>
  );
}

// The artists on a page. Search is closed to the session, so these are the way from a song
// to more of its artist. An artist's own page lists them no further.
function PageArtists({ library, page }: { library: Library; page: LoadedPage }) {
  const { t } = useI18n();
  const artists = artistsOf(page.tracks).slice(0, MAX_ARTISTS);
  if (page.context.startsWith('spotify:artist:') || !artists.length) return null;
  return (
    <SheetGroup title={t('spotifyArtists')}>
      {artists.map((artist) => (
        <ListRow
          key={artist.uri}
          art={art(artist.name)}
          title={artist.name}
          onClick={() => library.open({ kind: 'artist', uri: artist.uri, title: artist.name })}
        />
      ))}
    </SheetGroup>
  );
}

// An artist's releases, one sideways row per kind, as Stockholm.
function Releases({ library, albums }: { library: Library; albums: SpotifyAlbum[] }) {
  const { t } = useI18n();
  const groups = [
    ['album', t('spotifyAlbums')],
    ['single', t('spotifySingles')],
    ['compilation', t('spotifyCompilations')],
    ['appears_on', t('spotifyAppearsOn')],
  ] as const;
  return (
    <>
      {groups.map(([group, title]) => (
        <Ribbon
          key={group}
          title={title}
          items={albums.filter((album) => (album.group ?? 'album') === group)}
          sub={(album) => album.releaseDate?.slice(0, 4)}
          onOpen={(album) => library.open({ kind: 'album', uri: album.uri, title: album.name })}
          onHold={library.hold && ((album) => holdable(library, album)?.())}
        />
      ))}
    </>
  );
}

// A playlist, album, artist or Liked Songs: play it all, or one track.
function PageView({ library, page }: { library: Library; page: LoadedPage }) {
  const { t } = useI18n();
  const cover = artworkURL(page.artwork);
  return (
    <>
      <Hero>
        {cover && <Cover src={cover} alt="" />}
        <HeroText>
          <Heading>{page.title}</Heading>
          {page.sub && <Text>{page.sub}</Text>}
          <Button
            variant="primary"
            small
            onClick={() =>
              void library.play(undefined, page.context, {
                uri: page.context,
                name: page.title,
                artwork: page.artwork,
              })
            }
          >
            <Icon.play width="12" height="12" aria-hidden="true" />
            {t('spotifyPlayAll')}
          </Button>
        </HeroText>
      </Hero>
      <SheetGroup title={t('spotifyTracks')}>
        {page.tracks.map((track) => (
          <ListRow
            key={track.uri}
            art={art(track.name, track.artwork)}
            small
            title={track.name}
            subtitle={artistNames(track.artists)}
            onClick={() =>
              // A track starts its list, which then plays on: the list is what is playing and
              // what a held preset key stores. Naming the track kept its title up after it
              // ended.
              void library.play(track.uri, page.context, {
                uri: page.context,
                name: page.title,
                artwork: page.artwork,
              })
            }
            onHold={holdable(library, track)}
          />
        ))}
      </SheetGroup>
      <Releases library={library} albums={page.albums ?? []} />
      <PageArtists library={library} page={page} />
      {(page.next ?? 0) !== 0 ? (
        <Button
          variant="dashed"
          disabled={library.loadingMore}
          aria-label={library.loadingMore ? t('loading') : undefined}
          onClick={() => void library.loadMore()}
        >
          {library.loadingMore ? <Spinner /> : t('spotifyMore')}
        </Button>
      ) : null}
    </>
  );
}

// What the library shows: its home, an opened page, a retry after a failed load, or the
// spinner while a page loads.
function LibraryBody({ library }: { library: Library }): ReactElement {
  const { t } = useI18n();
  if (!library.trail.length) return <Home library={library} />;
  if (library.page) return <PageView library={library} page={library.page} />;
  // The load failed and is over: offer it again instead of a spinner.
  if (library.error) return <LinkButton onClick={library.retry}>{t('reconnect')}</LinkButton>;
  return <SheetLoading />;
}

// The Spotify tab's body.
export function SpotifyLibrary({ library }: { library: Library }): ReactElement {
  if (!library.statusLoaded) return <SheetLoading />;
  if (library.view !== 'ready') return <NotReady view={library.view} />;
  return (
    <>
      <LibraryBody library={library} />
      {library.error && (
        <FloatingHint error role="alert">
          {library.error}
        </FloatingHint>
      )}
    </>
  );
}
