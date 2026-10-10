// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// What a room is doing, read from the speaker's normalised now-playing. Shared by the
// room cards, the dock and the now-playing screen so they never
// disagree about whether something plays.

import type { I18nKey } from './i18n';
import type { NowPlaying } from './types';

// The design has three sources: the network ones (radio, Spotify, media servers --
// "wifi"), Bluetooth and the AUX input.
export type Source = 'wifi' | 'bluetooth' | 'aux' | 'tv' | 'hdmi';

export function sourceOf(np: NowPlaying | null | undefined): Source {
  if (!np || np.standby) return 'wifi';
  if (np.source === 'BLUETOOTH') return 'bluetooth';
  if (np.source === 'AUX') return 'aux';
  // The soundbar processor's own inputs (ST300): its HDMI inputs, else TV.
  if (np.source === 'PRODUCT') return np.stationName?.startsWith('HDMI') ? 'hdmi' : 'tv';
  return 'wifi';
}

// The inputs a speaker offers, from /api/user-sources. The network sources are always
// there; Bluetooth and AUX only on a model whose hardware has them proven.
export const SOURCE_LABEL: Record<Source, I18nKey> = {
  wifi: 'src_wifi',
  bluetooth: 'src_bluetooth',
  aux: 'src_aux',
  tv: 'src_tv',
  hdmi: 'src_hdmi',
};
export const SOURCE_DESCRIPTION: Record<Exclude<Source, 'wifi'>, I18nKey> = {
  bluetooth: 'srcDesc_bluetooth',
  aux: 'srcDesc_aux',
  tv: 'srcDesc_tv',
  hdmi: 'srcDesc_hdmi',
};

export function sourcesOf(userSources: string[]): Source[] {
  const out: Source[] = ['wifi'];
  if (userSources.includes('BLUETOOTH')) out.push('bluetooth');
  if (userSources.includes('AUX')) out.push('aux');
  if (userSources.includes('TV')) out.push('tv');
  if (userSources.includes('HDMI')) out.push('hdmi');
  return out;
}

export interface RoomStation {
  title: string; // what a view leads with
  key: string; // stable identity for colour: TuneIn id, else the name
  name: string;
  sub: string;
  art: string;
  tuneInId: string | null;
  spotify: boolean;
  skips: boolean; // next and previous do something
  source: string;
  paused: boolean;
}

// Whether next and previous do something: Spotify's own, or the speaker's keys.
const canSkip = (np: NowPlaying) => np.source === 'SPOTIFY' || np.canSkip === true;

// Spotify leads with the song, as Stockholm does; the playlist it came from stays the name
// that presets and recents go by. Radio leads with the station.
function stationLines(np: NowPlaying, name: string, spotify: boolean) {
  const title = (spotify && np.track) || name;
  const sub = [np.artist, np.track && np.track !== title ? np.track : '', np.album]
    .filter(Boolean)
    .join(' • ');
  return { title, sub };
}

// A network source playing something nameable, or null.
export function stationOf(np: NowPlaying | null | undefined): RoomStation | null {
  if (!np || np.standby || sourceOf(np) !== 'wifi') return null;
  if (np.source === 'MULTIROOM') return null; // following: the leader's card says what
  if (np.playStatus === 'STOP_STATE') return null;
  const name = np.stationName || np.track || '';
  if (!name) return null;
  const spotify = np.source === 'SPOTIFY';
  const { title, sub } = stationLines(np, name, spotify);
  return {
    key: np.tuneInId || name,
    name,
    title,
    sub,
    art: np.art || '',
    tuneInId: np.tuneInId || null,
    spotify,
    skips: canSkip(np),
    source: np.source || 'TUNEIN',
    paused: np.playStatus === 'PAUSE_STATE',
  };
}

// On: playing something, or on an external input.
export const roomOn = (room: { now: NowPlaying | null }) =>
  !!stationOf(room.now) || sourceOf(room.now) !== 'wifi';

// A group's display name: its speakers, leader first ("Kitchen + Office").
export const groupName = (lead: { name: string }, members: { name: string }[]) =>
  [lead, ...members].map((r) => r.name).join(' + ');
