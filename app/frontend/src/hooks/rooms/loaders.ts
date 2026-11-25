// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback } from 'react';
import { getDevices, getMultiroom, getNowPlaying, getSettings, getVolume } from '../../lib/api';
import type { SpeakerDevice } from '../../lib/types';
import {
  speakerArg,
  type HouseholdRef,
  type Leaders,
  type RoomSetters,
  type RoomStates,
} from './household';

// Where a groups read comes from. While the stream is open a speaker it reported on is not
// asked. `ask` reads every snapserver anyway, to confirm a change made here before the push
// about it arrives; `push` recomputes from what the stream said and asks nobody; a speaker
// it said nothing about keeps its last group until the next poll.
export type GroupsFrom = 'poll' | 'ask' | 'push';

// Whether two follower -> leader maps agree on the speakers that are online.
function sameGroups(a: Leaders, b: Leaders, known: Set<string>) {
  return [...known].every((id) => (a[id] ?? null) === (b[id] ?? null));
}

// The speaker that served the page when it has no discovery registry (an older
// soundtapd): it still has to show up as one room, named as its settings say.
async function selfWithoutRegistry(): Promise<SpeakerDevice> {
  const settings = await getSettings('');
  return {
    id: 'self',
    name: settings?.name || '',
    ip: '',
    model: settings?.model || '',
    online: true,
    self: true,
  };
}

// Every speaker in the household: the one you are standing next to first, the rest by
// name, so a card never jumps when discovery answers in a different order.
// `known` is the last list: a registry read that fails (a timeout) keeps it, so the other
// rooms do not vanish for a poll. Only with nothing known yet is the speaker taken for an
// older one without a registry, the only room.
async function discoverSpeakers(known: SpeakerDevice[]): Promise<SpeakerDevice[] | null> {
  const list = await getDevices().catch(() => null);
  if (!list && known.length) return null;
  const all =
    list?.some((d) => d.self) === true ? list : [await selfWithoutRegistry(), ...(list ?? [])];
  return all.sort((a, b) => Number(b.self) - Number(a.self) || a.name.localeCompare(b.name));
}

// Each online speaker's now-playing and volume; null where a read failed.
async function readState(list: SpeakerDevice[]) {
  return Promise.all(
    list.map(async (d) => {
      const speaker = speakerArg(d);
      const [now, volume] = await Promise.all([getNowPlaying(speaker), getVolume(speaker)]);
      return [d.id, now, volume] as const;
    })
  );
}

// Who follows whom among `list`, from each leader's snapserver or, where given, from what
// the stream said (`pushed`). `unread` are the speakers whose read failed.
async function readGroups(
  list: SpeakerDevice[],
  from: GroupsFrom,
  pushed: Record<string, string[]> | null
) {
  const known = new Set(list.map((d) => d.id));
  const next: Leaders = {};
  const unread = new Set<string>();
  await Promise.all(
    list.map(async (d) => {
      // null: the read failed. That says nothing about who follows this speaker, so
      // what was last confirmed for it stays.
      const said = pushed ? pushed[d.self ? '' : d.id] : null;
      const followers =
        said ??
        (from === 'push' ? null : await getMultiroom(speakerArg(d)))
          ?.filter((r) => r.connected)
          .map((r) => r.deviceId);
      if (!followers) {
        unread.add(d.id);
        return;
      }
      for (const f of followers) if (f && f !== d.id && known.has(f)) next[f] = d.id;
    })
  );
  // A speaker that follows cannot also lead; if two answers disagree (a follower that
  // just moved), the one that is itself followed keeps its own stream.
  // Decided in key order: a leader dropped earlier no longer counts as followed.
  const dropped = new Set<string>();
  for (const [id, leader] of Object.entries(next))
    if (next[leader] && !dropped.has(leader)) dropped.add(id);
  const groups: Leaders = Object.fromEntries(
    Object.entries(next).filter(([id]) => !dropped.has(id))
  );
  return { next: groups, unread, known };
}

// The read groups, with what was last confirmed for a speaker whose read failed.
function withUnread(prev: Leaders, read: Awaited<ReturnType<typeof readGroups>>): Leaders {
  const out = { ...read.next };
  for (const [f, l] of Object.entries(prev))
    if (read.unread.has(l) && read.known.has(f) && !(f in out)) out[f] = l;
  return out;
}

export interface RoomLoaders {
  loadDevices: () => Promise<void>;
  loadState: () => Promise<void>;
  loadGroups: (from?: GroupsFrom) => Promise<void>;
}

// The three reads behind the rooms: the device list, each room's state, and the groups.
export function useRoomLoaders(householdRef: HouseholdRef, set: RoomSetters): RoomLoaders {
  const { setDevices, setLoaded, setState, setLeaders } = set;

  const loadDevices = useCallback(async () => {
    const seq = ++householdRef.current.devicesSeq;
    const want = householdRef.current.devicesWant;
    const list = await discoverSpeakers(householdRef.current.devices);
    // A failed read keeps the last list and the revision unconfirmed, so the poll retries.
    if (!list) return;
    if (seq !== householdRef.current.devicesSeq) return; // a newer read is on its way
    // Now, not after the render: refresh() polls state right after this resolves,
    // and with the old list the first poll asked nobody.
    householdRef.current.devices = list;
    householdRef.current.devicesRev = want;
    setDevices(list);
    setLoaded(true);
  }, [householdRef, setDevices, setLoaded]);

  const loadState = useCallback(async () => {
    const seq = ++householdRef.current.stateSeq;
    const { live, pushed, skip } = householdRef.current;
    const list = householdRef.current.devices.filter(
      (d) => d.online && d.id !== skip && !(live && pushed.has(d.id))
    );
    if (!list.length) return;
    const read = await readState(list);
    if (seq !== householdRef.current.stateSeq) return;
    setState((prev: RoomStates) => {
      const next = { ...prev };
      // A reading that failed keeps the last known one rather than showing 0 or idle.
      for (const [id, now, volume] of read)
        next[id] = { now: now ?? prev[id]?.now ?? null, volume: volume ?? prev[id]?.volume ?? 0 };
      return next;
    });
  }, [householdRef, setState]);

  const loadGroups = useCallback(
    async (from: GroupsFrom = 'poll') => {
      const seq = ++householdRef.current.groupsSeq;
      // A lone speaker can lead nobody: skip the round trip.
      const online = householdRef.current.devices.filter((d) => d.online);
      const list = online.length < 2 ? [] : online;
      const pushed =
        householdRef.current.live && from !== 'ask' ? householdRef.current.pushedFollowers : null;
      const read = await readGroups(list, from, pushed);
      if (seq !== householdRef.current.groupsSeq) return;
      setLeaders((prev) => {
        const out = withUnread(prev, read);
        const want = householdRef.current.wanted;
        if (!want) return out;
        if (Date.now() >= want.until) {
          householdRef.current.wanted = null;
          return out;
        }
        return sameGroups(out, want.leaders, read.known) ? out : want.leaders;
      });
    },
    [householdRef, setLeaders]
  );

  return { loadDevices, loadState, loadGroups };
}
