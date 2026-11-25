// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { getSettings, saveSettings } from '../lib/api';
import { speakerArg, type Room } from './useRooms';
import { refreshHeld } from './speaker/store';
import type { IntegrationsPrefs, MultiroomPrefs } from '../lib/types';

const RETRY_MS = 5000;

// Writes one preference to each of `targets`; resolves to those that refused it.
const write = async (key: string, value: unknown, targets: Room[]) =>
  Promise.all(
    targets.map(async (r) =>
      saveSettings({ [key]: value }, speakerArg(r)).then(
        () => {
          // The home page's copy of this speaker's settings (its group volume) follows.
          refreshHeld('settings', speakerArg(r));
          return null;
        },
        () => r
      )
    )
  ).then((failed) => failed.filter((r): r is Room => r !== null));

// The household preferences a save writes, by settings key.
interface Prefs {
  multiroom: MultiroomPrefs;
  integrations: IntegrationsPrefs;
}

export interface HouseholdPrefs {
  multiroom: MultiroomPrefs | null;
  saveMultiroom: (next: MultiroomPrefs) => Promise<void>;
  mqttHost: string;
  integrations: IntegrationsPrefs | null;
  saveIntegrations: (next: IntegrationsPrefs) => Promise<void>;
}

// Settings that hold for the whole household: read from the speaker serving the page,
// written to every speaker that is online, since any of them may lead a group. Until
// they have been read there is nothing to change: a toggle would otherwise write a
// default over the other preference.
export function useHouseholdPrefs(rooms: Room[]): HouseholdPrefs {
  const [multiroom, setMultiroom] = useState<MultiroomPrefs | null>(null);
  const [mqttHost, setMqttHost] = useState('');
  const [integrations, setIntegrations] = useState<IntegrationsPrefs | null>(null);
  const roomsRef = useRef(rooms);
  useEffect(() => {
    roomsRef.current = rooms;
  }, [rooms]);

  // The last value a speaker accepted (or was read): what a failed save falls back to,
  // not the unsaved optimistic value another save may have shown meanwhile.
  const confirmed = useRef<Partial<Prefs>>({});

  // Read once a speaker is online to read from; with no room known yet at mount, that
  // is when discovery first finds one.
  const anyOnline = rooms.some((r) => r.online);
  const loaded = multiroom !== null;
  // A read that failed (a timeout, a speaker rebooting) is tried again a little later,
  // instead of leaving the multiroom controls hidden until the view is reopened.
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (loaded) return undefined;
    const source =
      roomsRef.current.find((r) => r.self && r.online) ?? roomsRef.current.find((r) => r.online);
    if (!source) return undefined;
    let current = true;
    let retry: ReturnType<typeof setTimeout> | undefined;
    void getSettings(speakerArg(source)).then((settings) => {
      if (!current) return;
      if (!settings) {
        retry = setTimeout(() => setAttempt((n) => n + 1), RETRY_MS);
        return;
      }
      confirmed.current = { multiroom: settings.multiroom, integrations: settings.integrations };
      setMultiroom(settings.multiroom);
      setMqttHost(settings.mqtt.enabled ? settings.mqtt.host : '');
      setIntegrations(settings.integrations);
    });
    return () => {
      current = false;
      clearTimeout(retry);
    };
  }, [anyOnline, loaded, attempt]);

  // Bumped per save of a key, so only the newest one may roll back and an older
  // one still waiting is dropped: the newer value goes out instead.
  const saves = useRef<Record<string, number>>({});
  const closed = useRef(false); // a closed view must not retry over a reopened one
  useEffect(() => {
    closed.current = false;
    return () => void (closed.current = true);
  }, []);
  // Saves go out one after another, so on every speaker the later choice lands last.
  const queue = useRef<Promise<unknown>>(Promise.resolve());
  // Shows `next` at once and writes it to every online speaker; if not one of them took
  // it, puts back what is really stored.
  const saveEverywhere = async <K extends keyof Prefs>(
    key: K,
    next: Prefs[K],
    show: (v: Prefs[K] | null) => void
  ) => {
    const mine = (saves.current[key] = (saves.current[key] ?? 0) + 1);
    show(next);
    // A speaker that missed this save while others took it gets it again a little later,
    // as long as no newer value went out, so the household does not drift apart.
    const retry = (targets: Room[], left: number) =>
      setTimeout(() => {
        queue.current = queue.current.then(async () => {
          if (closed.current || mine !== saves.current[key]) return;
          const failed = await write(key, next, targets);
          if (failed.length > 0 && left > 1) retry(failed, left - 1);
        });
      }, RETRY_MS);
    const turn = queue.current.then(async () => {
      if (mine !== saves.current[key]) return;
      const online = roomsRef.current.filter((r) => r.online);
      const failed = await write(key, next, online);
      if (failed.length < online.length) {
        confirmed.current[key] = next;
        if (failed.length > 0) retry(failed, 3);
      } else if (mine === saves.current[key]) show(confirmed.current[key] ?? null);
    });
    queue.current = turn;
    return turn;
  };
  const saveMultiroom = async (next: MultiroomPrefs) =>
    saveEverywhere('multiroom', next, setMultiroom);
  const saveIntegrations = async (next: IntegrationsPrefs) =>
    saveEverywhere('integrations', next, setIntegrations);

  return { multiroom, saveMultiroom, mqttHost, integrations, saveIntegrations };
}
