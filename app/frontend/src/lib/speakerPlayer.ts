// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { sameStation } from './station';
import type { NowPlaying, Player, PlayerStation, PlayerStatus, Preset } from './types';

// The pure side of useSpeaker: what the polled / pushed state means, with no React in it.

export interface Pending {
  name: string;
  tuneInId: string | null;
  logo: string;
  since: number;
  prevName: string;
}
export interface Picked {
  name: string;
  logo: string;
  tuneInId: string | null;
}

// Hard cap on holding a pending target: give up, show reality (likely an error).
export const PENDING_MAX_MS = 15000;
// STOP_STATE counts as giving up only this long after a tap: a dead stream dips into it
// while it retries, 2 s in, and stays there from 4 s.
const GIVE_UP_MS = 4000;
const STOP_HOLD_MS = 8000; // after Stop: ignore the box still reporting PLAY_STATE this long
const POLL_ACTIVE_MS = 2500; // poll faster while starting / buffering (SSE down)
export const POLL_IDLE_MS = 8000; // settle back to a calm poll when playing / idle (SSE down)
const POLL_SAFETY_MS = 30000; // while SSE is live, only poll occasionally as a backstop

// After Stop the box still reports the old station as PLAY_STATE for a moment; this says
// whether now-playing is still to be ignored (`holding`) and whether the hold is over
// (`release`).
// The hold must survive readings that are neither "playing" nor "stopped" — a stopping box
// passes through BUFFERING_STATE, and treating that as "not playing" used to drop the hold
// on the very first tick, so the lagging PLAY_STATE of the old station arrived unguarded
// and flickered back on screen. Hold until the box confirms it has actually stopped
// (standby/STOP_STATE) or the window expires.
export function stopHold(
  hold: number,
  np: NowPlaying | null | undefined,
  now: number
): { holding: boolean; release: boolean } {
  const expired = hold !== 0 && now - hold >= STOP_HOLD_MS;
  const settledStopped = np != null && (np.standby || np.playStatus === 'STOP_STATE');
  return {
    holding: hold !== 0 && !expired && !settledStopped,
    release: hold !== 0 && (expired || settledStopped),
  };
}

// Whether a reading ends the pending target (`clear`), and whether it ends it because the
// box gave up on the stream (`failed`).
export function pendingOutcome(
  np: NowPlaying,
  p: Pending,
  now: number
): { failed: boolean; clear: boolean } {
  const elapsed = now - p.since;
  const playing = !np.standby && np.playStatus === 'PLAY_STATE';
  // Clear the hold only once the box actually plays our station, OR has
  // moved off the previous one onto something new — never while it still
  // reports the station we switched away from (that brief PLAY_STATE of the
  // old station during a switch is exactly what used to flicker through).
  const onOurs = sameStation(np.stationName, p.name);
  const stillPrev = sameStation(np.stationName, p.prevName);
  const settled = playing && (onOurs || !stillPrev);
  // A stream the box gave up on reports STOP_STATE. Drop the optimistic hold
  // then rather than spinning out the full PENDING_MAX_MS.
  const stopped = !np.standby && np.playStatus === 'STOP_STATE';
  const failed = stopped && elapsed >= GIVE_UP_MS;
  return { failed, clear: settled || failed || elapsed > PENDING_MAX_MS };
}

const idle = (): Player => ({ status: 'idle', station: null });

// While a tap is pending: its station, and how far along the box is with it.
function pendingPlayer(pending: Pending, nowPlaying: NowPlaying | null): Player {
  let status: PlayerStatus = 'starting';
  if (nowPlaying && !nowPlaying.standby) {
    if (nowPlaying.playStatus === 'BUFFERING_STATE') status = 'buffering';
    else if (
      nowPlaying.playStatus === 'PLAY_STATE' &&
      sameStation(nowPlaying.stationName, pending.name)
    )
      status = 'playing';
    // else keep 'starting': box is waking or still playing the previous station.
  }
  return {
    status,
    station: {
      name: pending.name,
      art: pending.logo,
      tuneInId: pending.tuneInId,
      track: '',
      artist: '',
    },
  };
}

