// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { playPreset, selectStation } from '../../lib/api';
import { spotifyPlay } from '../../lib/spotify';
import type { RecentItem } from '../../lib/recent';
import type { PlaybackContext } from './context';

export interface Playable {
  tuneInId: string;
  name: string;
  logo?: string;
}

export interface Starters {
  playSlot: (slot: number) => Promise<void>;
  playStation: (station: Playable) => Promise<void>;
  playRecent: (item: RecentItem) => Promise<void>;
}

// Starting something on the selected room: shown as playing at once, a repeat tap ignored
// until the request has settled.
export function starters({
  live,
  speaker,
  lead,
  failed,
  begin,
  settle,
}: PlaybackContext): Starters {
  // A failed request withdraws the target it set, not a newer tap's.
  const playFailed = (mine: ReturnType<typeof live.playOptimistic>) => (err?: unknown) => {
    live.cancelPending(mine);
    failed(lead?.name || '', 'actionFailed', true)(err);
  };

  const playSlot = async (slot: number) => {
    const preset = live.presets[slot - 1];
    const key = `${speaker}|preset:${slot}`;
    if (!preset || !begin(key)) return;
    const wasPlaying = live.player.status === 'playing';
    const mine = live.playOptimistic(preset);
    live.nudge();
    await playPreset(slot, !wasPlaying, preset, speaker).catch(playFailed(mine));
    settle(key);
  };

  const playStation = async (station: Playable) => {
    const key = `${speaker}|station:${station.tuneInId}`;
    if (!begin(key)) return;
    const mine = live.playOptimistic(station);
    live.nudge();
    await selectStation(station.tuneInId, station.name, speaker).catch(playFailed(mine));
    settle(key);
  };

  const playRecent = async (item: RecentItem) => {
    if (item.tuneInId) {
      await playStation({ tuneInId: item.tuneInId, name: item.name, logo: item.art });
      return;
    }
    if (item.spotifyUri) {
      // Like a station: shown as starting at once, and a repeat is ignored until settled.
      const key = `${speaker}|spotify:${item.spotifyUri}`;
      if (!begin(key)) return;
      // A track plays on its own; a list (or Liked Songs) is the context.
      const track = item.spotifyUri.startsWith('spotify:track:');
      const uri = track ? item.spotifyUri : undefined;
      const ctx = track ? undefined : item.spotifyUri;
      const mine = live.playOptimistic({ name: item.name, logo: item.art });
      live.nudge();
      await spotifyPlay(
        uri,
        ctx,
        { uri: item.spotifyUri, name: item.name, artwork: item.art },
        speaker
      ).catch(playFailed(mine));
      settle(key);
    }
  };

  return { playSlot, playStation, playRecent };
}
