// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { setPaused, setPower, skipKey, skipSpotify, stopPlayback } from '../../lib/api';
import { stationOf } from '../../lib/rooms';
import type { PlaybackContext } from './context';

const STOP_SETTLE_MS = 1000;

export interface Transports {
  stop: () => Promise<void>;
  transport: () => Promise<void>;
  skip: (dir: 'previous' | 'next') => Promise<void>;
  power: (on: boolean) => Promise<void>;
}

// Stop, pause, skip and power for the selected group.
export function transports(c: PlaybackContext): Transports {
  const { live, speaker, lead, roomsState, leadFailed } = c;

  const stop = async () => {
    live.stopOptimistic();
    const ok = await stopPlayback(speaker).then(
      () => true,
      (err: unknown) => {
        leadFailed(err);
        return false;
      }
    );
    if (ok) {
      setTimeout(live.refreshNowPlaying, STOP_SETTLE_MS);
      return;
    }
    // Nothing stopped: show what the speaker really plays, not the idle we promised.
    live.releaseStopHold();
    void live.refreshNowPlaying();
  };

  // The selected group's transport button: pause/resume Spotify, otherwise stop.
  const transport = async () => {
    const station = stationOf(lead?.now ?? null);
    if (station?.spotify !== true) {
      await stop();
      return;
    }
    // The glyph follows the room's state, so flip it at once (the stop below does the
    // same) and ignore a repeat tap until the request settles.
    const key = `${speaker}|transport`;
    const was = lead?.now;
    if (!lead || !was || !c.begin(key)) return;
    roomsState.patch(lead.id, {
      now: { ...was, playStatus: station.paused ? 'PLAY_STATE' : 'PAUSE_STATE' },
    });
    await setPaused(!station.paused, speaker).catch((err: unknown) => {
      roomsState.patch(lead.id, { now: was });
      leadFailed(err);
    });
    c.settle(key);
  };

  const skip = async (dir: 'previous' | 'next') => {
    const send = stationOf(lead?.now ?? null)?.spotify === true ? skipSpotify : skipKey;
    await send(dir, speaker).catch(leadFailed);
    void live.refreshNowPlaying();
  };

  const power = async (on: boolean) => {
    if (!on) live.stopOptimistic();
    await setPower(on, speaker).catch(leadFailed);
    live.nudge();
  };

  return { stop, transport, skip, power };
}
