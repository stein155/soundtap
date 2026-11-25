// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, type Dispatch, type RefObject, type SetStateAction } from 'react';
import type { NowPlaying, SpeakerDevice } from '../../lib/types';

// The API argument that addresses a room: the speaker that served the page answers unprefixed, any
// other one through that speaker's /api/devices/{id}/ proxy (see lib/base.ts).
export const speakerArg = (room: { id: string; self: boolean }): string =>
  room.self ? '' : room.id;

export type RoomStates = Partial<Record<string, { now: NowPlaying | null; volume: number }>>;
// follower id -> leader id
export type Leaders = Record<string, string>;

// What the pollers and the household stream share. They run on timers and callbacks, so
// they read it through one ref rather than from a render.
export interface Household {
  // The latest device list, and the room the page already follows live.
  devices: SpeakerDevice[];
  skip: string | null;
  // Polls overlap (a slow speaker takes up to the request deadline, longer than a poll):
  // only the newest one may land, or a late old answer overwrites a fresh one.
  stateSeq: number;
  groupsSeq: number;
  devicesSeq: number;
  // The grouping this page just asked for, shown until the hold runs out. A read in between
  // can still have the old group, even after one that had the new one: a push from before
  // the change arrives late, and would drop the room again.
  wanted: { leaders: Leaders; until: number } | null;
  timers: Set<ReturnType<typeof setTimeout>>;
  // While the household stream is open it carries every room's state and the poll stays
  // quiet; it takes over again whenever the stream drops.
  live: boolean;
  // The rooms it has reported on since it opened: a peer whose own event feed fails is
  // never in here, and keeps being polled.
  pushed: Set<string>;
  // What the stream last said: who follows each speaker (keyed by id, '' for the one that
  // served the page) and the revision of its device list.
  pushedFollowers: Record<string, string[]>;
  // `devicesWant` is the newest revision announced, `devicesRev` the one a read confirmed.
  devicesRev: string | undefined;
  devicesWant: string | undefined;
}

export type HouseholdRef = RefObject<Household>;

export interface RoomSetters {
  setDevices: Dispatch<SetStateAction<SpeakerDevice[]>>;
  setLoaded: Dispatch<SetStateAction<boolean>>;
  setState: Dispatch<SetStateAction<RoomStates>>;
  setLeaders: Dispatch<SetStateAction<Leaders>>;
}

// The shared record, kept up to date with the device list and the skipped room.
export function useHousehold(devices: SpeakerDevice[], skip: string | null): HouseholdRef {
  const householdRef = useRef<Household>({
    devices,
    skip,
    stateSeq: 0,
    groupsSeq: 0,
    devicesSeq: 0,
    wanted: null,
    timers: new Set(),
    live: false,
    pushed: new Set(),
    pushedFollowers: {},
    devicesRev: undefined,
    devicesWant: undefined,
  });
  useEffect(() => {
    householdRef.current.devices = devices;
    householdRef.current.skip = skip;
  }, [devices, skip]);
  return householdRef;
}

// The stream is gone: what it said no longer counts.
export function forgetStream(household: Household): void {
  household.live = false;
  household.pushed.clear();
  household.pushedFollowers = {};
  household.devicesRev = undefined;
  household.devicesWant = undefined;
}
