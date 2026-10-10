// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { getBluetooth, getPresets, getSettings, getVersion } from '../../lib/api';
import type { BluetoothStatus } from '../../lib/api';
import type { Preset, Settings, VersionInfo } from '../../lib/types';

// The slow-changing reads of one speaker, held once for the whole page: the home page, the
// settings page and the sections inside it read the same entry, so a save or a poll on one
// shows on all of them instead of each keeping its own copy that drifts. Now-playing and
// volume are not here: useSpeaker follows them live, with optimistic holds of its own.
export interface SpeakerResources {
  settings: Settings;
  version: VersionInfo;
  presets: (Preset | null)[];
  bluetooth: BluetoothStatus;
}
export type ResourceKind = keyof SpeakerResources;

// Read through a function, not a table of the imports: a test that mocks lib/api with only
// the reads it needs must still be able to load this module.
const READ: { [K in ResourceKind]: (speaker: string) => Promise<SpeakerResources[K] | null> } = {
  settings: (speaker) => getSettings(speaker),
  version: (speaker) => getVersion(speaker),
  presets: (speaker) => getPresets(speaker),
  bluetooth: (speaker) => getBluetooth(speaker),
};

// undefined until the first answer; null when the speaker did not answer and nothing it
// said before is known. A failed read after a good one keeps the good one.
export type Held<K extends ResourceKind> = SpeakerResources[K] | null | undefined;

type Entry = {
  value: unknown;
  // Monotonic per entry: only the newest read may land, so a slow answer can never
  // overwrite a newer one (a poll sent before a "forget", a read before a save).
  seq: number;
  inflight: Promise<boolean> | null;
  listeners: Set<() => void>;
};

const entries = new Map<string, Entry>();
const keyOf = (kind: ResourceKind, speaker: string) => `${kind}\u0000${speaker}`;

function entry(kind: ResourceKind, speaker: string): Entry {
  const key = keyOf(kind, speaker);
  let e = entries.get(key);
  if (!e) {
    e = { value: undefined, seq: 0, inflight: null, listeners: new Set() };
    entries.set(key, e);
  }
  return e;
}

function publish(e: Entry, value: unknown) {
  e.value = value;
  e.listeners.forEach((l) => l());
}

export function snapshot<K extends ResourceKind>(kind: K, speaker: string): Held<K> {
  return entries.get(keyOf(kind, speaker))?.value as Held<K>;
}

// Reads the speaker now; resolves whether it answered. Every caller of a fresh read gets
// one: a read that started earlier may have been answered before what the caller changed.
export function refreshResource(kind: ResourceKind, speaker: string): Promise<boolean> {
  const e = entry(kind, speaker);
  const seq = ++e.seq;
  const read = READ[kind](speaker).then(
    (value) => {
      if (seq !== e.seq || entries.get(keyOf(kind, speaker)) !== e) return value !== null;
      if (value !== null || e.value === undefined) publish(e, value);
      return value !== null;
    },
    () => false
  );
  const done = read.finally(() => {
    if (e.inflight === done) e.inflight = null;
    forgetUnread(kind, speaker, e);
  });
  e.inflight = done;
  return done;
}

// Reads again only what a page holds: a change made elsewhere (a save, a household
// preference) shows where it is on screen, and costs nothing where it is not.
export function refreshHeld(kind: ResourceKind, speaker: string) {
  const e = entries.get(keyOf(kind, speaker));
  if (e && e.listeners.size > 0) void refreshResource(kind, speaker);
}

// Reads once: nothing if an answer is held or a read is on its way.
export function ensureResource(kind: ResourceKind, speaker: string): Promise<boolean> {
  const e = entry(kind, speaker);
  if (e.inflight) return e.inflight;
  if (e.value !== undefined) return Promise.resolve(e.value !== null);
  return refreshResource(kind, speaker);
}

// Drops whatever read is on its way: an action changed what it would report.
export function discardResource(kind: ResourceKind, speaker: string) {
  const e = entries.get(keyOf(kind, speaker));
  if (e) {
    e.seq++;
    e.inflight = null;
  }
}

// An entry lives while someone reads it: a page opened later reads the speaker again
// rather than trusting what a page closed long ago saw.
function forgetUnread(kind: ResourceKind, speaker: string, e: Entry) {
  if (e.listeners.size === 0 && !e.inflight && entries.get(keyOf(kind, speaker)) === e)
    entries.delete(keyOf(kind, speaker));
}

export function subscribeResource(kind: ResourceKind, speaker: string, listener: () => void) {
  const e = entry(kind, speaker);
  e.listeners.add(listener);
  return () => {
    e.listeners.delete(listener);
    forgetUnread(kind, speaker, e);
  };
}

// For tests: no entry outlives the test that made it.
export function resetSpeakerResources() {
  entries.clear();
}
