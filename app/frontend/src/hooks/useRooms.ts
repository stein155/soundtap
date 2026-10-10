// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { getUserSources } from '../lib/api';
import { PREF, readPref, writePref } from '../lib/storage';
import type { NowPlaying, SpeakerDevice } from '../lib/types';
import { usePageVisible } from './usePageVisible';
import { speakerArg, useHousehold, type Leaders, type RoomStates } from './rooms/household';
import { useRoomLoaders } from './rooms/loaders';
import { useHouseholdStream, usePolling, useRefresh } from './rooms/live';

export { speakerArg };

// One speaker as the home screen shows it: a room. `linkedTo` is the id of the speaker
// whose stream it plays along with; a room without one leads (or plays alone).
export interface Room {
  id: string;
  name: string;
  model: string;
  ip: string;
  online: boolean;
  self: boolean;
  now: NowPlaying | null;
  volume: number;
  sources: string[];
  linkedTo: string | null;
  connecting?: boolean;
}

// The household as this browser last saw it, so a cold start (the home-screen app, a
// reload) shows the rooms at once instead of an empty page while discovery answers.
// Only a speaker can serve the page, so this is never shown without one to correct it.
export function lastRooms(): Room[] {
  try {
    const rooms: unknown = JSON.parse(readPref(PREF.rooms) ?? '');
    return Array.isArray(rooms) ? (rooms as Room[]) : [];
  } catch {
    return [];
  }
}

export function keepRooms(rooms: Room[]): void {
  writePref(PREF.rooms, JSON.stringify(rooms.map(({ connecting: _, ...r }) => r)));
}

// A group change shows in the leader's snapserver once the follower's snapclient is
// connected, which takes several seconds; until then the change shown here stands. The
// last check falls after the hold, so what the speakers really say ends it.
const REGROUP_HOLD_MS = 20000;
const REGROUP_CHECKS_MS = [1500, 4000, 8000, 14000, REGROUP_HOLD_MS + 500];
// The least time the retry spinner shows, so a quick answer still reads as "tried".
const RETRY_MIN_MS = 1200;

// Which inputs each speaker offers is a property of its model; ask once per speaker.
function useUserSources(devices: SpeakerDevice[], seed: Room[]) {
  const [sources, setSources] = useState<Record<string, string[]>>(() =>
    Object.fromEntries(seed.map((r) => [r.id, r.sources]))
  );
  const sourcesAsked = useRef(new Set<string>());
  useEffect(() => {
    for (const d of devices) {
      if (!d.online || sourcesAsked.current.has(d.id)) continue;
      sourcesAsked.current.add(d.id);
      // Only an answer is kept; a failed read is asked again with the next device list.
      Promise.resolve(getUserSources(speakerArg(d)))
        .then((list) => {
          if (list) setSources((s) => ({ ...s, [d.id]: list }));
          else sourcesAsked.current.delete(d.id);
        })
        .catch(() => sourcesAsked.current.delete(d.id));
    }
  }, [devices]);
  return sources;
}

interface RoomParts {
  state: RoomStates;
  sources: Record<string, string[]>;
  leaders: Leaders;
  connecting: Record<string, boolean>;
}

const roomOf = (d: SpeakerDevice, p: RoomParts): Room => ({
  id: d.id,
  name: d.name,
  model: d.model,
  ip: d.ip,
  online: d.online,
  self: d.self,
  now: p.state[d.id]?.now ?? null,
  volume: p.state[d.id]?.volume ?? 0,
  sources: p.sources[d.id] ?? [],
  linkedTo: p.leaders[d.id] ?? null,
  connecting: p.connecting[d.id],
});

// useRooms keeps every speaker on the network in view: which exist, what each plays, how
// loud, and who follows whom. Groups are read from each leader's snapserver -- the
// connected clients other than itself are the speakers playing along -- so a group made
// from another phone shows up here too.
//
// `skip` is the room the page already follows live (useSpeaker, over SSE); polling it
// again would only race that stream.
export function useRooms(skip: string | null) {
  const [seed] = useState(lastRooms);
  const [devices, setDevices] = useState<SpeakerDevice[]>(() =>
    seed.map(({ id, name, model, ip, online, self }) => ({ id, name, model, ip, online, self }))
  );
  const [state, setState] = useState<RoomStates>(() =>
    Object.fromEntries(seed.map((r) => [r.id, { now: r.now, volume: r.volume }]))
  );
  const [leaders, setLeaders] = useState<Leaders>(() =>
    Object.fromEntries(seed.flatMap((r) => (r.linkedTo ? [[r.id, r.linkedTo]] : [])))
  );
  const [connecting, setConnecting] = useState<Record<string, boolean>>({});
  const [loaded, setLoaded] = useState(false);
  const visible = usePageVisible();
  const householdRef = useHousehold(devices, skip);
  const loaders = useRoomLoaders(householdRef, { setDevices, setLoaded, setState, setLeaders });
  const { loadDevices, loadState, loadGroups } = loaders;
  const refresh = useRefresh(householdRef, loaders);
  usePolling(householdRef, visible, refresh, loaders);
  useHouseholdStream(householdRef, visible, loaders, setState);
  const sources = useUserSources(devices, seed);

  // Show a change before the next poll confirms it (a volume drag, a stop).
  const patch = useCallback((id: string, p: { now?: NowPlaying | null; volume?: number }) => {
    setState((prev) => ({ ...prev, [id]: { ...(prev[id] ?? { now: null, volume: 0 }), ...p } }));
  }, []);

  // A group change is visible in the snapserver a moment after the follow call returns.
  const regroup = useCallback(
    (next: Leaders) => {
      householdRef.current.groupsSeq += 1; // a poll already in flight predates this change
      householdRef.current.wanted = { leaders: next, until: Date.now() + REGROUP_HOLD_MS };
      setLeaders(next);
      for (const ms of REGROUP_CHECKS_MS) {
        const t = setTimeout(() => {
          householdRef.current.timers.delete(t);
          void loadGroups('ask');
        }, ms);
        householdRef.current.timers.add(t);
      }
    },
    [householdRef, loadGroups]
  );

  // Retry an offline speaker: discovery decides, the spinner only says we asked.
  const reconnect = useCallback(
    async (id: string) => {
      setConnecting((c) => ({ ...c, [id]: true }));
      await Promise.all([loadDevices(), new Promise((r) => setTimeout(r, RETRY_MIN_MS))]);
      setConnecting((c) => ({ ...c, [id]: false }));
      void loadState();
    },
    [loadDevices, loadState]
  );

  const rooms = devices.map((d) => roomOf(d, { state, sources, leaders, connecting }));
  return { rooms, loaded, leaders, refresh, patch, regroup, reconnect, loadState };
}
