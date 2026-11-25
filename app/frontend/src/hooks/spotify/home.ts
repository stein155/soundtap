// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import {
  spotifyFollowing,
  spotifyMyPlaylists,
  spotifyRecent,
  type SpotifyArtist,
  type SpotifyPage,
  type SpotifyPlaylist,
  type SpotifyRecent,
} from '../../lib/spotify';

// How long after a failed first rootlist page it is read again.
const ROOTLIST_RETRY_MS = 15000;
// 20 pages of 50 = 1000 rootlist entries; add a More button if anyone has more.
const MAX_PLAYLIST_PAGES = 20;

// The whole rootlist, page by page: the home list has no More button, so a
// playlist past the first page would otherwise be unreachable.
function usePlaylists(ready: boolean) {
  const [playlists, setPlaylists] = useState<SpotifyPlaylist[]>([]);
  // The first page answered, or failed: until then the home list shows the loader.
  const [homeLoaded, setHomeLoaded] = useState(false);
  useEffect(() => {
    if (!ready) return undefined;
    let alive = true;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let all: SpotifyPlaylist[] = [];
    let offset: number | undefined = 0;
    let page = 0;
    const load = async () => {
      for (; offset !== undefined && page < MAX_PLAYLIST_PAGES; page++) {
        const r: SpotifyPage<SpotifyPlaylist> | null = await spotifyMyPlaylists(offset).catch(
          () => null
        );
        if (!alive) return;
        setHomeLoaded(true);
        if (!r) {
          // A failed page is tried again, keeping the ones before it; nothing would ask for
          // it otherwise.
          if (!page) setPlaylists([]);
          retry = setTimeout(() => void load(), ROOTLIST_RETRY_MS);
          return;
        }
        all = [...all, ...r.items];
        setPlaylists(all);
        offset = r.next !== undefined && r.next > offset ? r.next : undefined;
      }
    };
    void load();
    return () => {
      alive = false;
      clearTimeout(retry);
    };
  }, [ready]);
  return { playlists, homeLoaded };
}

export interface SpotifyHome {
  playlists: SpotifyPlaylist[];
  homeLoaded: boolean;
  recent: SpotifyRecent[];
  following: SpotifyArtist[];
}

// The home list: the playlists, and as extras the recent plays and followed artists. A
// failed extra hides its group; the rest of the library still works.
export function useSpotifyHome(ready: boolean): SpotifyHome {
  const { playlists, homeLoaded } = usePlaylists(ready);
  const [recent, setRecent] = useState<SpotifyRecent[]>([]);
  const [following, setFollowing] = useState<SpotifyArtist[]>([]);
  useEffect(() => {
    if (!ready) return undefined;
    let alive = true;
    spotifyRecent().then(
      (r) => alive && setRecent(r),
      () => alive && setRecent([])
    );
    spotifyFollowing().then(
      (r) => alive && setFollowing(r),
      () => alive && setFollowing([])
    );
    return () => {
      alive = false;
    };
  }, [ready]);
  return { playlists, homeLoaded, recent, following };
}
