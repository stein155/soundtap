// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { getMutedLevel, setMuted } from '../lib/api/playback';
import type { ReportFailure } from './useErrorReporter';
import type { RoomVolume } from './useRoomVolume';
import { speakerArg, type Room } from './useRooms';

// The speaker icon of a volume slider: mutes a room, and the next tap puts it back where
// it was. It is the speaker's own mute, the one its remote's mute key toggles: the speaker
// keeps the level, so an unmute returns to it after a reload, from another phone and for a
// room muted by its remote. A muted room shows 0; a room at 0 that is not muted has
// nothing to return to, and the tap leaves it as it is. `volumeStamp` changes with every
// volume change of a room: a slider moved while the kept level was being read wins, and
// the late level is dropped.
export function useMute(
  { showRoomVolume, volumeStamp }: Pick<RoomVolume, 'showRoomVolume' | 'volumeStamp'>,
  failed?: ReportFailure
): (room: Room) => Promise<void> {
  return async (room: Room) => {
    const speaker = speakerArg(room);
    if (room.volume === 0) {
      const stamp = volumeStamp(room.id);
      const level = await getMutedLevel(speaker);
      if (level === null || volumeStamp(room.id) !== stamp) return;
      showRoomVolume(room, level);
      try {
        await setMuted(false, speaker);
      } catch (err) {
        showRoomVolume(room, 0);
        failed?.(room.name)(err);
      }
      return;
    }
    showRoomVolume(room, 0);
    try {
      await setMuted(true, speaker);
    } catch (err) {
      showRoomVolume(room, room.volume);
      failed?.(room.name)(err);
    }
  };
}
