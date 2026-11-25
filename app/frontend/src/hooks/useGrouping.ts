// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef } from 'react';
import { followSpeaker, inviteSpeaker } from '../lib/api';
import { speakerArg, type Room } from './useRooms';
import type { ReportFailure } from './useErrorReporter';
import type { SelectedRoom } from './useSelectedRoom';

export interface Grouping {
  link: (room: Room) => Promise<void>;
  unlink: (room: Room) => Promise<void>;
  linkMany: (targets: Room[]) => Promise<void>;
  unlinkMany: (targets: Room[]) => Promise<void>;
}

// Multiroom groups around the selected room. A speaker joins when the leader invites
// it (the leader knows its own address); it leaves by being told to stop following.
// The new grouping is shown at once; if a speaker refuses, that is said and the real
// grouping is read back from the speakers.
export function useGrouping(
  { rooms, lead, speaker, roomsState }: SelectedRoom,
  failed: ReportFailure
): Grouping {
  const without = (ids: string[]) =>
    Object.fromEntries(Object.entries(roomsState.leaders).filter(([id]) => !ids.includes(id)));
  const undo = (room: Room) => (err?: unknown) => {
    failed(room.name)(err); // the error tells a refusal (4xx) from a speaker that is silent
    void roomsState.refresh();
  };
  // The last thing asked of each room: a link still letting followers go does not invite a
  // room that has since been ungrouped.
  const asked = useRef(new Map<string, object>());
  const ask = (targets: Room[]) => {
    const request = {};
    for (const room of targets) asked.current.set(room.id, request);
    return request;
  };
  // A room's unlink still on its way: a link waits for it, or the late "stop following"
  // would land after the invite and take the room out again.
  const leaving = useRef(new Map<string, Promise<unknown>>());

  // A speaker that led a group of its own lets its followers go first, so nobody is
  // left following a follower. Several at once change the grouping in one step: one
  // after the other would each start from the grouping as it was.
  const linkMany = async (targets: Room[]) => {
    if (!lead || !targets.length) return;
    const request = ask(targets);
    const followers = rooms.filter((r) => targets.some((t) => t.id === r.linkedTo));
    roomsState.regroup({
      ...without(followers.map((f) => f.id)),
      ...Object.fromEntries(targets.map((r) => [r.id, lead.id])),
    });
    await Promise.all(followers.map(async (f) => followSpeaker('', speakerArg(f)).catch(undo(f))));
    await Promise.all(targets.map(async (r) => leaving.current.get(r.id)));
    const still = targets.filter((r) => asked.current.get(r.id) === request);
    await Promise.all(still.map(async (r) => inviteSpeaker(r.ip, speaker).catch(undo(r))));
  };

  const unlinkMany = async (targets: Room[]) => {
    ask(targets);
    roomsState.regroup(without(targets.map((r) => r.id)));
    await Promise.all(
      targets.map(async (r) => {
        const left = followSpeaker('', speakerArg(r)).catch(undo(r));
        leaving.current.set(r.id, left);
        return left;
      })
    );
  };

  const link = async (room: Room) => linkMany([room]);
  const unlink = async (room: Room) => unlinkMany([room]);

  return { link, unlink, linkMany, unlinkMany };
}
