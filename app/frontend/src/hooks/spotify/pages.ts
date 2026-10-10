// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import {
  LIKED_CONTEXT,
  artistNames,
  idOf,
  spotifyAlbum,
  spotifyArtist,
  spotifyLiked,
  spotifyPlaylist,
  type SpotifyAlbum,
  type SpotifyTrack,
} from '../../lib/spotify';
import type { ErrorFallback } from './session';

// One level of the drill-down: what is shown and the context its tracks play in.
export type LibraryPage =
  | { kind: 'liked'; title: string }
  | { kind: 'album'; uri: string; title: string }
  | { kind: 'artist'; uri: string; title: string }
  | { kind: 'playlist'; uri: string; title: string };

export interface LoadedPage {
  title: string;
  sub?: string;
  artwork?: string;
  context: string;
  tracks: SpotifyTrack[];
  albums?: SpotifyAlbum[];
  next?: number;
}

async function loadPage(page: LibraryPage, offset = 0): Promise<LoadedPage> {
  switch (page.kind) {
    case 'liked': {
      const r = await spotifyLiked(offset);
      return { title: page.title, context: LIKED_CONTEXT, tracks: r.items, next: r.next };
    }
    case 'album': {
      const a = await spotifyAlbum(idOf(page.uri), offset);
      return {
        title: a.name,
        sub: [artistNames(a.artists), a.releaseDate?.slice(0, 4)].filter(Boolean).join(' · '),
        artwork: a.artwork,
        context: a.uri,
        tracks: a.tracks ?? [],
        next: a.next,
      };
    }
    case 'artist': {
      const a = await spotifyArtist(idOf(page.uri));
      return {
        title: a.artist.name,
        artwork: a.artist.artwork,
        context: a.artist.uri,
        tracks: a.topTracks,
        albums: a.albums,
      };
    }
    case 'playlist': {
      const pl = await spotifyPlaylist(idOf(page.uri), offset);
      return {
        title: pl.name,
        sub: pl.owner,
        artwork: pl.artwork,
        context: pl.uri,
        tracks: pl.tracks ?? [],
        next: pl.next,
      };
    }
  }
}

const pageKey = (p: LibraryPage) => (p.kind === 'liked' ? 'liked' : p.uri);

interface Errors {
  setError: (text: string) => void;
  showError: (e: unknown, fallback?: ErrorFallback) => void;
}

// Pages already loaded in this opening: going back shows them at once. Closing
// drops them, and answers still in flight, so a reopen fetches fresh pages.
function usePageCache(opened: boolean) {
  const loadedRef = useRef(new Map<string, LoadedPage>());
  const generationRef = useRef(0);
  // Bumped on close: a page answer from an earlier opening may not enter the cache.
  const openingRef = useRef(0);
  useEffect(() => {
    if (opened) return;
    loadedRef.current.clear();
    generationRef.current++;
    openingRef.current++;
  }, [opened]);
  return { loadedRef, generationRef, openingRef };
}

// The drill-down: the trail of pages opened, the one shown, and more of its tracks.
// Closing the tab forgets where in the library it was.
export function useLibraryPages(opened: boolean, { setError, showError }: Errors) {
  const [trail, setTrail] = useState<LibraryPage[]>([]);
  const [page, setPage] = useState<LoadedPage | null>(null);
  // One "more" at a time: a double tap used to fetch the same page twice.
  const [loadingMore, setLoadingMore] = useState(false);
  const [wasOpened, setWasOpened] = useState(opened);
  if (opened !== wasOpened) {
    setWasOpened(opened);
    if (!opened) {
      setTrail([]);
      setPage(null);
      setError('');
    }
  }
  const { loadedRef, generationRef, openingRef } = usePageCache(opened);

  const show = (next: LibraryPage[]) => {
    const gen = ++generationRef.current;
    // A "more" still running belongs to the page left behind, not to this one.
    setLoadingMore(false);
    const open = openingRef.current;
    setTrail(next);
    setError('');
    const target = next.at(-1);
    const cached = target && loadedRef.current.get(pageKey(target));
    setPage(cached ?? null);
    if (!target || cached) return;
    loadPage(target).then(
      (result) => {
        if (open === openingRef.current) loadedRef.current.set(pageKey(target), result);
        if (gen === generationRef.current) setPage(result);
      },
      (e) => gen === generationRef.current && showError(e, 'spotifyLoadFailed')
    );
  };

  const loadMore = async () => {
    if (!page?.next || !trail.length || loadingMore) return;
    // Another page opened meanwhile: this answer belongs to the one left behind.
    const gen = generationRef.current;
    const target = trail[trail.length - 1];
    setLoadingMore(true);
    try {
      const more = await loadPage(target, page.next);
      if (gen !== generationRef.current) return;
      // The cache keeps what was appended, so a revisit shows the whole list.
      const grown = { ...page, tracks: [...page.tracks, ...more.tracks], next: more.next };
      loadedRef.current.set(pageKey(target), grown);
      setPage(grown);
    } catch (e) {
      if (gen === generationRef.current) showError(e, 'spotifyLoadFailed');
    } finally {
      if (gen === generationRef.current) setLoadingMore(false);
    }
  };

  return { trail, page, loadingMore, show, loadMore, openingRef };
}
