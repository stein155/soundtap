// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { contract, getJSON, send, SLOW_READ_TIMEOUT_MS } from './http';
import type { UPnPServer, UPnPItem } from '../types';
import type { SpotifyModes, SpotifyRepeat } from './playback';

// UPnP ContentDirectory discovery.
export async function getUPnPServers(speaker?: string): Promise<UPnPServer[] | null> {
  try {
    return await getJSON<UPnPServer[]>('/api/upnp/servers', speaker);
  } catch {
    return null;
  }
}

// searchUPnPServers has the speaker search now and answers once the round is over.
export async function searchUPnPServers(speaker?: string): Promise<UPnPServer[] | null> {
  try {
    return await contract<UPnPServer[] | null>(
      send('/api/upnp/search', 'POST', undefined, speaker)
    );
  } catch {
    return null;
  }
}

export async function browseUPnP(
  usn: string,
  id = '0',
  speaker?: string
): Promise<UPnPItem[] | null> {
  try {
    return await getJSON<UPnPItem[]>(
      `/api/upnp/browse?server=${encodeURIComponent(usn)}&id=${encodeURIComponent(id)}`,
      speaker,
      SLOW_READ_TIMEOUT_MS
    );
  } catch {
    return null;
  }
}

// playUPnP hands a browse result's media URL to the speaker, which streams it as the
// UPNP source, and the rest of the folder it came from after it. The server rejects anything
// that is not an http(s) URL.
export async function playUPnP(
  track: { url: string; name: string; server: string; folder: string },
  speaker?: string
): Promise<unknown> {
  return send('/api/upnp/play', 'POST', track, speaker);
}

// The played folder's shuffle and repeat, in the shape the Spotify buttons use: the speaker
// says "all" where Spotify says "context".
export async function upnpModes(speaker?: string): Promise<SpotifyModes> {
  const m = await getJSON<{ shuffle?: boolean; repeat?: string }>('/api/upnp/modes', speaker);
  return { shuffle: m.shuffle ?? false, repeat: repeatOf(m.repeat) };
}

// The speaker's repeat mode in Spotify's words.
function repeatOf(mode: string | undefined): SpotifyRepeat {
  if (mode === 'all') return 'context';
  if (mode === 'track') return 'track';
  return 'off';
}

export async function setUPnPShuffle(on: boolean, speaker?: string): Promise<unknown> {
  return send('/api/upnp/modes', 'POST', { shuffle: on }, speaker);
}

export async function setUPnPRepeat(mode: SpotifyRepeat, speaker?: string): Promise<unknown> {
  return send('/api/upnp/modes', 'POST', { repeat: mode === 'context' ? 'all' : mode }, speaker);
}

// isUPnPContainer reports whether a browse result is a folder to open rather than a
// track to play. DIDL marks folders with an object.container class, and a container
// carries no resource URL.
export function isUPnPContainer(item: UPnPItem): boolean {
  return item.class.startsWith('object.container') || !item.url;
}