// The player status for a settled (non-pending) play state.
function settledStatus(ps: string | undefined): PlayerStatus {
  if (ps === 'PLAY_STATE') return 'playing';
  if (ps === 'STOP_STATE') return 'idle';
  // A pause is settled, not a connection in progress.
  if (ps === 'PAUSE_STATE') return 'paused';
  return 'buffering'; // BUFFERING_STATE or a transient non-standby state
}

// Only trust the box's own name when it isn't just the injected track (that's
// the duplicate we're avoiding). If it equals the track we have no real station
// name, so leave it blank and let the player fall back without repeating it.
const boxNameOf = (np: NowPlaying) =>
  np.stationName && np.stationName !== np.track ? np.stationName : '';

// The station the app picked, when this now-playing is that one.
function knownPick(np: NowPlaying, picked: Picked | null): Picked | null {
  if (!picked) return null;
  const sameId = !!np.tuneInId && picked.tuneInId === np.tuneInId;
  return sameId || sameStation(np.stationName, picked.name) ? picked : null;
}

// The station's name: what the app picked, a matching preset, or the box's own.
const nameOf = (np: NowPlaying, known: Picked | null, presetMatch: Preset | undefined) =>
  known?.name || presetMatch?.name || boxNameOf(np) || '';

// The station line from what the box reports, what the app picked and a matching preset.
function stationOf(
  np: NowPlaying,
  known: Picked | null,
  presetMatch: Preset | undefined
): PlayerStation {
  return {
    name: nameOf(np, known, presetMatch),
    art: np.art || known?.logo || presetMatch?.logo || '',
    tuneInId: np.tuneInId || known?.tuneInId || null,
    track: np.track || '',
    artist: np.artist || '',
    source: np.source,
  };
}

// The speaker's reported "station name" is often the live TRACK, not the
// station: SoundTap injects the rolling track onto the box's own display, so the
// firmware echoes it back in now-playing. Recover the real station name (and
// logo) from what the app actually picked, or from a preset with the same
// TuneIn id — matching on the id, which the injection leaves intact, rather
// than on the name. This keeps the station on line one and the track on line
// two, instead of the track showing on both. The box's own name is only a last
// resort (e.g. playback started outside the app, before any track was injected).
function playingPlayer(
  np: NowPlaying,
  status: PlayerStatus,
  presets: (Preset | null)[],
  lastPicked: Picked | null
): Player {
  const npId = np.tuneInId;
  const known = knownPick(np, lastPicked);
  const presetMatch = npId
    ? presets.find((p): p is Preset => !!p && p.tuneInId === npId)
    : undefined;
  return { status, station: stationOf(np, known, presetMatch) };
}

// Derived player state the components render. status: idle | starting | buffering | playing.
export function derivePlayer(
  pending: Pending | null,
  nowPlaying: NowPlaying | null,
  presets: (Preset | null)[],
  lastPicked: Picked | null
): Player {
  if (pending) return pendingPlayer(pending, nowPlaying);
  if (!nowPlaying || nowPlaying.standby) return idle();
  const status = settledStatus(nowPlaying.playStatus);
  return status === 'idle' ? idle() : playingPlayer(nowPlaying, status, presets, lastPicked);
}

// How long until the next poll tick: a slow backstop while the SSE stream is live, quick
// while starting / buffering or after a miss, calm otherwise.
export function pollDelay(connected: boolean, status: PlayerStatus, misses: number): number {
  if (connected) return POLL_SAFETY_MS;
  const active = status === 'starting' || status === 'buffering' || misses > 0;
  return active ? POLL_ACTIVE_MS : POLL_IDLE_MS;
}
