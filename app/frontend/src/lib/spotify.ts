// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Spotify browsing and playback through the speaker. The speaker owns the Spotify
// session; this module only ever sees SoundTap's own models — never a token.
import { contract, deadline } from './api/http';
import { apiFetch, apiURL } from './base';

export interface SpotifyRef {
  uri: string;
  name: string;
}

export interface SpotifyTrack {
  uri: string;
  name: string;
  artists: SpotifyRef[];
  album?: SpotifyRef;
  artwork?: string;
  durationMs: number;
}

export interface SpotifyAlbum {
  uri: string;
  name: string;
  artists: SpotifyRef[];
  artwork?: string;
  releaseDate?: string;
  group?: 'album' | 'single' | 'compilation' | 'appears_on';
  total?: number;
  tracks?: SpotifyTrack[];
  next?: number;
}

export interface SpotifyPlaylist {
  uri: string;
  name: string;
  owner?: string;
  artwork?: string;
  total: number;
  tracks?: SpotifyTrack[];
  next?: number;
}

export interface SpotifyArtistPage {
  artist: SpotifyRef & { artwork?: string };
  topTracks: SpotifyTrack[];
  albums: SpotifyAlbum[];
}

export interface SpotifyRecent {
  kind: 'album' | 'artist' | 'playlist';
  uri: string;
  name: string;
  artwork?: string;
}

export interface SpotifyArtist {
  uri: string;
  name: string;
  artwork?: string;
}

export interface SpotifyPage<T> {
  items: T[];
  total: number;
  next?: number;
}

export interface SpotifyStatus {
  available: boolean;
  authenticated: boolean;
  connected: boolean;
  reauthRequired: boolean;
  state: string;
}

// The pseudo context for "play my Liked Songs"; the speaker resolves it.
export const LIKED_CONTEXT = 'spotify:collection';
export const SPOTIFY_SEARCH_URL = 'https://open.spotify.com/search';

export class SpotifyError extends Error {
  constructor(public code: string) {
    super(code);
  }
}

// The speaker gives a Spotify call up to 15 s; without a deadline of its own a hung request
// held the status poll forever and the tab span until a reload.
const SPOTIFY_TIMEOUT_MS = 20000;

async function call<T>(path: string, init?: RequestInit, speaker?: string): Promise<T> {
  const r = await apiFetch(apiURL(path, speaker), {
    ...init,
    signal: init?.signal ?? deadline(SPOTIFY_TIMEOUT_MS),
  });
  if (!r.ok) {
    let code = 'spotify_playback_failed';
    try {
      const body: unknown = await r.json();
      const fields: { error?: unknown } = typeof body === 'object' && body !== null ? body : {};
      if (typeof fields.error === 'string') code = fields.error;
    } catch {
      /* not JSON */
    }
    throw new SpotifyError(code);
  }
  return contract<T>(r.json());
}

const post = async <T>(path: string, body?: unknown, speaker?: string): Promise<T> =>
  call<T>(
    path,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body ?? {}),
    },
    speaker
  );

// idOf takes the base62 id out of a spotify:<kind>:<id> URI.
export const idOf = (uri: string): string => uri.split(':')[2] ?? '';

// artworkURL routes an artwork path through the same speaker-selection as every API
// call, so another speaker's artwork loads through the one serving the page.
export const artworkURL = (path?: string): string | undefined => (path ? apiURL(path) : undefined);

export const spotifyStatus = async (): Promise<SpotifyStatus> =>
  call<SpotifyStatus>('/api/spotify/status');
export const spotifyArtist = async (id: string): Promise<SpotifyArtistPage> =>
  call<SpotifyArtistPage>(`/api/spotify/artists/${id}`);
export const spotifyAlbum = async (id: string, offset = 0): Promise<SpotifyAlbum> =>
  call<SpotifyAlbum>(`/api/spotify/albums/${encodeURIComponent(id)}?offset=${offset}`);
export const spotifyPlaylist = async (id: string, offset = 0): Promise<SpotifyPlaylist> =>
  call<SpotifyPlaylist>(`/api/spotify/playlists/${encodeURIComponent(id)}?offset=${offset}`);
export const spotifyMyPlaylists = async (offset = 0): Promise<SpotifyPage<SpotifyPlaylist>> =>
  call<SpotifyPage<SpotifyPlaylist>>(`/api/spotify/me/playlists?offset=${offset}`);
export const spotifyLiked = async (offset = 0): Promise<SpotifyPage<SpotifyTrack>> =>
  call<SpotifyPage<SpotifyTrack>>(`/api/spotify/me/tracks?offset=${offset}`);
// item is what a held preset key stores while this plays (the same as a preset made here).
export const spotifyRecent = async (): Promise<SpotifyRecent[]> =>
  call<SpotifyRecent[]>('/api/spotify/me/recent');
export const spotifyFollowing = async (): Promise<SpotifyArtist[]> =>
  call<SpotifyArtist[]>('/api/spotify/me/following');
export const spotifyPlay = async (
  uri: string | undefined,
  context?: string,
  item?: { uri: string; name: string; artwork?: string | undefined },
  speaker?: string
): Promise<{ status: string }> =>
  post<{ status: string }>(
    '/api/spotify/play',
    {
      uri: uri ?? '',
      context: context ?? '',
      item: item && { uri: item.uri, name: item.name, art: item.artwork ?? '' },
    },
    speaker
  );
export const spotifyDisconnect = async (): Promise<{ status: string }> =>
  post<{ status: string }>('/api/spotify/disconnect');

// statusView turns the session state into the one thing the sheet should show.
export type SpotifyView = 'ready' | 'pair' | 'reauth' | 'connecting' | 'unavailable';
export function statusView(s: SpotifyStatus | null): SpotifyView {
  if (s?.available !== true) return 'unavailable';
  if (s.connected) return 'ready';
  if (s.reauthRequired) return 'reauth';
  if (s.authenticated) return 'connecting';
  return 'pair';
}

export const artistNames = (artists: SpotifyRef[]): string => artists.map((a) => a.name).join(', ');

// The artists of a list of tracks, each once, in order of appearance.
export const artistsOf = (tracks: SpotifyTrack[]): SpotifyRef[] => [
  ...new Map(tracks.flatMap((t) => t.artists).map((a) => [a.uri, a])).values(),
];
