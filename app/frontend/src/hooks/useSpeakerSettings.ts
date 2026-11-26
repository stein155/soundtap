// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useEffectEvent, useRef, useState } from 'react';
import { saveSettings } from '../lib/api';
import type { AlarmConfig, DisplayConfig, Preset, Settings, VersionInfo } from '../lib/types';
import { usePageVisible } from './usePageVisible';
import { useSpeakerResource } from './speaker/resource';
import { refreshHeld, refreshResource, type Held } from './speaker/store';
import type { BluetoothStatus } from '../lib/api';

// What Save commits; everything else on the page acts on its own.
export interface SpeakerForm {
  name: string;
  bass: number;
  // Idle minutes before standby: 0 is the default 20, below 0 is never.
  autoOff: number;
  // What the AUX input is called; empty is "AUX".
  auxName: string;
  alarm: AlarmConfig;
  display: DisplayConfig | null;
}

const FIELDS = ['name', 'bass', 'autoOff', 'auxName', 'alarm', 'display'] as const;

// How often an open page rereads what the speaker reports.
const REFRESH_MS = 15000;

const differs = (a: SpeakerForm, b: SpeakerForm, field: keyof SpeakerForm) =>
  JSON.stringify(a[field]) !== JSON.stringify(b[field]);

const browserZone = () => new Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;

// The standby clock shows, as on stock, in this browser's zone. A panel that has no zone
// yet gets it once, without waiting for a Save.
const giveClockAZone = (d: DisplayConfig | null, speaker: string) => {
  const zone = browserZone();
  if (d && !d.timezone && !d.clockOff && zone)
    void saveSettings({ display: { ...d, timezone: zone } }, speaker).catch(() => {
      // Best effort: the panel asks again on the next open.
    });
};

const formFrom = (s: Settings, fallbackName: string): SpeakerForm => ({
  name: s.name || fallbackName,
  bass: s.bass?.actual ?? 0,
  autoOff: s.autoOff,
  auxName: s.auxName,
  alarm: s.alarm,
  display: s.hasDisplay ? s.display : null,
});

// What this page's own reads of the settings found: the page waits for one rather than
// open on what another page read earlier, and says the speaker is away when it has not
// answered since the page opened.
interface Reached {
  speaker: string;
  state: 'loading' | 'away' | 'answered';
}

// The page's reads, all into the page's one copy of the speaker (speaker/store.ts): the home
// page and the Bluetooth section see what this page reads, and the other way round.
function useReads(speaker: string) {
  const stored = useSpeakerResource('settings', speaker);
  const presets = useSpeakerResource('presets', speaker);
  const bluetooth = useSpeakerResource('bluetooth', speaker);
  const version = useSpeakerResource('version', speaker);
  const [reached, setReached] = useState<Reached>({ speaker, state: 'loading' });
  useEffect(() => {
    let live = true;
    const answered = (ok: boolean) => {
      if (!live) return;
      setReached((r) =>
        ok || r.speaker !== speaker || r.state === 'loading'
          ? { speaker, state: ok ? 'answered' : 'away' }
          : r
      );
    };
    void refreshResource('settings', speaker).then(answered);
    void refreshResource('presets', speaker);
    void refreshResource('bluetooth', speaker);
    void refreshResource('version', speaker);
    return () => {
      live = false;
    };
  }, [speaker]);
  // Signal and address change while a page sits open, so it reads them again; a hidden page
  // does not. The form stays as the owner left it.
  const visible = usePageVisible();
  useEffect(() => {
    if (!visible) return undefined;
    let live = true;
    const id = window.setInterval(() => {
      void refreshResource('settings', speaker).then(
        (ok) => live && ok && setReached({ speaker, state: 'answered' })
      );
    }, REFRESH_MS);
    return () => {
      live = false;
      window.clearInterval(id);
    };
  }, [speaker, visible]);
  const state = reached.speaker === speaker ? reached.state : 'loading';
  // undefined while loading; null when the speaker did not answer.
  const settings = settingsAs(state, stored);
  return {
    settings,
    presets: presets ?? [],
    bluetooth: bluetooth ?? null,
    version: version ?? null,
  };
}

// What the page holds of the settings: undefined while loading, null when the speaker did
// not answer, else what it said.
function settingsAs(state: Reached['state'], stored: Held<'settings'>): Held<'settings'> {
  if (state === 'loading') return undefined;
  return state === 'away' ? null : stored;
}

// The queue goes on past an earlier save's refusal.
const afterEarlier = () => {
  // That refusal was already reported to the save that made it.
};

