// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import {
  useEffect,
  useEffectEvent,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from 'react';
import { getMqttStatus, getSettings, saveSettings } from '../lib/api';
import type { MqttConfig } from '../lib/types';
import { usePageVisible } from './usePageVisible';
import { speakerArg, type Room } from './useRooms';

export interface MqttForm {
  enabled: boolean;
  host: string;
  port: string;
  username: string;
  password: string;
  discoveryPrefix: string;
  tls: boolean;
}

export interface BrokerLink {
  connected: boolean;
  lastError: string;
}

const STATUS_POLL_MS = 4000;
// How soon a failed settings read is tried again.
const LOAD_RETRY_MS = 5000;
// The speaker's broker port for an empty field (mqttbridge.DefaultPort, DefaultTLSPort).
// The form only shows them: it saves an empty port as 0, so the speaker picks by TLS.
export const DEFAULT_PORT = 1883;
export const DEFAULT_TLS_PORT = 8883;
export const DEFAULT_DISCOVERY_PREFIX = 'homeassistant';

// The form as a speaker's stored settings fill it; the password is never sent back.
function toForm(m: MqttConfig | undefined): MqttForm {
  return {
    enabled: m?.enabled === true,
    host: m?.host || '',
    port: m?.port !== undefined && m.port !== 0 ? String(m.port) : '',
    username: m?.username || '',
    password: '',
    discoveryPrefix: m?.discoveryPrefix || DEFAULT_DISCOVERY_PREFIX,
    tls: m?.tls === true,
  };
}

// The settings patch a form saves. A blank password keeps each speaker's stored one.
interface MqttPatch {
  enabled: boolean;
  host: string;
  port: number;
  username: string;
  discoveryPrefix: string;
  tls: boolean;
  password?: string;
}

function toPatch(form: MqttForm): MqttPatch {
  // A port that is not a number goes as 0.
  const port = Number(form.port);
  const patch: MqttPatch = {
    enabled: form.enabled,
    host: form.host.trim(),
    port: Number.isNaN(port) ? 0 : port,
    username: form.username,
    discoveryPrefix: form.discoveryPrefix.trim(),
    tls: form.tls,
  };
  if (form.password) patch.password = form.password;
  return patch;
}

// Whether two patches send credentials to the same broker, as the speaker decides it.
const sameBroker = (a: MqttPatch, b: MqttPatch) =>
  a.host === b.host && a.port === b.port && a.tls === b.tls;

// Each online speaker's link to the broker, polled while the page is on screen. It restarts
// when the online set changes (`key`), not on every poll that hands over a new rooms array.
function useBrokerLinks(online: Room[], key: string) {
  const [links, setLinks] = useState<Partial<Record<string, BrokerLink>>>({});
  const visible = usePageVisible();
  const readLinks = useEffectEvent(async () =>
    Promise.all(online.map(async (r) => [r.id, await getMqttStatus(speakerArg(r))] as const))
  );
  useEffect(() => {
    if (!visible) return undefined;
    let live = true;
    const poll = async () => {
      const read = await readLinks();
      if (!live) return;
      const next: Record<string, BrokerLink> = {};
      for (const [id, link] of read) if (link) next[id] = link;
      setLinks(next);
    };
    void poll();
    const id = setInterval(poll, STATUS_POLL_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, [key, visible]);
  return links;
}

export interface HouseholdMqtt {
  form: MqttForm | null;
  loading: boolean;
  loadFailed: boolean;
  setForm: Dispatch<SetStateAction<MqttForm | null>>;
  dirty: boolean;
  hasPassword: boolean;
  links: Partial<Record<string, BrokerLink>>;
  failedOn: string[];
  online: Room[];
  save: () => Promise<void>;
}

// The MQTT broker settings, for every speaker at once. Each speaker runs its
// own bridge under its own base topic, so the broker is the same for all of them; the
// base topic stays per speaker, because one shared topic would collide.
//
// The form is read from the speaker serving the page. If that read fails there is no
// form at all: saving defaults over every speaker's real broker would wipe it.
export function useHouseholdMqtt(rooms: Room[]): HouseholdMqtt {
  const online = rooms.filter((r) => r.online);
  const key = online.map((r) => r.id).join(',');
  // undefined while loading; null when the settings could not be read.
  const [saved, setSaved] = useState<MqttForm | null | undefined>(undefined);
  const [form, setForm] = useState<MqttForm | null>(null);
  const [hasPassword, setHasPassword] = useState(false);
  const [failedOn, setFailedOn] = useState<string[]>([]);
  const [retry, setRetry] = useState(0);

  // The load restarts when the online set changes (`key`), not on every poll that
  // hands over a new rooms array; the event reads the rooms of that moment.
  // The form is the household's, so once read it stays: a speaker coming or going must
  // not reset what is being typed. Only the newest read may land.
  const loadSeq = useRef(0);
  const loadForm = useEffectEvent(() => {
    const source = online.find((r) => r.self) ?? online.at(0);
    if (!source || form) return;
    loadSeq.current += 1;
    const seq = loadSeq.current;
    void getSettings(speakerArg(source)).then((settings) => {
      if (seq !== loadSeq.current) return;
      if (!settings) {
        setSaved(null);
        return;
      }
      const loaded = toForm(settings.mqtt);
      setSaved(loaded);
      setForm(loaded);
      setHasPassword(settings.mqtt.hasPassword);
    });
  });
  useEffect(() => loadForm(), [key, retry]);
  // A failed read is tried again until a form is loaded.
  useEffect(() => {
    if (saved !== null) return undefined;
    const id = setTimeout(() => setRetry((n) => n + 1), LOAD_RETRY_MS);
    return () => clearTimeout(id);
  }, [saved, retry]);

  const links = useBrokerLinks(online, key);

  // Writes the form to every online speaker. Rejects when none took it; the speakers
  // that did not are listed in failedOn, and the form is kept for a retry.
  const save = async () => {
    if (!form) return;
    const submitted = form;
    const patch = toPatch(submitted);
    const results = await Promise.all(
      online.map(async (r) =>
        saveSettings({ mqtt: patch }, speakerArg(r)).then(
          () => null,
          () => r.name
        )
      )
    );
    const failed = results.filter((name): name is string => name !== null);
    setFailedOn(failed);
    if (failed.length === online.length) throw new Error('not saved');
    if (submitted.password) setHasPassword(true);
    // A speaker drops its stored password when the broker changes without a new one
    // (settings.go sameBroker), so the form must not claim one is still set.
    else if (saved && !sameBroker(toPatch(saved), patch)) setHasPassword(false);
    // Until every speaker has it the form stays dirty, password included, so a retry
    // sends the same settings to the ones that missed them.
    if (failed.length > 0) return;
    // What was sent is now stored. Edits made while the request was out are the
    // newest intent and stay in the form, still dirty.
    const stored = { ...submitted, password: '' };
    setSaved(stored);
    setForm((now) => (now === submitted ? stored : now));
  };

  return {
    form,
    loading: saved === undefined,
    loadFailed: saved === null,
    setForm,
    dirty: !!form && JSON.stringify(form) !== JSON.stringify(saved),
    hasPassword,
    links,
    failedOn,
    online,
    save,
  };
}
