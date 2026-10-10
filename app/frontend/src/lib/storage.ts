// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Per-browser preferences in localStorage. Every access can throw (private windows,
// blocked site data); then a preference lives in memory and lasts until reload.

const memory = new Map<string, string>();

export function readPref(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return memory.get(key) ?? null;
  }
}

export function writePref(key: string, value: string): void {
  memory.set(key, value);
  try {
    localStorage.setItem(key, value);
  } catch {
    /* not persisted: the memory copy lasts until reload */
  }
}

export function removePref(key: string): void {
  memory.delete(key);
  try {
    localStorage.removeItem(key);
  } catch {
    /* nothing stored to remove */
  }
}

// The keys, in one place. Existing ones keep their historical spelling so a browser's
// choices survive the upgrade.
export const PREF = {
  lang: 'soundtap-lang',
  theme: 'soundtap-theme',
  device: 'soundtap.device',
  rooms: 'soundtap.rooms',
  swipeTip: 'soundtap.swipe-tip',
} as const;
