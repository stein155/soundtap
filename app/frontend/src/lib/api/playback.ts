// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { clean } from '../text';
import { makeT } from '../i18n';
import { LIKED_CONTEXT } from '../spotify';
import { getJSON, send } from './http';
import type { NowPlaying, Preset } from '../types';

// Liked Songs has no name of its own: Spotify, and presets saved in another language, name
// it in theirs. It is named in the page's language (useLanguage keeps it on <html lang>).
export const likedName = (uri: unknown, name: string): string =>
  uri === LIKED_CONTEXT ? makeT(document.documentElement.lang)('spotifyLiked') : name;

// Now playing: soundtapd reports {source,track,artist,station,stationId,art,playStatus};
// normalised to the shape the UI reads (standby / stationName / tuneInId). Shared by the
// /api/now fetch and the /api/events push stream (see events.ts).
export function normalizeNowPlaying(np: unknown): NowPlaying | null {
  if (!np || typeof np !== 'object') return null;
  const raw = np as Record<string, unknown>;
  const source = clean(raw.source);
  if (!source || source === 'STANDBY' || source === 'INVALID_SOURCE') {
    return { standby: true };
  }
  return {
    standby: false,
    source,
    stationName: likedName(raw.stationId, clean(raw.station) || clean(raw.track)),
    track: clean(raw.track),
    artist: clean(raw.artist),
    album: clean(raw.album),
    playStatus: clean(raw.playStatus),
    art: clean(raw.art),
    tuneInId: clean(raw.stationId) || null,
    canSkip: raw.canSkip === true,
    failed: raw.failed === true,
  };
}

export async function getNowPlaying(speaker?: string): Promise<NowPlaying | null> {
  try {
    return normalizeNowPlaying(await getJSON('/api/now', speaker));
  } catch {
    return null;
  }
}

export async function getVolume(speaker?: string): Promise<number | null> {
  try {
    const v = await getJSON<{ volume?: number; muted?: boolean }>('/api/volume', speaker);
    // The speaker keeps the level while muted; the slider shows the silence.
    return typeof v.volume === 'number' ? (v.muted ? 0 : v.volume) : null;
  } catch {
    return null;
  }
}

// The level a muted speaker returns to; null when it is not muted. A soundbar's remote can mute
// it without this page knowing the level it had.
export async function getMutedLevel(speaker?: string): Promise<number | null> {
  try {
    const v = await getJSON<{ volume?: number; muted?: boolean }>('/api/volume', speaker);
    return v.muted && typeof v.volume === 'number' ? v.volume : null;
  } catch {
    return null;
  }
}

// `group` marks a change made to a whole group: with the household's "volume together"
// setting on, the leader then passes the same step on to the speakers following it. A
// change to one speaker's own slider never does.
export async function setVolume(value: number, speaker?: string, { group = false } = {}) {
  return send('/api/volume', 'POST', group ? { volume: value, group } : { volume: value }, speaker);
}

// Select (play) a TuneIn station by id.
export async function selectStation(tuneInId: string, name: string, speaker?: string) {
  return send('/api/play', 'POST', { stationId: tuneInId, name: clean(name) }, speaker);
}

// Play a preset slot: the speaker plays the station saved on it.
// Falls back to a direct station select if the slot has a known station id but
// the server-side play fails (e.g. native preset only known to the box).
export async function playPreset(
  slot: number,
  _standby: boolean,
  preset?: Preset | null,
  speaker?: string
) {
  try {
    return await send(`/api/play/${slot}`, 'POST', undefined, speaker);
  } catch (e) {
    if (preset?.tuneInId) return selectStation(preset.tuneInId, preset.name, speaker);
    throw e;
  }
}

export async function stopPlayback(speaker?: string) {
  return send('/api/stop', 'POST', undefined, speaker);
}

// Power off is standby (what Stop does, plus it cancels a sleep timer); power on wakes
// the speaker without choosing anything to play.
export async function setPower(on: boolean, speaker?: string) {
  return send('/api/power', 'POST', { on }, speaker);
}

// Sleep timer, kept on the speaker so it still fires with the page closed.
export interface SleepState {
  minutes: number;
  endsAt: string; // RFC 3339, '' when no timer runs
}

export async function getSleep(speaker?: string): Promise<SleepState | null> {
  try {
    return await getJSON<SleepState>('/api/sleep', speaker);
  } catch {
    return null;
  }
}

export async function setSleep(minutes: number, speaker?: string) {
  return send('/api/sleep', 'POST', { minutes }, speaker) as Promise<SleepState | null>;
}

// Pause and resume exist for Spotify, whose session can hold its place; a radio stream
// has nothing to resume to, so radio only stops.
export async function setPaused(paused: boolean, speaker?: string) {
  return send(paused ? '/api/spotify/pause' : '/api/spotify/resume', 'POST', {}, speaker);
}

export type SpotifyRepeat = 'off' | 'context' | 'track';

export interface SpotifyModes {
  shuffle: boolean;
  repeat: SpotifyRepeat;
}

// Shuffle and repeat as the Spotify session has them, whoever set them.
export async function spotifyModes(speaker?: string): Promise<SpotifyModes> {
  const p = await getJSON<Partial<SpotifyModes>>('/api/spotify/player', speaker);
  return { shuffle: p.shuffle ?? false, repeat: p.repeat ?? 'off' };
}

export async function setSpotifyShuffle(on: boolean, speaker?: string) {
  return send('/api/spotify/shuffle', 'POST', { on }, speaker);
}

export async function setSpotifyRepeat(mode: SpotifyRepeat, speaker?: string) {
  return send('/api/spotify/repeat', 'POST', { mode }, speaker);
}

export interface SpotifyLiked {
  available: boolean; // false for what cannot be liked: an episode, or nothing yet
  liked: boolean;
}

export async function spotifyLiked(speaker?: string): Promise<SpotifyLiked> {
  return getJSON<SpotifyLiked>('/api/spotify/liked', speaker);
}

export async function setSpotifyLiked(on: boolean, speaker?: string) {
  return send('/api/spotify/liked', 'POST', { on }, speaker);
}

export interface TrackPosition {
  positionMs: number;
  durationMs: number; // zero: a stream without an end
  seekable: boolean;
}

export async function trackPosition(speaker?: string): Promise<TrackPosition> {
  return getJSON<TrackPosition>('/api/position', speaker);
}

// Moves the playing track: Spotify, or a track a UPnP controller sent.
export async function seekTrack(positionMs: number, speaker?: string) {
  return send('/api/seek', 'POST', { positionMs }, speaker);
}

// Next and previous as the speaker's own keys send them: a media server's folder moves on.
export async function skipKey(dir: 'previous' | 'next', speaker?: string) {
  const key = dir === 'next' ? 'NEXT_TRACK' : 'PREV_TRACK';
  return send('/api/key', 'POST', { key }, speaker);
}

// Spotify's own previous and next: the session on the speaker moves, not this page.
export async function skipSpotify(dir: 'previous' | 'next', speaker?: string) {
  return send(`/api/spotify/${dir}`, 'POST', {}, speaker);
}