export interface SpeakerSettings {
  settings: Held<'settings'>;
  presets: (Preset | null)[];
  bluetooth: BluetoothStatus | null;
  version: VersionInfo | null;
  form: SpeakerForm | null;
  set: <K extends keyof SpeakerForm>(field: K, value: SpeakerForm[K]) => void;
  commit: <K extends keyof SpeakerForm>(field: K, value: SpeakerForm[K]) => Promise<boolean>;
  dirty: boolean;
  save: () => Promise<boolean>;
}

// One speaker's settings page: what it reports, and a local form for the fields Save
// commits. Only changed fields are sent, so saving a new name never re-sends a display
// setting the speaker would validate again.
export function useSpeakerSettings(speaker: string, fallbackName: string): SpeakerSettings {
  const { settings, presets, bluetooth, version } = useReads(speaker);
  const [saved, setSaved] = useState<SpeakerForm | null>(null);
  const [form, setForm] = useState<SpeakerForm | null>(null);
  // Only a fallback for a speaker that reports no name. Discovery refreshes it every few
  // seconds, and reloading on that would throw away unsaved edits.
  const fallback = useRef(fallbackName);
  const baseline = useRef<{ speaker: string; form: SpeakerForm } | null>(null);
  const queue = useRef<Promise<unknown>>(Promise.resolve());
  useEffect(() => {
    fallback.current = fallbackName;
  });

  // Once per speaker, from whichever read answers first: a refresh can be the first,
  // and running this again would throw away unsaved edits.
  const startForm = useEffectEvent((s: Settings) => {
    if (baseline.current?.speaker === speaker) return;
    const loaded = formFrom(s, fallback.current);
    baseline.current = { speaker, form: loaded };
    giveClockAZone(loaded.display, speaker);
    setSaved(loaded);
    setForm(loaded);
  });

  useEffect(() => {
    if (settings) startForm(settings);
  }, [settings]);

  const changed = (field: keyof SpeakerForm) => !!form && !!saved && differs(form, saved, field);

  const send = async (sent: SpeakerForm) => {
    const base = baseline.current;
    if (base?.speaker !== speaker) return false; // the room changed while this waited
    const patch: Partial<Record<(typeof FIELDS)[number], unknown>> = {};
    for (const field of FIELDS)
      if (differs(sent, base.form, field) && sent[field] !== null)
        patch[field] = field === 'name' ? sent.name.trim() : sent[field];
    if (patch.name === '') delete patch.name;
    // A speaker's own clock is UTC: the alarm is read in the zone of the browser it was
    // set from.
    if (patch.alarm !== undefined) patch.alarm = { ...sent.alarm, timezone: browserZone() };
    // The standby clock keeps this browser's time, like the alarm.
    if (sent.display && patch.display !== undefined)
      patch.display = { ...sent.display, timezone: browserZone() ?? '' };
    if (Object.keys(patch).length > 0) {
      await saveSettings(patch, speaker);
      refreshHeld('settings', speaker); // the home page's copy and this page's own
    }
    // A blank name was not sent, so the old one stays.
    const accepted = { ...sent, name: sent.name.trim() || base.form.name };
    // eslint-disable-next-line require-atomic-updates -- saves go out one at a time (queue below)
    baseline.current = { speaker, form: accepted };
    setSaved(accepted);
    setForm((f) => (f === sent ? accepted : f));
    return 'name' in patch;
  };

  // Two SaveBars have their own busy state, so saves can be asked for side by side. They go
  // out one at a time, each against what the speaker accepted last: requests that overlap
  // can land in either order, and no answer says which value stuck.
  // Rejects with the speaker's reason when it refuses; returns whether the name changed.
  const save = async () => {
    if (!form) return false;
    const sent = form;
    const turn = queue.current.catch(afterEarlier).then(async () => send(sent));
    queue.current = turn;
    return turn;
  };

  const set = <K extends keyof SpeakerForm>(field: K, value: SpeakerForm[K]) =>
    setForm((f) => (f ? { ...f, [field]: value } : f));

  // A switch, a slider or a choice goes to the speaker at once: this field alone, against
  // what the speaker accepted last, so an unsaved name typed elsewhere is not sent with it.
  // In the same queue as save. Rejects with the speaker's reason when it refuses.
  const commit = async <K extends keyof SpeakerForm>(field: K, value: SpeakerForm[K]) => {
    set(field, value);
    const turn = queue.current.catch(afterEarlier).then(async () => {
      const base = baseline.current;
      if (base?.speaker !== speaker) return false;
      return send({ ...base.form, [field]: value }).catch((e: unknown) => {
        // Refused: the control goes back to what the speaker still has.
        const kept = baseline.current?.form[field];
        if (kept !== undefined) set(field, kept);
        throw e;
      });
    });
    queue.current = turn;
    return turn;
  };

  return {
    settings,
    presets,
    bluetooth,
    version,
    form,
    set,
    commit,
    dirty: FIELDS.some(changed),
    save,
  };
}
