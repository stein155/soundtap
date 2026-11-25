// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { targetDevice } from '../lib/base';
import { spotifyPlay } from '../lib/spotify';
import type { SpotifyArtist, SpotifyPlaylist, SpotifyRecent, SpotifyView } from '../lib/spotify';
import { useSpotifyHome } from './spotify/home';
import { useLibraryPages, type LibraryPage, type LoadedPage } from './spotify/pages';
import { useSpotifyError, useSpotifySession } from './spotify/session';

export type { LibraryPage, LoadedPage } from './spotify/pages';

export interface PlayItem {
  uri: string;
  name: string;
  artwork?: string | undefined;
}

export interface SpotifyLibrary {
  view: SpotifyView;
  statusLoaded: boolean;
  playlists: SpotifyPlaylist[];
  homeLoaded: boolean;
  recent: SpotifyRecent[];
  following: SpotifyArtist[];
  trail: LibraryPage[];
  loadingMore: boolean;
  page: LoadedPage | null;
  error: string;
  hold: ((item: PlayItem) => void) | undefined;
  open: (next: LibraryPage) => void;
  canGoUp: boolean;
  goUp: () => void;
  retry: () => void;
  loadMore: () => Promise<void>;
  play: (uri: string | undefined, context: string | undefined, item: PlayItem) => Promise<void>;
  disconnect: () => Promise<void>;
}

// The selected speaker's own Spotify library (its librespot session, ADR 0014): Liked
// Songs, playlists, recent plays and followed artists, drilled into tracks, albums and artists. Searching Spotify is deliberately
// not offered. `hold` saves what a row holds on a preset.
// `opened`: the sheet is open. Closing it resets the library; switching to the TuneIn tab
// and back does not.
export function useSpotifyLibrary(
  active: boolean,
  hold: ((item: PlayItem) => void) | undefined,
  onPlayed: () => void,
  opened = active
): SpotifyLibrary {
  const { view, statusLoaded, renew, disconnect } = useSpotifySession(active);
  const home = useSpotifyHome(active && view === 'ready');
  const { error, setError, showError } = useSpotifyError(renew);
  const pages = useLibraryPages(opened, { setError, showError });
  const { trail, show, openingRef } = pages;

  const play = async (uri: string | undefined, context: string | undefined, item: PlayItem) => {
    setError('');
    // The request goes to the speaker selected at the tap, even if the room changes while
    // it is in flight.
    const speaker = targetDevice();
    // The request finishes on the speaker either way, but a sheet closed meanwhile has
    // nothing to hear about it, and a reopened one must stay open.
    const open = openingRef.current;
    try {
      await spotifyPlay(uri, context, item, speaker);
      if (open === openingRef.current) onPlayed();
    } catch (e) {
      if (open === openingRef.current) showError(e);
    }
  };

  return {
    view,
    statusLoaded,
    ...home,
    trail,
    loadingMore: pages.loadingMore,
    page: pages.page,
    error,
    hold,
    open: (next: LibraryPage) => show([...trail, next]),
    canGoUp: trail.length > 0,
    goUp: () => show(trail.slice(0, -1)),
    retry: () => show(trail),
    loadMore: pages.loadMore,
    play,
    disconnect,
  };
}
