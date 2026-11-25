// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect } from 'react';
import { subscribeHousehold } from '../../lib/events';
import { forgetStream, type HouseholdRef, type RoomSetters } from './household';
import type { RoomLoaders as Loaders } from './loaders';

// Room state, groups and the device list are polled only while the household stream is
// down, or for a speaker too old to report them over it.
const DEVICES_MS = 10000;
const STATE_MS = 4000;
const GROUPS_MS = 8000;

// Everything at once: the device list first, then what each room plays and the groups.
export function useRefresh(
  householdRef: HouseholdRef,
  { loadDevices, loadState, loadGroups }: Loaders
): () => Promise<void> {
  return useCallback(async () => {
    householdRef.current.wanted = null; // a refused change is read back as it really is
    await loadDevices();
    await Promise.all([loadState(), loadGroups()]);
  }, [householdRef, loadDevices, loadState, loadGroups]);
}

// Hidden: no polling at all. Back on screen: everything at once, then the timers.
export function usePolling(
  householdRef: HouseholdRef,
  visible: boolean,
  refresh: () => Promise<void>,
  { loadDevices, loadState, loadGroups }: Loaders
): void {
  useEffect(() => {
    if (!visible) return;
    void refresh();
    const a = setInterval(() => {
      const { live, devicesRev, devicesWant } = householdRef.current;
      if (!live || devicesRev === undefined || devicesRev !== devicesWant) void loadDevices();
    }, DEVICES_MS);
    const b = setInterval(loadState, STATE_MS);
    const c = setInterval(() => void loadGroups(), GROUPS_MS);
    const pending = householdRef.current.timers;
    return () => {
      clearInterval(a);
      clearInterval(b);
      clearInterval(c);
      for (const t of pending) clearTimeout(t);
      pending.clear();
    };
  }, [householdRef, visible, refresh, loadDevices, loadState, loadGroups]);
}

// The household stream. Open only while the page is on screen; closing it hands state back
// to the poll, which reconciles everything when the page returns.
export function useHouseholdStream(
  householdRef: HouseholdRef,
  visible: boolean,
  { loadDevices, loadGroups }: Loaders,
  setState: RoomSetters['setState']
): void {
  useEffect(() => {
    if (!visible) return;
    // One record for the whole run; only its fields change.
    const household = householdRef.current;
    const close = subscribeHousehold({
      onOpen: () => {
        household.live = true;
      },
      onError: () => forgetStream(household),
      onState: (who, s) => {
        if (who.self && s.devices !== undefined && s.devices !== household.devicesWant) {
          household.devicesWant = s.devices;
          void loadDevices().then(async () => loadGroups('push'));
        }
        const key = who.self ? '' : who.id;
        if (s.followers && key !== undefined) {
          household.pushedFollowers[key] = s.followers;
          void loadGroups('push');
        }
        const id = who.self ? household.devices.find((d) => d.self)?.id : who.id;
        if (!id || id === household.skip) return;
        household.pushed.add(id);
        household.stateSeq += 1; // a poll in flight predates this push
        setState((prev) => ({
          ...prev,
          [id]: {
            now: s.now ?? prev[id]?.now ?? null,
            volume: s.volume ?? prev[id]?.volume ?? 0,
          },
        }));
      },
    });
    return () => {
      close();
      forgetStream(household);
    };
  }, [householdRef, visible, loadDevices, loadGroups, setState]);
}
