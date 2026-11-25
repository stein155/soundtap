// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { getMutedLevel } from '../lib/api/playback';
import { speakerArg, type Room } from './useRooms';

// Unmuting with nothing to return to (the page opened on a room at volume 0) lands here.
const UNMUTE_FALLBACK = 25;

// The speaker icon of a volume slider: mutes a room, and the next tap puts it back where
// it was. The level to return to is kept per room: another room's level must not leak
// into this one after a room switch. A room muted elsewhere, by a soundbar's remote,
// knows its own level, and that one goes first: this page's may be older. `volumeStamp`
// changes with every volume change of a room: a slider moved while that level was being
// read wins, and the late level is dropped.
export function useMute(
  setVolume: (room: Room, volume: number) => void,
  volumeStamp: (id: string) => number = () => 0
): (room: Room) => Promise<void> {
  const [lastAudible, setLastAudible] = useState<Record<string, number>>({});
  return async (room: Room) => {
    if (room.volume === 0) {
      const stamp = volumeStamp(room.id);
      const level = await getMutedLevel(speakerArg(room));
      if (volumeStamp(room.id) !== stamp) return;
      // No level kept (or a kept 0): the last level heard here, else a quiet default.
      const back = level !== null && level !== 0 ? level : undefined;
      setVolume(room, back ?? lastAudible[room.id] ?? UNMUTE_FALLBACK);
      return;
    }
    setLastAudible((levels) => ({ ...levels, [room.id]: room.volume }));
    setVolume(room, 0);
  };
}
