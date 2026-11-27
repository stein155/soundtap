// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef } from 'react';
import { setVolume } from '../lib/api';
import { createLatestSender } from '../lib/latestSender';
import type { ReportFailure } from './useErrorReporter';
import { speakerArg, type Room } from './useRooms';
import type { SelectedRoom } from './useSelectedRoom';

const clampVolume = (v: number) => Math.max(0, Math.min(100, Math.round(v)));

// How long after its last move a group drag still measures from where it started.
const DRAG_MS = 1500;

export interface RoomVolume {
  setRoomVolume: (room: Room, volume: number) => void;
  showRoomVolume: (room: Room, volume: number) => void;
  setGroupVolume: (volume: number) => void;
  volumeStamp: (id: string) => number;
}

// Volume for any room, and for the selected group as a whole. The selected room goes
// through useSpeaker (it holds the slider steady against stale reads); every other room
// gets its own coalescing sender, so a drag does not flood it through the proxy.
//
// `syncVolume` is the household setting under which the leader passes a group change on
// to its followers itself; the group slider then only moves the leader, marked as a
// group change, and the followers are shown moving with it. The leader is sent its level
// unclamped: at 0 or 100 it stays put, but the followers still take the whole step.
export function useRoomVolume(
  { lead, members, live, roomsState, rooms }: SelectedRoom,
  syncVolume: boolean,
  failed?: ReportFailure
): RoomVolume {
  const senders = useRef(new Map<string, (volume: number) => void>());
  // Every member's level and the group mean when the current drag began, and which
  // group that was: a drag never carries over to another group.
  const drag = useRef<{
    at: number;
    group: string;
    mean: number;
    base: Map<string, number>;
  } | null>(null);
  // Counts every volume change per room, so a reply that was asked for before a newer
  // change (the unmute level) can tell it is stale.
  const changes = useRef(new Map<string, number>());
  const touch = (id: string) => changes.current.set(id, (changes.current.get(id) ?? 0) + 1);
  const volumeStamp = (id: string) => changes.current.get(id) ?? 0;
  // A change from elsewhere (a remote, another phone) counts as well: `rooms` holds
  // what the feed and the polls last reported, the selected room's live level included.
  const heard = useRef(new Map<string, number>());
  useEffect(() => {
    for (const { id, volume } of rooms) {
      const before = heard.current.get(id);
      if (before !== undefined && before !== volume) touch(id);
      heard.current.set(id, volume);
    }
  });
  const senderFor = (speaker: string, name: string) => {
    let send = senders.current.get(speaker);
    if (!send) {
      send = createLatestSender(
        async (volume: number) => setVolume(volume, speaker),
        () => failed?.(name)()
      );
      senders.current.set(speaker, send);
    }
    return send;
  };

  const setRoomVolume = (room: Room, volume: number) => {
    touch(room.id);
    if (room.id === lead?.id) {
      live.changeVolume(volume);
      return;
    }
    roomsState.patch(room.id, { volume });
    senderFor(speakerArg(room), room.name)(volume);
  };

  // Shows a room at a level it reached without a volume write (a mute), sending nothing.
  const showRoomVolume = (room: Room, volume: number) => {
    touch(room.id);
    if (room.id === lead?.id) {
      live.showVolume(volume);
      return;
    }
    roomsState.patch(room.id, { volume });
  };

  // Moves every speaker of the group by the same step, so their balance stays. The step
  // is measured from the levels at the start of the drag, so a member clamped at an end
  // of the slider comes back to its own level on the way back.
  const setGroupVolume = (volume: number) => {
    if (!lead) return;
    const group = [lead, ...members];
    const now = Date.now();
    const key = group.map((r) => r.id).join(' ');
    if (drag.current?.group !== key || now - drag.current.at > DRAG_MS) {
      drag.current = {
        at: now,
        group: key,
        mean: group.reduce((sum, r) => sum + r.volume, 0) / group.length,
        base: new Map(group.map((r) => [r.id, r.volume])),
      };
    }
    const { mean, base } = drag.current;
    drag.current.at = now;
    const step = volume - mean;
    const from = (r: Room) => base.get(r.id) ?? r.volume;
    if (syncVolume) {
      for (const room of group) touch(room.id);
      live.changeVolume(Math.round(from(lead) + step), { group: true });
      for (const member of members)
        roomsState.patch(member.id, { volume: clampVolume(from(member) + step) });
      return;
    }
    for (const room of group) setRoomVolume(room, clampVolume(from(room) + step));
  };

  return { setRoomVolume, showRoomVolume, setGroupVolume, volumeStamp };
}
